// Package backup implements a content-addressed, deduplicating backup engine.
//
// The model is: a running workload's filesystem is scanned and split into
// content-defined chunks (see internal/backup/cdc); each unique chunk is
// compressed, authenticated-encrypted, and stored once in a local
// content-addressed repository built from immutable pack files; a versioned,
// signed manifest records the ordered chunk references for every file plus a
// workload Blueprint. Restore points share unchanged chunks, so a second
// backup costs roughly the changed data plus a metadata scan.
//
// Local capture and remote upload are separate stages: capture writes only to
// the local repository and enqueues pack objects for a bounded, restart-
// recoverable upload queue. A slow remote transfer never blocks capturing the
// next workload.
//
// This package deliberately performs no guest interruption: it reads a live
// directory tree. There is no freeze, pause, stop, or restart anywhere in the
// capture path. Consistency is crash-consistent unless an application-aware
// pre-hook is used by the caller.
package backup

import (
	"encoding/hex"
	"fmt"

	"github.com/no-dal/ndl-ce/internal/backup/cdc"
)

// Repository format identifiers. A reader must reject data it does not
// understand. Bumping these is a format change.
const (
	Format         = "ndl-cab" // content-addressed backup
	RepoVersion    = 1
	ManifestKind   = "ndl-backup-manifest"
	BlueprintKind  = "ndl-backup-blueprint"
	ChunkAlgorithm = cdc.Algorithm
)

// String renders a chunk id as lowercase hex.
func (id KeyID) String() string { return hex.EncodeToString(id[:]) }

// ParseKeyID parses a lowercase-hex chunk id.
func ParseKeyID(s string) (KeyID, error) {
	var id KeyID
	b, err := hex.DecodeString(s)
	if err != nil {
		return id, err
	}
	if len(b) != len(id) {
		return id, fmt.Errorf("chunk id must be %d bytes", len(id))
	}
	copy(id[:], b)
	return id, nil
}

// MarshalText / UnmarshalText let chunk ids appear as hex strings in JSON.
func (id KeyID) MarshalText() ([]byte, error) { return []byte(id.String()), nil }

func (id *KeyID) UnmarshalText(text []byte) error {
	parsed, err := ParseKeyID(string(text))
	if err != nil {
		return err
	}
	*id = parsed
	return nil
}
