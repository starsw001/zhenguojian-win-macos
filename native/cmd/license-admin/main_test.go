package main

import (
	"regexp"
	"testing"
)

// TestGeneratedOfflineCredentialsHaveExactRequestedShape 验证「Generated Offline Credentials Have Exact Requested Shape」回归场景。
func TestGeneratedOfflineCredentialsHaveExactRequestedShape(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 20; i++ {
		value, err := newCustomer(1)
		if err != nil {
			t.Fatal(err)
		}
		if !regexp.MustCompile(`^[a-z0-9]{6,12}@[a-z0-9]{2,12}\.com$`).MatchString(value.Username) {
			t.Fatal("username format")
		}
		upper, lower, digits, at, hash := 0, 0, 0, 0, 0
		for _, c := range value.Password {
			switch {
			case c >= 'A' && c <= 'Z':
				upper++
			case c >= 'a' && c <= 'z':
				lower++
			case c >= '0' && c <= '9':
				digits++
			case c == '@':
				at++
			case c == '#':
				hash++
			}
		}
		if len(value.Password) != 8 || upper < 1 || lower < 1 || upper+lower != 3 || digits != 3 || at != 1 || hash != 1 {
			t.Fatal("password format")
		}
		if seen[value.Username] {
			t.Fatal("random username duplicated")
		}
		seen[value.Username] = true
	}
	if _, err := newCustomer(0); err == nil {
		t.Fatal("zero months accepted")
	}
	if _, err := newCustomer(13); err == nil {
		t.Fatal("13 months accepted")
	}
}

// TestGeneratedUsernamesUseRequestedEmailShape 验证「Generated Usernames Use Requested Email Shape」回归场景。
func TestGeneratedUsernamesUseRequestedEmailShape(t *testing.T) {
	pattern := regexp.MustCompile(`^[a-z0-9]{6,12}@[a-z0-9]{2,12}\.com$`)
	for i := 0; i < 200; i++ {
		if !pattern.MatchString(newUsername()) {
			t.Fatal("generated email does not match the requested lengths, alphabet and suffix")
		}
	}
}
