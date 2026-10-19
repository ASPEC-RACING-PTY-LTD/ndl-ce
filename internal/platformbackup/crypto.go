// Package platformbackup keeps an encrypted copy of the NDL-CE platform
// itself (database, configuration, the backup repository key and the index
// of workload restore points) in a backup target, so a new host can recover
// everything with only the target's credentials and a SuperAdmin's password.
//
// The snapshot key never leaves the host in the clear. It is wrapped once
// per SuperAdmin with a key derived from that SuperAdmin's password (Argon2id), and the
// wraps are published as an envelope next to the snapshots. Access to the
// bucket and the ability to decrypt are separate: bucket credentials alone
// reveal only ciphertext and salted wraps.
package platformbackup

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"golang.org/x/crypto/argon2"
)

// KeySize is the snapshot key length.
const KeySize = 32

// KDF are the Argon2id parameters a wrap was made with.
type KDF struct {
	Alg       string `json:"alg"`
	Time      uint32 `json:"time"`
	MemoryKiB uint32 `json:"memory_kib"`
	Threads   uint8  `json:"threads"`
}

// DefaultKDF costs about a quarter of a second and 64 MiB per attempt.
var DefaultKDF = KDF{Alg: "argon2id", Time: 3, MemoryKiB: 64 * 1024, Threads: 4}

// Wrap is the snapshot key encrypted for one SuperAdmin.
type Wrap struct {
	// Owner identifies the SuperAdmin without naming them: see OwnerID.
	Owner     string    `json:"owner"`
	KeyID     string    `json:"key_id"`
	KDF       KDF       `json:"kdf"`
	Salt      []byte    `json:"salt"`
	Nonce     []byte    `json:"nonce"`
	Wrapped   []byte    `json:"wrapped"`
	CreatedAt time.Time `json:"created_at"`
}

// ErrLocked is returned when no wrap opens with the given credentials.
var ErrLocked = errors.New("the username or password does not unlock this backup")

// NewKey returns a random snapshot key.
func NewKey() ([]byte, error) {
	k := make([]byte, KeySize)
	_, err := rand.Read(k)
	return k, err
}

// KeyID names a key without revealing it.
func KeyID(key []byte) string {
	m := hmac.New(sha256.New, key)
	m.Write([]byte("ndl-platform key id"))
	return hex.EncodeToString(m.Sum(nil))[:16]
}

// KeyCheck lets a recovered key be confirmed before anything is decrypted.
func KeyCheck(key []byte) string {
	m := hmac.New(sha256.New, key)
	m.Write([]byte("ndl-platform key check"))
	return hex.EncodeToString(m.Sum(nil))
}

// OwnerID is the stable, non-reversible id of a SuperAdmin's sign-in name.
func OwnerID(username string) string {
	sum := sha256.Sum256([]byte("ndl-owner:" + strings.ToLower(strings.TrimSpace(username))))
	return hex.EncodeToString(sum[:])
}

func kek(password string, salt []byte, kdf KDF) ([]byte, error) {
	if kdf.Alg != "argon2id" || kdf.Time == 0 || kdf.MemoryKiB < 8*1024 || kdf.Threads == 0 {
		return nil, fmt.Errorf("unsupported key derivation %q", kdf.Alg)
	}
	if kdf.MemoryKiB > 4*1024*1024 || kdf.Time > 64 {
		return nil, fmt.Errorf("key derivation parameters are out of range")
	}
	return argon2.IDKey([]byte(password), salt, kdf.Time, kdf.MemoryKiB, kdf.Threads, KeySize), nil
}

func gcm(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

func wrapAAD(owner, keyID string) []byte {
	return []byte("ndl-platform wrap\x00" + owner + "\x00" + keyID)
}

// WrapKey encrypts key for the SuperAdmin signing in as username with password.
func WrapKey(key []byte, username, password string, kdf KDF, now time.Time) (Wrap, error) {
	if len(key) != KeySize {
		return Wrap{}, fmt.Errorf("invalid snapshot key")
	}
	if password == "" {
		return Wrap{}, fmt.Errorf("a password is required")
	}
	w := Wrap{Owner: OwnerID(username), KeyID: KeyID(key), KDF: kdf, Salt: make([]byte, 16), CreatedAt: now.UTC()}
	if _, err := rand.Read(w.Salt); err != nil {
		return Wrap{}, err
	}
	k, err := kek(password, w.Salt, kdf)
	if err != nil {
		return Wrap{}, err
	}
	aead, err := gcm(k)
	if err != nil {
		return Wrap{}, err
	}
	w.Nonce = make([]byte, aead.NonceSize())
	if _, err := rand.Read(w.Nonce); err != nil {
		return Wrap{}, err
	}
	w.Wrapped = aead.Seal(nil, w.Nonce, key, wrapAAD(w.Owner, w.KeyID))
	return w, nil
}

// UnwrapKey opens a wrap with a password.
func UnwrapKey(w Wrap, password string) ([]byte, error) {
	k, err := kek(password, w.Salt, w.KDF)
	if err != nil {
		return nil, err
	}
	aead, err := gcm(k)
	if err != nil {
		return nil, err
	}
	if len(w.Nonce) != aead.NonceSize() {
		return nil, ErrLocked
	}
	key, err := aead.Open(nil, w.Nonce, w.Wrapped, wrapAAD(w.Owner, w.KeyID))
	if err != nil || KeyID(key) != w.KeyID {
		return nil, ErrLocked
	}
	return key, nil
}

// Snapshot blobs: magic, key id, nonce, then AES-256-GCM of the archive with
// the header as additional data.
const blobMagic = "NDLPLAT1"

const blobHeader = len(blobMagic) + 16 + 12

// Seal encrypts a snapshot archive.
func Seal(key, plain []byte) ([]byte, error) {
	aead, err := gcm(key)
	if err != nil {
		return nil, err
	}
	head := make([]byte, 0, blobHeader)
	head = append(head, blobMagic...)
	head = append(head, KeyID(key)...)
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	head = append(head, nonce...)
	return aead.Seal(head, nonce, plain, head), nil
}

// BlobKeyID returns the id of the key a snapshot was sealed with.
func BlobKeyID(blob []byte) (string, error) {
	if len(blob) < blobHeader || string(blob[:len(blobMagic)]) != blobMagic {
		return "", fmt.Errorf("not an NDL-CE platform snapshot")
	}
	return string(blob[len(blobMagic) : len(blobMagic)+16]), nil
}

// Open decrypts a snapshot archive.
func Open(key, blob []byte) ([]byte, error) {
	id, err := BlobKeyID(blob)
	if err != nil {
		return nil, err
	}
	if id != KeyID(key) {
		return nil, fmt.Errorf("the snapshot was sealed with another key (%s)", id)
	}
	aead, err := gcm(key)
	if err != nil {
		return nil, err
	}
	head := blob[:blobHeader]
	plain, err := aead.Open(nil, head[len(blobMagic)+16:], blob[blobHeader:], head)
	if err != nil {
		return nil, fmt.Errorf("the snapshot failed authentication")
	}
	return plain, nil
}
