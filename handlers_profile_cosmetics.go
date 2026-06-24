package main

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

type ProfileCosmeticEntry struct {
	CosmeticCatalogEntryPublic
	RawUrl string `json:"raw_url"`
}

func (e CosmeticCatalogEntry) ToProfile() ProfileCosmeticEntry {
	return ProfileCosmeticEntry{
		CosmeticCatalogEntryPublic: e.ToPublic(),
		RawUrl:                     buildCosmeticRawUrl(e.CosmeticType, e.Id),
	}
}

func buildCosmeticRawUrl(ct CosmeticType, id string) string {
	base := strings.TrimRight(BASE_URL, "/")
	switch ct {
	case CosmeticTypeOverlay:
		return fmt.Sprintf("%s/cosmetics/overlays/%s.gif", base, id)
	}
	return ""
}

func buildActiveCosmeticsDetails(uc *UserCosmetics) map[CosmeticType]ProfileCosmeticEntry {
	cosmeticsCatalogMu.RLock()
	defer cosmeticsCatalogMu.RUnlock()

	activeDetails := make(map[CosmeticType]ProfileCosmeticEntry, len(uc.ActiveCosmetics))
	for ct, id := range uc.ActiveCosmetics {
		rawUrl := buildCosmeticRawUrl(ct, id)
		var found *CosmeticCatalogEntry
		for i := range cosmeticsCatalog {
			if cosmeticsCatalog[i].Id == id && cosmeticsCatalog[i].CosmeticType == ct {
				found = &cosmeticsCatalog[i]
				break
			}
		}
		if found != nil {
			entry := found.ToProfile()
			if entry.RawUrl == "" {
				entry.RawUrl = rawUrl
			}
			activeDetails[ct] = entry
		} else {
			activeDetails[ct] = ProfileCosmeticEntry{
				CosmeticCatalogEntryPublic: CosmeticCatalogEntryPublic{
					Id:           id,
					CosmeticType: ct,
					Name:         id,
					ImageUrl:     rawUrl,
				},
				RawUrl: rawUrl,
			}
		}
	}
	return activeDetails
}

func getProfileCosmetics(c *gin.Context) {
	username := Username(c.Param("username"))
	if username == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Username is required"})
		return
	}

	foundUser, err := getAccountByUsername(username)
	if err != nil || foundUser == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "User not found"})
		return
	}
	if foundUser.IsBanned() {
		c.JSON(http.StatusForbidden, gin.H{"error": "User is banned"})
		return
	}

	userId := foundUser.GetId()
	uc, err := loadUserCosmetics(userId)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to load cosmetics"})
		return
	}

	activeDetails := buildActiveCosmeticsDetails(uc)

	c.JSON(http.StatusOK, gin.H{
		"username":         foundUser.GetUsername(),
		"id":               userId,
		"active_cosmetics": activeDetails,
	})
}

const MaxBatchProfileCosmeticsUsernames = 100

func getProfileCosmeticsBatch(c *gin.Context) {
	var rawUsernames []string

	for _, u := range c.QueryArray("usernames") {
		for _, part := range strings.Split(u, ",") {
			rawUsernames = append(rawUsernames, part)
		}
	}
	if len(rawUsernames) == 0 {
		if u := c.Query("usernames"); u != "" {
			rawUsernames = strings.Split(u, ",")
		}
	}

	if c.Request.Method == http.MethodPost && len(rawUsernames) == 0 {
		var body struct {
			Usernames []string `json:"usernames"`
		}
		if err := c.ShouldBindJSON(&body); err == nil {
			rawUsernames = body.Usernames
		}
	}

	seen := make(map[string]struct{}, len(rawUsernames))
	usernames := make([]string, 0, len(rawUsernames))
	for _, u := range rawUsernames {
		u = strings.TrimSpace(u)
		if u == "" {
			continue
		}
		key := strings.ToLower(u)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		usernames = append(usernames, key)
	}

	if len(usernames) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "usernames parameter is required (provide via ?usernames=a,b or JSON body {\"usernames\":[...]})"})
		return
	}
	if len(usernames) > MaxBatchProfileCosmeticsUsernames {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": fmt.Sprintf("too many usernames (max %d per request)", MaxBatchProfileCosmeticsUsernames),
		})
		return
	}

	results := make(map[string]gin.H, len(usernames))

	for _, u := range usernames {
		foundUser, err := getAccountByUsername(u)
		if err != nil || foundUser == nil {
			continue
		}
		if foundUser.IsBanned() {
			continue
		}

		userId := foundUser.GetId()
		uc, err := loadUserCosmetics(userId)
		if err != nil {
			continue
		}

		activeDetails := buildActiveCosmeticsDetails(uc)
		if len(activeDetails) == 0 {
			continue
		}

		results[u] = gin.H{
			"username":         foundUser.GetUsername(),
			"id":               userId,
			"active_cosmetics": activeDetails,
		}
	}

	c.JSON(http.StatusOK, results)
}
