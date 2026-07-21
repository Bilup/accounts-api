package pwhash

import (
	"crypto/md5"
	crypto_rand "crypto/rand"
	"crypto/sha256"
	"encoding/hex"

	"golang.org/x/crypto/pbkdf2"
)

func MD5Hex(input string) string {
	h := md5.Sum([]byte(input))
	return hex.EncodeToString(h[:])
}

func IsMD5Hex(s string) bool {
	if len(s) != 32 {
		return false
	}
	for _, c := range s {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return false
		}
	}
	return true
}

func HashPBKDF2(rawPassword string, salt string, iterations int) string {
	derived := pbkdf2.Key([]byte(rawPassword), []byte(salt), iterations, 32, sha256.New)
	return hex.EncodeToString(derived)
}

func GenerateSalt() string {
	b := make([]byte, 32)
	if _, err := crypto_rand.Read(b); err != nil {
		panic("failed to generate salt: " + err.Error())
	}
	return hex.EncodeToString(b)
}
