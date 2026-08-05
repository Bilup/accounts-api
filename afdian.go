package main

import (
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"log"
	"sync"
	"time"

	"claw/internal/config"
)

// 爱发电公钥（来源：爱发电官方文档 https://ifdian.net/p/9c65d9cc617011ed81c352540025c377）
const afdianPublicKeyPEM = `-----BEGIN PUBLIC KEY-----
MIIBIjANBgkqhkiG9w0BAQEFAAOCAQ8AMIIBCgKCAQEAwwdaCg1Bt+UKZKs0R54y
lYnuANma49IpgoOwNmk3a0rhg/PQuhUJ0EOZSowIC44l0K3+fqGns3Ygi4AfmEfS
4EKbdk1ahSxu7Zkp2rHMt+R9GarQFQkwSS/5x1dYiHNVMiR8oIXDgjmvxuNes2Cr
8fw9dEF0xNBKdkKgG2qAawcN1nZrdyaKWtPVT9m2Hl0ddOO9thZmVLFOb9NVzgYf
jEgI+KWX6aY19Ka/ghv/L4t1IXmz9pctablN5S0CRWpJW3Cn0k6zSXgjVdKm4uN7
jRlgSRaf/Ind46vMCm3N2sgwxu/g3bnooW+db0iLo13zzuvyn727Q3UDQ0MmZcEW
MQIDAQAB
-----END PUBLIC KEY-----`

var afdianPublicKey *rsa.PublicKey

func initAfdianPublicKey() {
	block, _ := pem.Decode([]byte(afdianPublicKeyPEM))
	if block == nil {
		log.Fatal("[afdian] Failed to parse public key PEM")
	}
	pub, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		log.Fatalf("[afdian] Failed to parse public key: %v", err)
	}
	afdianPublicKey = pub.(*rsa.PublicKey)
	log.Println("[afdian] Public key loaded")
}

// --- 爱发电订单数据结构 ---

type AfdianOrder struct {
	OutTradeNo    string      `json:"out_trade_no"`
	CustomOrderId string      `json:"custom_order_id"`
	UserId        string      `json:"user_id"`
	PlanId        string      `json:"plan_id"`
	Month         int         `json:"month"`
	TotalAmount   string      `json:"total_amount"`
	ShowAmount    string      `json:"show_amount"`
	Status        int         `json:"status"`
	Remark        string      `json:"remark"`
	RedeemId      string      `json:"redeem_id"`
	ProductType   int         `json:"product_type"`
	Discount      string      `json:"discount"`
	SkuDetail     []AfdianSku `json:"sku_detail"`
}

type AfdianSku struct {
	SkuId   string `json:"sku_id"`
	Count   int    `json:"count"`
	Name    string `json:"name"`
	AlbumId string `json:"album_id"`
	Pic     string `json:"pic"`
}

// --- 幂等记录存储（防重试/重放） ---

type AfdianProcessedOrder struct {
	OutTradeNo  string `json:"out_trade_no"`
	Type        string `json:"type"`
	ProductType int    `json:"product_type"`
	Username    string `json:"username"`
	Amount      string `json:"amount"`
	ProcessedAt int64  `json:"processed_at"`
}

var (
	afdianOrders      = map[string]AfdianProcessedOrder{}
	afdianOrdersMutex sync.RWMutex
)

// afdianSubUpdateMutex 保护订阅续期的 read-modify-write 操作，防止跨订单竞态
var afdianSubUpdateMutex sync.Mutex

func loadAfdianOrders() {
	afdianOrdersMutex.Lock()
	afdianOrders = loadJSONOrDefault(config.AFDIAN_ORDERS_FILE, map[string]AfdianProcessedOrder{})
	count := len(afdianOrders)
	// 清理 90 天前的记录
	cutoff := time.Now().Add(-90 * 24 * time.Hour).UnixMilli()
	for k, v := range afdianOrders {
		if v.ProcessedAt < cutoff {
			delete(afdianOrders, k)
		}
	}
	remaining := len(afdianOrders)
	afdianOrdersMutex.Unlock()
	if remaining < count {
		go saveAfdianOrders()
	}
	log.Printf("Loaded %d afdian processed orders (%d expired removed)", remaining, count-remaining)
}

func saveAfdianOrders() {
	afdianOrdersMutex.RLock()
	snapshot := make(map[string]AfdianProcessedOrder, len(afdianOrders))
	for k, v := range afdianOrders {
		snapshot[k] = v
	}
	afdianOrdersMutex.RUnlock()
	saveJsonFile(config.AFDIAN_ORDERS_FILE, snapshot)
}

// reserveAfdianOrder 原子预约订单（防并发重复处理 / TOCTOU 竞态）。
// 返回 true 表示首次见到此订单，false 表示已处理过。
func reserveAfdianOrder(outTradeNo string) bool {
	afdianOrdersMutex.Lock()
	defer afdianOrdersMutex.Unlock()
	if _, ok := afdianOrders[outTradeNo]; ok {
		return false
	}
	afdianOrders[outTradeNo] = AfdianProcessedOrder{
		OutTradeNo:  outTradeNo,
		ProcessedAt: time.Now().UnixMilli(),
	}
	return true
}

// updateAfdianOrder 填充预约记录的完整信息并同步落盘。
func updateAfdianOrder(order AfdianProcessedOrder) {
	afdianOrdersMutex.Lock()
	afdianOrders[order.OutTradeNo] = order
	afdianOrdersMutex.Unlock()
	saveAfdianOrders()
}

// --- Plans 配置（plan_id → Tier，sku_id → 积分数） ---

type AfdianPlansConfig struct {
	Plans        map[string]string `json:"plans"`
	Skus         map[string]int    `json:"skus"`
	DaysPerMonth int               `json:"days_per_month"`
}

var (
	afdianPlans      AfdianPlansConfig
	afdianPlansMutex sync.RWMutex
)

func loadAfdianPlans() {
	plans := loadJSONOrDefault(config.AFDIAN_PLANS_FILE, AfdianPlansConfig{
		Plans:        map[string]string{},
		Skus:         map[string]int{},
		DaysPerMonth: 31,
	})
	if plans.DaysPerMonth <= 0 {
		plans.DaysPerMonth = 31
	}
	if plans.Plans == nil {
		plans.Plans = map[string]string{}
	}
	if plans.Skus == nil {
		plans.Skus = map[string]int{}
	}
	afdianPlansMutex.Lock()
	afdianPlans = plans
	afdianPlansMutex.Unlock()
	log.Printf("Loaded %d afdian plans and %d skus", len(plans.Plans), len(plans.Skus))
}

func watchAfdianPlansFile() {
	watchFile(config.AFDIAN_PLANS_FILE, func() {
		log.Println("Detected change in afdian_plans.json, reloading...")
		loadAfdianPlans()
	})
}

func afdianTierForPlan(planId string) (string, bool) {
	afdianPlansMutex.RLock()
	defer afdianPlansMutex.RUnlock()
	tier, ok := afdianPlans.Plans[planId]
	return tier, ok
}

func afdianCreditsForSku(skuId string) (int, bool) {
	afdianPlansMutex.RLock()
	defer afdianPlansMutex.RUnlock()
	credits, ok := afdianPlans.Skus[skuId]
	return credits, ok
}

func afdianDaysPerMonth() int {
	afdianPlansMutex.RLock()
	defer afdianPlansMutex.RUnlock()
	return afdianPlans.DaysPerMonth
}

// --- 签名验证 ---
// sign = RSA-SHA256(out_trade_no + user_id + plan_id + total_amount)
// rsa.VerifyPKCS1v15 本身非时序敏感，天然抗时序攻击

func verifyAfdianSign(outTradeNo, userId, planId, totalAmount, signBase64 string) bool {
	if afdianPublicKey == nil {
		return false
	}
	signStr := outTradeNo + userId + planId + totalAmount
	hashed := sha256.Sum256([]byte(signStr))
	signBytes, err := base64.StdEncoding.DecodeString(signBase64)
	if err != nil {
		return false
	}
	return rsa.VerifyPKCS1v15(afdianPublicKey, crypto.SHA256, hashed[:], signBytes) == nil
}
