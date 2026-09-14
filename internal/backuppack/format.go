package backuppack

import "time"

// Manifest is the commit record for one backup pack. Workload identity is the
// UUID, never the human-readable object prefix. Renames do not rewrite history.
type Manifest struct {
	Version       int       `json:"version"`
	Format        string    `json:"format"`
	PayloadKind   string    `json:"payload_kind"`
	ClusterID     string    `json:"cluster_id"`
	WorkloadID    string    `json:"workload_id"`
	WorkloadName  string    `json:"workload_name,omitempty"`
	ArtifactID    string    `json:"artifact_id"`
	RunID         string    `json:"run_id,omitempty"`
	Method        string    `json:"method,omitempty"`
	Consistency   string    `json:"consistency,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
	Encrypted     bool      `json:"encrypted"`
	PayloadSHA256 string    `json:"payload_sha256"`
	PayloadSize   int64     `json:"payload_size"`
	ChunkSize     int       `json:"chunk_size"`
	Chunks        []Chunk   `json:"chunks"`
	ConfigSHA256  string    `json:"config_sha256,omitempty"`
	ObjectPrefix  string    `json:"object_prefix"`
	PlanJSON      string    `json:"plan_json,omitempty"`
}

// Chunk is one indexed payload piece. Dedup later hashes PlainSHA256.
type Chunk struct {
	Index        int    `json:"index"`
	Name         string `json:"name"`
	PlainSHA256  string `json:"plain_sha256"`
	CipherSHA256 string `json:"cipher_sha256"`
	PlainSize    int64  `json:"plain_size"`
	CipherSize   int64  `json:"cipher_size"`
}

// Checksums is a sidecar copy of payload and chunk hashes for verify.
type Checksums struct {
	PayloadSHA256 string  `json:"payload_sha256"`
	PayloadSize   int64   `json:"payload_size"`
	Chunks        []Chunk `json:"chunks"`
}
