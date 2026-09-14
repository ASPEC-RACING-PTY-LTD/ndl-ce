package mfa

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"strings"
	"time"
)

const (
	digits      = 6
	period      = 30
	secretLen   = 20
	window      = 1
	recoveryN   = 8
	recoveryLen = 10
)

// GenerateSecret returns a base32 TOTP secret without padding.
func GenerateSecret() (string, error) {
	b := make([]byte, secretLen)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return strings.TrimRight(base32.StdEncoding.EncodeToString(b), "="), nil
}

// OTPAuthURL is the enrollment URI. It is not a password.
func OTPAuthURL(issuer, username, secret string) string {
	return fmt.Sprintf("otpauth://totp/%s:%s?secret=%s&issuer=%s&period=%d&digits=%d",
		issuer, username, secret, issuer, period, digits)
}

// Verify reports whether code is valid for secret at now.
func Verify(secret, code string, now time.Time) bool {
	code = strings.TrimSpace(code)
	if len(code) != digits {
		return false
	}
	for _, delta := range []int64{-window, 0, window} {
		if totp(secret, now.Add(time.Duration(delta)*period*time.Second)) == code {
			return true
		}
	}
	return false
}

// Code returns the 6-digit TOTP at t. Tests and enroll confirm use the same clock.
func Code(secret string, t time.Time) string {
	return totp(secret, t)
}

func totp(secret string, t time.Time) string {
	key, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(strings.ToUpper(secret))
	if err != nil {
		return ""
	}
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], uint64(t.Unix()/period))
	mac := hmac.New(sha1.New, key)
	_, _ = mac.Write(buf[:])
	sum := mac.Sum(nil)
	off := sum[len(sum)-1] & 0x0f
	bin := binary.BigEndian.Uint32(sum[off:off+4]) & 0x7fffffff
	mod := uint32(1)
	for i := 0; i < digits; i++ {
		mod *= 10
	}
	return fmt.Sprintf("%0*d", digits, bin%mod)
}

// RecoveryCodes returns plaintext codes and their SHA-256 hashes.
func RecoveryCodes() (plain []string, hashes []string, err error) {
	plain = make([]string, recoveryN)
	hashes = make([]string, recoveryN)
	for i := 0; i < recoveryN; i++ {
		b := make([]byte, recoveryLen)
		if _, err = rand.Read(b); err != nil {
			return nil, nil, err
		}
		p := strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(b)[:recoveryLen])
		plain[i] = p
		sum := sha256.Sum256([]byte(p))
		hashes[i] = fmt.Sprintf("%x", sum[:])
	}
	return plain, hashes, nil
}

// HashRecovery hashes a recovery code the same way enroll does.
func HashRecovery(code string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(strings.ToLower(code))))
	return fmt.Sprintf("%x", sum[:])
}
