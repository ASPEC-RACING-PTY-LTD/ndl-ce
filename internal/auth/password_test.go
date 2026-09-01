package auth

import "testing"

func TestHashAndVerify(t *testing.T) {
	h, err := HashPassword("correct-horse")
	if err != nil {
		t.Fatal(err)
	}
	if !VerifyPassword("correct-horse", h) {
		t.Fatal("verify")
	}
	if VerifyPassword("wrong-password", h) {
		t.Fatal("false positive")
	}
}

func TestShortPasswordRejected(t *testing.T) {
	if _, err := HashPassword("short"); err == nil {
		t.Fatal("expected error")
	}
}
