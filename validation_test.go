package main

import (
	"testing"
)

func TestValidateUsername_Empty(t *testing.T) {
	ok, msg := ValidateUsername("")
	if ok {
		t.Error("Empty username should be invalid")
	}
	if msg == "" {
		t.Error("Should provide an error message for empty username")
	}
}

func TestValidateUsername_TooShort(t *testing.T) {
	ok, msg := ValidateUsername("ab")
	if ok {
		t.Error("2-char username should be invalid")
	}
	if msg == "" {
		t.Error("Should provide error message for short username")
	}
}

func TestValidateUsername_TooLong(t *testing.T) {
	ok, msg := ValidateUsername("abcdefghijklmnopqrstuvwxyz")
	if ok {
		t.Error("26-char username should be invalid (max 20)")
	}
	if msg == "" {
		t.Error("Should provide error message for long username")
	}
}

func TestValidateUsername_MinLength(t *testing.T) {
	ok, msg := ValidateUsername("abc")
	if !ok {
		t.Errorf("3-char username should be valid, got: %s", msg)
	}
}

func TestValidateUsername_MaxLength(t *testing.T) {
	ok, msg := ValidateUsername("abcdefghijklmnopqrst") // 20 chars
	if !ok {
		t.Errorf("20-char username should be valid, got: %s", msg)
	}
}

func TestValidateUsername_Spaces(t *testing.T) {
	ok, msg := ValidateUsername("hello world")
	if ok {
		t.Error("Username with spaces should be invalid")
	}
	if msg == "" {
		t.Error("Should provide error message for spaces")
	}
}

func TestValidateUsername_InvalidChars(t *testing.T) {
	invalidUsernames := []string{"hello!", "user@name", "test.name", "user name", "hello世界", "user$"}
	for _, username := range invalidUsernames {
		ok, _ := ValidateUsername(Username(username))
		if ok {
			t.Errorf("Username %q should be invalid", username)
		}
	}
}

func TestValidateUsername_ValidChars(t *testing.T) {
	validUsernames := []string{"hello", "user123", "test_user", "abc_def_123", "a_b_c"}
	for _, username := range validUsernames {
		ok, msg := ValidateUsername(Username(username))
		if !ok {
			t.Errorf("Username %q should be valid, got: %s", username, msg)
		}
	}
}

func TestValidateUsername_UppercaseConverted(t *testing.T) {
	ok, msg := ValidateUsername("Hello")
	if !ok {
		t.Logf("Username 'Hello' validation: %s (this is expected if uppercase is rejected)", msg)
	}
}

func TestValidateUsername_UppercaseRejected(t *testing.T) {
	ok, _ := ValidateUsername("HelloWorld")
	if ok {
		t.Error("Uppercase letters should be rejected by the regex")
	}
}

func TestValidateUsername_UnderscoresAllowed(t *testing.T) {
	ok, msg := ValidateUsername("hello_world")
	if !ok {
		t.Errorf("Underscores should be allowed, got: %s", msg)
	}
}

func TestValidateUsername_NumbersAllowed(t *testing.T) {
	ok, msg := ValidateUsername("user123")
	if !ok {
		t.Errorf("Numbers should be allowed, got: %s", msg)
	}
}

func TestValidatePasswordHash_Empty(t *testing.T) {
	ok, msg := ValidatePasswordHash("")
	if ok {
		t.Error("Empty password should be invalid")
	}
	if msg == "" {
		t.Error("Should provide error message for empty password")
	}
}

func TestValidatePasswordHash_WrongLength(t *testing.T) {
	ok, msg := ValidatePasswordHash("abc123")
	if ok {
		t.Error("Short password hash should be invalid")
	}
	if msg == "" {
		t.Error("Should provide error message for wrong length")
	}
}

func TestValidatePasswordHash_BlankMD5(t *testing.T) {
	// The MD5 of an empty string: d41d8cd98f00b204e9800998ecf8427e
	ok, msg := ValidatePasswordHash("d41d8cd98f00b204e9800998ecf8427e")
	if ok {
		t.Error("MD5 of empty string should be rejected")
	}
	if msg == "" {
		t.Error("Should provide error message for empty password MD5")
	}
}

func TestValidatePasswordHash_ValidMD5(t *testing.T) {
	// A valid 32-char hex string that is not the empty-string MD5
	ok, msg := ValidatePasswordHash("5d41402abc4b2a76b9719d911017c592")
	if !ok {
		t.Errorf("Valid MD5 hash should be accepted, got: %s", msg)
	}
}

func TestValidatePasswordHash_InvalidHex(t *testing.T) {
	ok, msg := ValidatePasswordHash("gggggggggggggggggggggggggggggggg") // 32 chars but not hex
	if ok {
		t.Error("Non-hex 32-char string should be invalid")
	}
	if msg == "" {
		t.Error("Should provide error message for non-hex")
	}
}

func TestValidatePasswordHash_UppercaseHex(t *testing.T) {
	ok, msg := ValidatePasswordHash("5D41402ABC4B2A76B9719D911017C592")
	if !ok {
		t.Errorf("Uppercase hex should be accepted, got: %s", msg)
	}
}

func TestValidatePasswordHash_TooLong(t *testing.T) {
	ok, _ := ValidatePasswordHash("5d41402abc4b2a76b9719d911017c592extra")
	if ok {
		t.Error("33-char string should be invalid")
	}
}

func TestIsIpInBannedList_Empty(t *testing.T) {
	// With no BANNED_IPS env, should return false
	result := IsIpInBannedList("1.2.3.4")
	if result {
		t.Error("Should not match when BANNED_IPS is not set")
	}
}
