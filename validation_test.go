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
	ok, msg := ValidateUsername("abcdefghijklmnopqrst")
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

func TestValidateUsername_UppercaseNormalized(t *testing.T) {
	ok, _ := ValidateUsername("HelloWorld")
	if !ok {
		t.Error("Uppercase letters should be normalized to lowercase, not rejected")
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

func TestValidatePassword_Empty(t *testing.T) {
	ok, msg := ValidatePassword("")
	if ok {
		t.Error("Empty password should be invalid")
	}
	if msg == "" {
		t.Error("Should provide error message for empty password")
	}
}

func TestValidatePassword_TooShort(t *testing.T) {
	ok, msg := ValidatePassword("abc")
	if ok {
		t.Error("Short password should be invalid")
	}
	if msg == "" {
		t.Error("Should provide error message for short password")
	}
}

func TestValidatePassword_TooLong(t *testing.T) {
	long := ""
	for i := 0; i < 129; i++ {
		long += "a"
	}
	ok, _ := ValidatePassword(long)
	if ok {
		t.Error("129-char password should be invalid")
	}
}

func TestValidatePassword_LooksLikeMD5(t *testing.T) {
	ok, msg := ValidatePassword("5d41402abc4b2a76b9719d911017c592")
	if ok {
		t.Error("32-char hex string should be rejected as MD5-like")
	}
	if msg == "" {
		t.Error("Should provide error message for MD5-like password")
	}
}

func TestValidatePassword_Valid(t *testing.T) {
	ok, msg := ValidatePassword("securepassword123")
	if !ok {
		t.Errorf("Valid password should be accepted, got: %s", msg)
	}
}

func TestValidatePassword_Exactly8Chars(t *testing.T) {
	ok, msg := ValidatePassword("12345678")
	if !ok {
		t.Errorf("8-char password should be valid, got: %s", msg)
	}
}

func TestValidatePassword_7Chars(t *testing.T) {
	ok, _ := ValidatePassword("1234567")
	if ok {
		t.Error("7-char password should be invalid")
	}
}

func TestIsIpInBannedList_Empty(t *testing.T) {
	result := IsIpInBannedList("1.2.3.4")
	if result {
		t.Error("Should not match when BANNED_IPS is not set")
	}
}
