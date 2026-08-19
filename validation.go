package main

import (
	"claw/internal/config"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"claw/internal/pwhash"
)

var (
	usernameAllowedRe = regexp.MustCompile("^[a-z0-9_][a-z0-9_.-]*[a-z0-9_.]$")
	bannedWordsOnce   sync.Once
	bannedWords       []string
	bannedWordsErr    error
	// bannedRules 是敏感词列表编译后的匹配规则，见 buildBannedRules。
	bannedRules []*regexp.Regexp
)

var (
	whitelistWords     map[string]struct{}
	whitelistWordsOnce sync.Once
)

func loadWhitelist() map[string]struct{} {
	whitelistWordsOnce.Do(func() {
		whitelistWords = make(map[string]struct{})
		// 内置硬编码白名单：已知的误伤词（常见英文单词恰好包含违禁词子串）
		builtin := []string{
			"class", "pass", "grass", "passion", "massage",
			"assistant", "assemble", "assurance",
			"sussex", "sextant", "sextoy",
			"dogood", "godzilla", "goddess",
		}
		for _, w := range builtin {
			whitelistWords[w] = struct{}{}
		}
		// 从文件加载额外白名单
		if data, err := os.ReadFile("./whitelist_words.json"); err == nil {
			var words []string
			if json.Unmarshal(data, &words) == nil {
				for _, w := range words {
					w = strings.ToLower(strings.TrimSpace(w))
					if w != "" {
						whitelistWords[w] = struct{}{}
					}
				}
			}
		}
		// 从环境变量加载（逗号分隔）
		if env := os.Getenv("WHITELIST_WORDS"); env != "" {
			for _, w := range strings.Split(env, ",") {
				w = strings.ToLower(strings.TrimSpace(w))
				if w != "" {
					whitelistWords[w] = struct{}{}
				}
			}
		}
	})
	return whitelistWords
}

// minBannedWordLen 是最小违禁词长度，短于此值的词不生成匹配规则。
const minBannedWordLen = 2

// isAsciiAlphaNumWithSymbols 报告 s 是否仅由 ASCII 字母、数字和常见符号组成。
// 用于判断含数字/符号的违禁词是否可尝试 leet 还原后做整词匹配。
func isAsciiAlphaNumWithSymbols(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') && !strings.ContainsRune("!@#$%^&*+-=._", r) {
			return false
		}
	}
	return true
}

// buildWordBoundaryPattern 构建整词边界正则。
// strict=true 时要求前后都必须有显式分隔符（不允许仅靠行首/行尾），
// 用于降低短词误伤风险。
func buildWordBoundaryPattern(word string, strict bool) string {
	if strict {
		return "[^a-z0-9]" + regexp.QuoteMeta(word) + "[^a-z0-9]"
	}
	return "(?:^|[^a-z0-9])" + regexp.QuoteMeta(word) + "(?:[^a-z0-9]|$)"
}

func loadBannedWordsLocal() ([]string, error) {
	bannedWordsOnce.Do(func() {
		file, err := os.Open("./banned_words.json")
		if err == nil {
			defer file.Close()
			var words []string
			if err := json.NewDecoder(file).Decode(&words); err == nil {
				bannedWords = words
			}
		}

		if config.BANNED_WORDS_URL != "" {
			client := &http.Client{Timeout: 5 * time.Second}
			resp, err := client.Get(config.BANNED_WORDS_URL)
			if err == nil {
				defer resp.Body.Close()
				if resp.StatusCode == 200 {
					body, err := io.ReadAll(resp.Body)
					if err == nil {
						for _, word := range strings.Split(string(body), "\n") {
							word = strings.TrimSpace(word)
							if word != "" && !slices.Contains(bannedWords, word) {
								bannedWords = append(bannedWords, word)
							}
						}
					}
				}
			}
		}

		bannedRules = buildBannedRules(bannedWords)

		if len(bannedWords) == 0 && bannedWordsErr == nil {
			bannedWordsErr = fmt.Errorf("no banned words loaded")
		}
	})
	return bannedWords, bannedWordsErr
}

func ValidateUsername(username Username) (bool, string) {
	usernameLower := string(username.ToLower())
	if usernameLower == "" {
		return false, "Username is required"
	}
	if len(usernameLower) < 3 || len(usernameLower) > 20 {
		return false, "Username must be between 3 and 20 characters"
	}
	if !usernameAllowedRe.MatchString(usernameLower) {
		return false, "Username contains invalid characters"
	}
	// 敏感词仅在名单成功加载后才生效；加载失败（名单缺失等）时跳过检查，
	// 避免因名单问题导致所有用户名无法注册。
	if _, err := loadBannedWordsLocal(); err != nil {
		return true, ""
	}
	u := normalizeLeet(usernameLower)

	// 白名单检查：若用户名（leetspeak 还原后）命中白名单，直接放行，
	// 避免常见英文单词被违禁词子串误伤。
	if _, ok := loadWhitelist()[u]; ok {
		return true, ""
	}

	for _, rule := range bannedRules {
		if rule.MatchString(u) {
			return false, "Username contains a banned word"
		}
	}
	return true, ""
}

// leetReplacements 定义 leetspeak 字符到字母的映射表。
// 注意：1 优先映射到 i（对抗 sh1t、b1tch 等绕过），
// 而非原 l，因为违禁词以脏话为主，1→i 命中率更高。
var leetReplacements = []struct{ from, to string }{
	{"@", "a"}, {"4", "a"},
	{"8", "b"},
	{"3", "e"},
	{"6", "g"},
	{"!", "i"}, {"1", "i"},
	{"0", "o"},
	{"5", "s"}, {"$", "s"},
	{"7", "t"}, {"+", "t"},
	{"2", "z"},
}

// normalizeLeet 将常见 leetspeak 字符替换还原为字母，用于对抗绕过检测。
// 例如 h3llo -> hello，a55 -> ass，sh!t -> shit。
func normalizeLeet(s string) string {
	for _, r := range leetReplacements {
		s = strings.ReplaceAll(s, r.from, r.to)
	}
	return s
}

// isAsciiAlphaWord 报告 s 是否由小写 ASCII 字母组成且非空。
func isAsciiAlphaWord(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < 'a' || r > 'z' {
			return false
		}
	}
	return true
}

// wildcardToRegexp 将带 * 通配符的敏感词（如 "f*ck"、"*ass*"）转换为正则表达式，
// * 表示匹配任意字符序列。
func wildcardToRegexp(pattern string) string {
	var b strings.Builder
	for _, r := range pattern {
		if r == '*' {
			b.WriteString(".*")
		} else {
			b.WriteString(regexp.QuoteMeta(string(r)))
		}
	}
	return b.String()
}

// buildBannedRules 将敏感词列表编译为匹配规则，避免子串匹配误伤正常用户名。
//
// 匹配策略：
//   - 含通配符 * 的词（如 "f*ck"、"*damn*"）按通配符匹配，可在任意位置命中；
//   - 纯 ASCII 字母词按整词匹配（词的前后必须是分隔符或边界），
//     因此 "class" 不会被 "ass" 拦截；
//   - 含数字/符号但无异域字符的词（如 "sh1t"、"a$$"），先尝试 leet 还原，
//     还原后为纯字母则按整词匹配，否则按带边界的子串匹配；
//   - 其余情况（含非 ASCII 字符）退化为子串匹配。
//   - 长度 < minBannedWordLen 的词不生成规则。
func buildBannedRules(words []string) []*regexp.Regexp {
	rules := make([]*regexp.Regexp, 0, len(words))
	for _, word := range words {
		w := strings.ToLower(strings.TrimSpace(word))
		if w == "" || len(w) < minBannedWordLen {
			continue
		}
		var pat string
		short := len(w) <= 2
		switch {
		case strings.Contains(w, "*"):
			pat = wildcardToRegexp(w)
		case isAsciiAlphaWord(w):
			pat = buildWordBoundaryPattern(w, short)
		case isAsciiAlphaNumWithSymbols(w):
			// 含数字/符号但无异域字符：尝试 leet 还原后按整词匹配
			normalized := normalizeLeet(w)
			if isAsciiAlphaWord(normalized) {
				pat = buildWordBoundaryPattern(normalized, len(normalized) <= 2)
			} else {
				pat = buildWordBoundaryPattern(w, short)
			}
		default:
			// 含非 ASCII 字符，退化为子串匹配（兜底）
			pat = regexp.QuoteMeta(w)
		}
		if re, err := regexp.Compile(pat); err == nil {
			rules = append(rules, re)
		}
	}
	return rules
}

func ValidatePassword(password string) (bool, string) {
	if password == "" {
		return false, "Password is required"
	}
	if len(password) < 8 {
		return false, "Password must be at least 8 characters"
	}
	if len(password) > 128 {
		return false, "Password must be at most 128 characters"
	}
	if pwhash.IsMD5Hex(password) {
		return false, "Password looks like an MD5 hash - please use a real password"
	}
	return true, ""
}

func IsIpInBannedList(ip string) bool {
	ips := strings.SplitSeq(os.Getenv("BANNED_IPS"), ",")
	for ipAddr := range ips {
		ipAddr = strings.TrimSpace(ipAddr)
		if ipAddr == "" {
			continue
		}
		if strings.EqualFold(ipAddr, ip) {
			return true
		}
	}
	return false
}
