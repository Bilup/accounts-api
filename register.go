package main

import (
	crypto_rand "crypto/rand"
	"strings"
)

func encodeBase64(b []byte) string {
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"
	var out strings.Builder
	out.Grow(((len(b) + 2) / 3) * 4)
	for i := 0; i < len(b); i += 3 {
		remain := len(b) - i
		var n uint32
		n |= uint32(b[i]) << 16
		if remain > 1 {
			n |= uint32(b[i+1]) << 8
		}
		if remain > 2 {
			n |= uint32(b[i+2])
		}
		out.WriteByte(alphabet[(n>>18)&63])
		out.WriteByte(alphabet[(n>>12)&63])
		if remain > 1 {
			out.WriteByte(alphabet[(n>>6)&63])
		} else {
			out.WriteByte('=')
		}
		if remain > 2 {
			out.WriteByte(alphabet[n&63])
		} else {
			out.WriteByte('=')
		}
	}
	return out.String()
}

func generateRandomPassword() string {
	const chars = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789!@#$%^&*"
	b := make([]byte, 24)
	_, err := crypto_rand.Read(b)
	if err != nil {
		panic("failed to generate random password")
	}
	result := make([]byte, 24)
	for i := range result {
		result[i] = chars[int(b[i])%len(chars)]
	}
	return string(result)
}
