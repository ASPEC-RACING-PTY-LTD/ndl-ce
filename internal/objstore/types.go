package objstore

import "time"

// Object-storage backup providers. One destination class; kind selects defaults.
const (
	KindS3    = "s3"
	KindR2    = "r2"
	KindAWS   = "aws"
	KindB2    = "b2"
	KindMinIO = "minio"
)

const (
	ActionPut  = "put"
	ActionGet  = "get"
	ActionHead = "head"
	ActionDel  = "del"
)

const (
	Magic      = "NDLE"
	Version    = byte(1)
	NonceSize  = 12
	KeySize    = 32
	PartSize   = 8 << 20
	HeaderSize = 4 + 1 + NonceSize
)

// DefaultRegion returns the SigV4 region for a provider.
func DefaultRegion(kind string) string {
	switch kind {
	case KindR2:
		return "auto"
	case KindAWS:
		return "us-east-1"
	case KindB2:
		return "us-west-004"
	default:
		return "us-east-1"
	}
}

// Request is a typed object put/get. Secrets are request-scoped and never last-applied.
type Request struct {
	Action          string
	Provider        string
	Endpoint        string
	Region          string
	Bucket          string
	Key             string
	SourcePath      string
	DestPath        string
	AccessKeyID     string
	SecretAccessKey string
	EncryptionKey   []byte
	NoCheckBucket   bool
	ResumeUploadID  string
}

// Result is observed object transfer state. Transferred is ciphertext bytes.
type Result struct {
	Key              string    `json:"key"`
	PlaintextSHA256  string    `json:"plaintext_sha256,omitempty"`
	PlaintextSize    int64     `json:"plaintext_size,omitempty"`
	TransferredBytes int64     `json:"transferred_bytes,omitempty"`
	Encrypted        bool      `json:"encrypted"`
	UploadID         string    `json:"upload_id,omitempty"`
	Status           string    `json:"status,omitempty"`
	Reason           string    `json:"reason,omitempty"`
	AppliedAt        time.Time `json:"applied_at,omitempty"`
}

// IsObjectKind reports whether kind is an S3-compatible destination.
func IsObjectKind(kind string) bool {
	switch kind {
	case KindS3, KindR2, KindAWS, KindB2, KindMinIO:
		return true
	default:
		return false
	}
}
