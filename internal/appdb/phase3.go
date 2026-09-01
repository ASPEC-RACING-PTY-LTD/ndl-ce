package appdb

import (
	"encoding/json"
	"time"

	"github.com/no-dal/ndl-ce/internal/storage"
)

// StoragePool is desired existence plus observed availability.
// RootPath and Backing are locators, not identity. ID is the UUID.
type StoragePool struct {
	ID               string
	ClusterID        string
	NodeID           string
	Name             string
	BackendType      string
	Status           string
	Reason           string
	RootPath         string
	Backing          json.RawMessage
	Warnings         []string
	WarningText      []string
	Capabilities     json.RawMessage
	UsableBytes      *int64
	AllocatedBytes   *int64
	ProvisionedBytes *int64
	TotalBytes       *int64
	Adopted          bool
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

// Volume is identified by UUID. BackendRef is a locator.
type Volume struct {
	ID             string
	ClusterID      string
	NodeID         string
	PoolID         string
	Class          string
	Kind           string
	Format         string
	SizeBytes      int64
	Status         string
	BackendType    string
	BackendRef     string
	XattrState     string
	AllocatedBytes *int64
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// LibraryItem is identified by UUID. DisplayName is metadata.
type LibraryItem struct {
	ID             string
	ClusterID      string
	NodeID         string
	PoolID         string
	Kind           string
	DisplayName    string
	BackendRef     string
	SizeBytes      int64
	ChecksumSHA256 string
	Status         string
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

func poolHint(p StoragePool) storage.PoolHint {
	var backing storage.BackingIdentity
	if len(p.Backing) > 0 {
		_ = json.Unmarshal(p.Backing, &backing)
	}
	return storage.PoolHint{
		PoolID:      p.ID,
		BackendType: p.BackendType,
		RootPath:    p.RootPath,
		Backing:     backing,
	}
}

func PoolHints(pools []StoragePool) []storage.PoolHint {
	out := make([]storage.PoolHint, 0, len(pools))
	for _, p := range pools {
		out = append(out, poolHint(p))
	}
	return out
}
