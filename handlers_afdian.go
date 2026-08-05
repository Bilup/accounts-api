package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
)

// handleAfdianWebhook 处理爱发电订单回调。
// 爱发电要求返回 {"ec":200,"em":""} 表示成功收到，否则会重试。
// 验签失败返回 400（安全边界），业务处理失败仍返回 ec:200（避免无限重试）。
func handleAfdianWebhook(c *gin.Context) {
	rawBody, err := io.ReadAll(c.Request.Body)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ec": 400, "em": "read body failed"})
		return
	}

	var payload struct {
		EC   int    `json:"ec"`
		EM   string `json:"em"`
		Data struct {
			Type  string      `json:"type"`
			Order AfdianOrder `json:"order"`
			Sign  string      `json:"sign"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rawBody, &payload); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ec": 400, "em": "invalid json"})
		return
	}

	order := payload.Data.Order

	// 1. 验签（安全边界）
	if !verifyAfdianSign(order.OutTradeNo, order.UserId, order.PlanId, order.TotalAmount, payload.Data.Sign) {
		log.Printf("[afdian] signature verification failed: out_trade_no=%s", order.OutTradeNo)
		c.JSON(http.StatusBadRequest, gin.H{"ec": 400, "em": "invalid signature"})
		return
	}

	// 2. 商户校验
	if !verifyAfdianMerchant(order.UserId) {
		log.Printf("[afdian] merchant mismatch: user_id=%s", order.UserId)
		c.JSON(http.StatusBadRequest, gin.H{"ec": 400, "em": "merchant mismatch"})
		return
	}

	// 3. 幂等检查（原子预约，防并发重复处理）
	if !reserveAfdianOrder(order.OutTradeNo) {
		afdianOK(c)
		return
	}

	// 4. 只处理已支付订单（status=2）
	if order.Status != 2 {
		afdianOK(c)
		return
	}

	// 5. 用户匹配（通过 custom_order_id，前端跳转支付时传用户名）
	var username string
	var matched User
	if order.CustomOrderId != "" {
		if u, err := getAccountByUsername(Username(order.CustomOrderId)); err == nil {
			matched = u
			username = string(u.GetUsername())
		}
	}

	// 6. 按 product_type 分发
	switch order.ProductType {
	case 0:
		handleAfdianSubscription(order, username, matched)
	case 1:
		handleAfdianShopOrder(order, username, matched)
	default:
		log.Printf("[afdian] unknown product_type: %d (out_trade_no=%s)", order.ProductType, order.OutTradeNo)
	}

	// 7. 更新幂等记录（填充完整信息，同步落盘）
	updateAfdianOrder(AfdianProcessedOrder{
		OutTradeNo:  order.OutTradeNo,
		Type:        payload.Data.Type,
		ProductType: order.ProductType,
		Username:    username,
		Amount:      order.TotalAmount,
		ProcessedAt: time.Now().UnixMilli(),
	})

	afdianOK(c)
}

// handleAfdianSubscription 处理赞助方案（订阅）订单
func handleAfdianSubscription(order AfdianOrder, username string, matched User) {
	tier, ok := afdianTierForPlan(order.PlanId)
	if !ok {
		log.Printf("[afdian] unknown plan_id: %s (out_trade_no=%s)", order.PlanId, order.OutTradeNo)
		sendDiscordWebhook([]map[string]any{{
			"title": "爱发电订单 - 未知方案",
			"description": fmt.Sprintf("**订单号:** %s\n**plan_id:** %s\n**金额:** %s 元\n**custom_order_id:** %s\n\n请在 afdian_plans.json 中配置此 plan_id 的 tier 映射",
				order.OutTradeNo, order.PlanId, order.TotalAmount, order.CustomOrderId),
			"timestamp": time.Now().Format(time.RFC3339),
		}})
		return
	}

	// 续期计算：month 字段表示购买月数
	months := order.Month
	if months <= 0 {
		months = 1
	}
	daysPerMonth := afdianDaysPerMonth()
	durationMs := int64(months*daysPerMonth) * 24 * 60 * 60 * 1000

	if matched != nil {
		afdianSubUpdateMutex.Lock()
		currentSub := matched.GetSubscription()
		var nextBilling int64
		now := time.Now().UnixMilli()
		if currentSub.Active && currentSub.Next_billing > now && currentSub.Tier == tier {
			// 同 tier 续期，在当前到期日基础上累加
			nextBilling = currentSub.Next_billing + durationMs
		} else {
			// 新购或升降级，从现在开始计期
			nextBilling = now + durationMs
		}
		applySubscriptionToUser(matched, tier, nextBilling)
		afdianSubUpdateMutex.Unlock()
	}

	matchedStr := "未匹配"
	if username != "" {
		matchedStr = username
	}
	sendDiscordWebhook([]map[string]any{{
		"title": "爱发电 - 新订阅",
		"description": fmt.Sprintf("**用户:** %s\n**方案:** %s (Tier: %s)\n**月数:** %d\n**金额:** %s 元\n**订单号:** %s\n**匹配状态:** %s",
			order.CustomOrderId, order.PlanId, tier, order.Month, order.TotalAmount, order.OutTradeNo, matchedStr),
		"timestamp": time.Now().Format(time.RFC3339),
	}})
}

// handleAfdianShopOrder 处理商品（积分包）订单
func handleAfdianShopOrder(order AfdianOrder, username string, matched User) {
	for _, sku := range order.SkuDetail {
		credits, ok := afdianCreditsForSku(sku.SkuId)
		if !ok {
			log.Printf("[afdian] unknown sku_id: %s (out_trade_no=%s)", sku.SkuId, order.OutTradeNo)
			continue
		}
		count := sku.Count
		if count <= 0 {
			count = 1
		}
		totalCredits := credits * count
		if matched != nil {
			addAfdianCredits(matched, totalCredits, order.OutTradeNo)
		}
	}

	matchedStr := "未匹配"
	if username != "" {
		matchedStr = username
	}
	sendDiscordWebhook([]map[string]any{{
		"title": "爱发电 - 商品订单",
		"description": fmt.Sprintf("**用户:** %s\n**金额:** %s 元\n**订单号:** %s\n**匹配状态:** %s",
			order.CustomOrderId, order.TotalAmount, order.OutTradeNo, matchedStr),
		"timestamp": time.Now().Format(time.RFC3339),
	}})
}

// addAfdianCredits 发放积分（源账户为 rotur，与原 Ko-fi 逻辑一致）
func addAfdianCredits(user User, credits int, outTradeNo string) {
	now := time.Now().UnixMilli()
	balance := float64(user.GetCredits()) + float64(credits)
	user.applyTransaction(balance, Transaction{
		Note:      fmt.Sprintf("爱发电积分购买 (%s)", outTradeNo),
		User:      getIdByUsername(Username("rotur")),
		Timestamp: now,
		Amount:    float64(credits),
		Type:      "transfer",
	})
	go saveUsers()
}

func afdianOK(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"ec": 200, "em": ""})
}
