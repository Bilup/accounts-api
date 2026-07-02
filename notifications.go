package main

import (
	"bytes"
	"encoding/json"
	"log"
	"maps"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

func getNotifications(c *gin.Context) {
	user := currentUser(c)

	timePeriod := 1
	if timePeriodStr := c.Query("after"); timePeriodStr != "" {
		if parsed, err := strconv.Atoi(timePeriodStr); err == nil && parsed >= 1 {
			timePeriod = parsed
		} else {
			c.JSON(400, gin.H{"error": "Invalid time period"})
			return
		}
	}

	userId := user.GetId()
	currentTime := time.Now().UnixMilli()
	cutoffTime := currentTime - int64(timePeriod*24*60*60*1000)

	notifications := make([]map[string]any, 0)

	eventsHistoryMutex.RLock()
	userEvents, exists := eventsHistory[userId]
	eventsHistoryMutex.RUnlock()

	if exists {
		for _, event := range userEvents {
			if event.Timestamp >= cutoffTime {
				notification := map[string]any{}
				maps.Copy(notification, event.Data)
				notification["type"] = event.Type
				notification["id"] = event.ID
				notification["timestamp"] = event.Timestamp
				notifications = append(notifications, notification)
			}
		}
	}

	sort.Slice(notifications, func(i, j int) bool {
		return notifications[i]["timestamp"].(int64) > notifications[j]["timestamp"].(int64)
	})

	c.JSON(200, notifications)
}

func makeHTTPRequest(method, url string, payload any, timeout time.Duration, logPrefix string, expectedStatusCode int) bool {
	jsonData, err := json.Marshal(payload)
	if err != nil {
		log.Printf("[%s] Error marshaling payload: %v", logPrefix, err)
		return false
	}

	isRetryableNetErr := func(e error) bool {
		if e == nil {
			return false
		}
		s := strings.ToLower(e.Error())
		return strings.Contains(s, "connection reset") ||
			strings.Contains(s, "connection refused") ||
			strings.Contains(s, "server closed idle connection") ||
			strings.Contains(s, "broken pipe") ||
			strings.Contains(s, "eof")
	}

	maxAttempts := 4 // initial try + 3 retries
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		client := &http.Client{Timeout: timeout}

		var resp *http.Response
		switch method {
		case "POST":
			resp, err = client.Post(url, "application/json", bytes.NewBuffer(jsonData))
		case "PATCH":
			req, reqErr := http.NewRequest("PATCH", url, bytes.NewBuffer(jsonData))
			if reqErr != nil {
				log.Printf("[%s] Error creating %s request: %v", logPrefix, method, reqErr)
				return false
			}
			req.Header.Set("Content-Type", "application/json")
			resp, err = client.Do(req)
		default:
			log.Printf("[%s] Unsupported HTTP method: %s", logPrefix, method)
			return false
		}

		if err != nil {
			if attempt < maxAttempts && isRetryableNetErr(err) {
				// Backoff: 200ms, 400ms, 800ms
				backoff := time.Duration(200*(1<<(attempt-1))) * time.Millisecond
				time.Sleep(backoff)
				continue
			}
			log.Printf("[%s] Error sending %s request after %d attempt(s): %v", logPrefix, method, attempt, err)
			return false
		}
		defer resp.Body.Close()

		if resp.StatusCode == expectedStatusCode {
			return true
		}

		// Status-code failures generally aren't transient; don't spam retries.
		log.Printf("[%s] Request failed with status: %d", logPrefix, resp.StatusCode)
		return false
	}

	return false
}

func createEventPayload(eventType string, data any) map[string]any {
	return map[string]any{
		"event_type": eventType,
		"data":       data,
		"from":       "rotur",
	}
}

func broadcastClawEvent(eventType string, data any) bool {
	clawRealtimeReceiveEvent(eventType, data)
	return true
}

func sendPostToDiscord(postData NetPost) {
	username := postData.User
	if username == "" {
		username = "Unknown User"
	}

	webhookData := map[string]any{
		"username":   username,
		"avatar_url": avatarURL(username),
		"content":    postData.Content,
	}

	success := makeHTTPRequest("POST", DISCORD_WEBHOOK_URL, webhookData, 5*time.Second, "Discord", 204)
	if success {
		log.Printf("[Discord] Post %s sent to Discord successfully", postData.ID)
	}
}

func sendReportToDiscord(reportData string) {
	webhookData := map[string]any{
		"username":   "Rotur",
		"avatar_url": avatarURL("rotur"),
		"content":    "<@603952506330021898> Reported: " + reportData,
	}

	success := makeHTTPRequest("POST", DISCORD_WEBHOOK_URL, webhookData, 5*time.Second, "Discord", 204)
	if success {
		log.Printf("[Discord] Report sent to Discord successfully")
	}
}

func notify(eventType string, data any) bool {
	payload := createEventPayload(eventType, data)
	success := makeHTTPRequest("POST", EVENT_SERVER_URL, payload, 5*time.Second, "Event", 200)
	if success {
		log.Printf("[Event] Event %s sent to Event server successfully", eventType)
	}
	return success
}

func broadcastUserUpdate(username Username, key string, value any) bool {
	mu := getMutexForUser(username.Id().User())
	mu.Lock()
	defer mu.Unlock()
	payload := createEventPayload("user_account_update", map[string]any{
		"username": username,
		"key":      key,
		"value":    value,
		"rotur":    username,
	})

	success := makeHTTPRequest("POST", EVENT_SERVER_URL, payload, 2*time.Second, "UserUpdate", 200)
	return success
}

func getPostRepliesSnapshot(postID string) []Reply {
	postsMutex.RLock()
	defer postsMutex.RUnlock()
	for i := range posts {
		if posts[i].ID == postID {
			if posts[i].Replies == nil {
				return nil
			}
			out := make([]Reply, len(posts[i].Replies))
			copy(out, posts[i].Replies)
			return out
		}
	}
	return nil
}

func addUserEvent(userId UserId, eventType string, data map[string]any) Event {
	switch eventType {
	case "reply":
		postID, _ := data["post_id"].(string)
		if postID != "" {
			replies := getPostRepliesSnapshot(postID)
			netReplies := make([]NetReply, 0)
			for _, reply := range replies {
				netReplies = append(netReplies, reply.ToNet())
			}
			go broadcastClawEvent("update_post", map[string]any{
				"id":   postID,
				"key":  "replies",
				"data": netReplies,
			})
		}
	}

	eventsHistoryMutex.Lock()
	if eventsHistory[userId] == nil {
		eventsHistory[userId] = make([]Event, 0)
	}

	newEvent := Event{
		Type:      eventType,
		Data:      data,
		Timestamp: time.Now().UnixMilli(),
		ID:        generateShortToken(),
	}

	eventsHistory[userId] = append(eventsHistory[userId], newEvent)

	if len(eventsHistory[userId]) > 100 {
		eventsHistory[userId] = eventsHistory[userId][len(eventsHistory[userId])-100:]
	}
	eventsHistoryMutex.Unlock()

	go saveEventsHistory()
	clawRealtimeSendUserEvent(userId, newEvent)

	return newEvent
}
