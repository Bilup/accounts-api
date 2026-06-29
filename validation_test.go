package main

import "testing"

func TestValidateUsernamePunctuationRules(t *testing.T) {
	validUsernames := []Username{
		"test.name",
		"test-name",
		"test.name-1",
		"test-name.",
	}
	for _, username := range validUsernames {
		ok, msg := ValidateUsername(username)
		if !ok {
			t.Errorf("Username %q should be valid, got: %s", username, msg)
		}
	}

	invalidUsernames := []Username{
		".testname",
		"-username",
		"username-",
	}
	for _, username := range invalidUsernames {
		ok, _ := ValidateUsername(username)
		if ok {
			t.Errorf("Username %q should be invalid", username)
		}
	}
}
