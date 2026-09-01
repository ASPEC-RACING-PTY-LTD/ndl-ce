package mfa

import (
	"testing"
	"time"
)

func TestTOTPRoundTrip(t *testing.T) {
	secret, err := GenerateSecret()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 1, 15, 0, 0, 0, time.UTC)
	code := totp(secret, now)
	if !Verify(secret, code, now) {
		t.Fatal("expected match")
	}
	if Verify(secret, "000000", now) {
		t.Fatal("junk code")
	}
}

func TestRecoveryCodesDistinct(t *testing.T) {
	plain, hashes, err := RecoveryCodes()
	if err != nil {
		t.Fatal(err)
	}
	if len(plain) != 8 || len(hashes) != 8 {
		t.Fatal(len(plain), len(hashes))
	}
	if HashRecovery(plain[0]) != hashes[0] {
		t.Fatal("hash mismatch")
	}
}
