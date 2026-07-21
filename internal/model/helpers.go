package model

import (
	"encoding/json"
	"math"
)

func RoundVal(val float64) float64 {
	return math.Round(val*100) / 100
}

func GetStringOrEmpty(val any) string {
	return GetStringOrDefault(val, "")
}

func GetStringOrDefault(val any, defaultVal string) string {
	if val == nil {
		return defaultVal
	}
	if s, ok := val.(string); ok {
		return s
	}
	return defaultVal
}

func GetIntOrDefault(val any, defaultVal int) int {
	if val == nil {
		return defaultVal
	}

	switch v := val.(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	}

	return defaultVal
}

func GetInt64OrDefault(val any, defaultVal int64) int64 {
	if val == nil {
		return defaultVal
	}

	switch v := val.(type) {
	case int64:
		return v
	case int:
		return int64(v)
	case float64:
		return int64(v)
	case float32:
		return int64(v)
	case json.Number:
		i, _ := v.Int64()
		return i
	}

	return defaultVal
}

func GetFloatOrDefault(val any, defaultVal float64) float64 {
	if val == nil {
		return defaultVal
	}
	switch val := val.(type) {
	case float64:
		return val
	case float32:
		return float64(val)
	case int:
		return float64(val)
	case int64:
		return float64(val)
	case json.Number:
		f, _ := val.Float64()
		return f
	default:
		return defaultVal
	}
}
