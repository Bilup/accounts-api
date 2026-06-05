package main

import (
	"math"
	"testing"
)

func TestGenerateToken(t *testing.T) {
	token := generateToken()
	if token == "" {
		t.Fatal("generateToken should not return empty string")
	}
	if len(token) != 32 {
		t.Fatalf("generateToken should return 32 hex chars (16 bytes), got %d", len(token))
	}
}

func TestGenerateTokenUniqueness(t *testing.T) {
	seen := make(map[string]bool)
	for range 500 {
		token := generateToken()
		if seen[token] {
			t.Fatalf("Duplicate token generated: %s", token)
		}
		seen[token] = true
	}
}

func TestMd5Hex(t *testing.T) {
	hex1234 := md5Hex("1234")
	if hex1234 != "81dc9bdb52d04dc20036dbd8313ed055" {
		t.Errorf("md5Hex(\"1234\") = %q, want %q", hex1234, "81dc9bdb52d04dc20036dbd8313ed055")
	}
}

func TestGenerateShortToken(t *testing.T) {
	token := generateShortToken()
	if token == "" {
		t.Fatal("generateShortToken should not return empty string")
	}
	if len(token) != 16 {
		t.Fatalf("generateShortToken should return 16 hex chars (8 bytes), got %d", len(token))
	}
}

func TestGenerateShortTokenUniqueness(t *testing.T) {
	seen := make(map[string]bool)
	for range 500 {
		token := generateShortToken()
		if seen[token] {
			t.Fatalf("Duplicate short token generated: %s", token)
		}
		seen[token] = true
	}
}

func TestRoundVal(t *testing.T) {
	tests := []struct {
		input    float64
		expected float64
	}{
		{1.234, 1.23},
		{1.235, 1.24},
		{1.236, 1.24},
		{0.0, 0.0},
		{-1.235, -1.24},
		{100.001, 100.0},
		{0.005, 0.01},
	}
	for _, tt := range tests {
		got := roundVal(tt.input)
		if got != tt.expected {
			t.Errorf("roundVal(%v) = %v, want %v", tt.input, got, tt.expected)
		}
	}
}

func TestRoundValPrecision(t *testing.T) {
	result := roundVal(1.005)
	if math.Abs(result-1.0) > 0.02 && math.Abs(result-1.01) > 0.02 {
		t.Errorf("roundVal(1.005) = %v, expected approximately 1.00 or 1.01", result)
	}
}

func TestGetStringOrDefault(t *testing.T) {
	if got := getStringOrDefault("hello", "default"); got != "hello" {
		t.Errorf("getStringOrDefault(\"hello\", ...) = %q, want %q", got, "hello")
	}
	if got := getStringOrDefault(nil, "default"); got != "default" {
		t.Errorf("getStringOrDefault(nil, ...) = %q, want %q", got, "default")
	}
	if got := getStringOrDefault(123, "default"); got != "default" {
		t.Errorf("getStringOrDefault(123, ...) = %q, want %q", got, "default")
	}
	if got := getStringOrDefault("", "default"); got != "" {
		t.Errorf("getStringOrDefault(\"\", ...) = %q, want %q", got, "")
	}
}

func TestGetStringOrEmpty(t *testing.T) {
	if got := getStringOrEmpty("hello"); got != "hello" {
		t.Errorf("getStringOrEmpty(\"hello\") = %q, want %q", got, "hello")
	}
	if got := getStringOrEmpty(nil); got != "" {
		t.Errorf("getStringOrEmpty(nil) = %q, want empty", got)
	}
	if got := getStringOrEmpty(42); got != "" {
		t.Errorf("getStringOrEmpty(42) = %q, want empty", got)
	}
}

func TestGetIntOrDefault(t *testing.T) {
	if got := getIntOrDefault(5, 0); got != 5 {
		t.Errorf("getIntOrDefault(5, 0) = %d, want 5", got)
	}
	if got := getIntOrDefault(int64(10), 0); got != 10 {
		t.Errorf("getIntOrDefault(int64(10), 0) = %d, want 10", got)
	}
	if got := getIntOrDefault(float64(3.7), 0); got != 3 {
		t.Errorf("getIntOrDefault(3.7, 0) = %d, want 3", got)
	}
	if got := getIntOrDefault(nil, 42); got != 42 {
		t.Errorf("getIntOrDefault(nil, 42) = %d, want 42", got)
	}
	if got := getIntOrDefault("abc", 7); got != 7 {
		t.Errorf("getIntOrDefault(\"abc\", 7) = %d, want 7", got)
	}
}

func TestGetFloatOrDefault(t *testing.T) {
	if got := getFloatOrDefault(3.14, 0.0); got != 3.14 {
		t.Errorf("getFloatOrDefault(3.14, 0) = %v, want 3.14", got)
	}
	if got := getFloatOrDefault(42, 0.0); got != 42.0 {
		t.Errorf("getFloatOrDefault(42, 0) = %v, want 42.0", got)
	}
	if got := getFloatOrDefault(int64(7), 0.0); got != 7.0 {
		t.Errorf("getFloatOrDefault(int64(7), 0) = %v, want 7.0", got)
	}
	if got := getFloatOrDefault(nil, 1.5); got != 1.5 {
		t.Errorf("getFloatOrDefault(nil, 1.5) = %v, want 1.5", got)
	}
}

func TestHasTierOrHigher(t *testing.T) {
	tests := []struct {
		tier     string
		required string
		expected bool
	}{
		{"free", "lite", false},
		{"free", "pro", false},
		{"free", "pro", false},
		{"free", "max", false},
		{"lite", "lite", true},
		{"lite", "pro", false},
		{"lite", "pro", false},
		{"lite", "max", false},
		{"pro", "lite", true},
		{"pro", "pro", true},
		{"pro", "pro", false},
		{"pro", "max", false},
		{"pro", "lite", true},
		{"pro", "pro", true},
		{"pro", "pro", true},
		{"pro", "max", false},
		{"max", "lite", true},
		{"max", "pro", true},
		{"max", "pro", true},
		{"max", "max", true},
		{"unknown", "free", false},
		{"free", "unknown", false},
	}
	for _, tt := range tests {
		got := hasTierOrHigher(tt.tier, tt.required)
		if got != tt.expected {
			t.Errorf("hasTierOrHigher(%q, %q) = %v, want %v", tt.tier, tt.required, got, tt.expected)
		}
	}
}

func TestHasTierOrHigherCaseInsensitive(t *testing.T) {
	if !hasTierOrHigher("Pro", "pro") {
		t.Error("hasTierOrHigher should be case-insensitive")
	}
	if !hasTierOrHigher("MAX", "drive") {
		t.Error("hasTierOrHigher should handle uppercase input")
	}
}

func TestHasRequiredStanding(t *testing.T) {
	tests := []struct {
		current  StandingLevel
		required StandingLevel
		expected bool
	}{
		{StandingGood, StandingGood, true},
		{StandingGood, StandingWarning, true},
		{StandingGood, StandingSuspended, true},
		{StandingGood, StandingBanned, true},
		{StandingWarning, StandingGood, false},
		{StandingWarning, StandingWarning, true},
		{StandingWarning, StandingSuspended, true},
		{StandingWarning, StandingBanned, true},
		{StandingSuspended, StandingGood, false},
		{StandingSuspended, StandingWarning, false},
		{StandingSuspended, StandingSuspended, true},
		{StandingSuspended, StandingBanned, true},
		{StandingBanned, StandingGood, false},
		{StandingBanned, StandingWarning, false},
		{StandingBanned, StandingSuspended, false},
		{StandingBanned, StandingBanned, true},
	}
	for _, tt := range tests {
		u := User{"sys.standing": string(tt.current)}
		got := u.HasStandingOrHigher(tt.required)
		if got != tt.expected {
			t.Errorf("HasStandingOrHigher(%q, %q) = %v, want %v", tt.current, tt.required, got, tt.expected)
		}
	}
}

func TestClamp(t *testing.T) {
	if got := clamp(5, 1, 10); got != 5 {
		t.Errorf("clamp(5, 1, 10) = %d, want 5", got)
	}
	if got := clamp(-1, 0, 10); got != 0 {
		t.Errorf("clamp(-1, 0, 10) = %d, want 0", got)
	}
	if got := clamp(15, 0, 10); got != 10 {
		t.Errorf("clamp(15, 0, 10) = %d, want 10", got)
	}
	if got := clamp(0, 0, 0); got != 0 {
		t.Errorf("clamp(0, 0, 0) = %d, want 0", got)
	}
	if got := clamp(3, 3, 3); got != 3 {
		t.Errorf("clamp(3, 3, 3) = %d, want 3", got)
	}
}

func TestJSONStringify(t *testing.T) {
	if got := JSONStringify(map[string]string{"key": "value"}); got != `{"key":"value"}` {
		t.Errorf("JSONStringify(map) = %q, want %q", got, `{"key":"value"}`)
	}
	if got := JSONStringify("hello"); got != `"hello"` {
		t.Errorf("JSONStringify(string) = %q, want %q", got, `"hello"`)
	}
	if got := JSONStringify(42); got != `42` {
		t.Errorf("JSONStringify(int) = %q, want %q", got, `42`)
	}
}

func TestIsFromBannedDomain(t *testing.T) {
	if !isFromBannedDomain("https://pornhub.com/video") {
		t.Error("should detect banned domain")
	}
	if !isFromBannedDomain("https://ONLYFANS.com/page") {
		t.Error("should detect banned domain case-insensitively")
	}
	if isFromBannedDomain("https://google.com") {
		t.Error("should not flag non-banned domain")
	}
	if isFromBannedDomain("") {
		t.Error("empty URL should not match")
	}
	if isFromBannedDomain("https://example.com") {
		t.Error("should not flag unrelated domain")
	}
}

func TestGenerateGiftCode(t *testing.T) {
	code := generateGiftCode()
	if code == "" {
		t.Fatal("generateGiftCode should not return empty string")
	}
	if len(code) != 32 {
		t.Errorf("generateGiftCode should return 32 hex chars, got %d", len(code))
	}
}

func TestGenerateGiftCodeUniqueness(t *testing.T) {
	seen := make(map[string]bool)
	for i := 0; i < 200; i++ {
		code := generateGiftCode()
		if seen[code] {
			t.Fatalf("Duplicate gift code generated: %s", code)
		}
		seen[code] = true
	}
}
