package httpapi

import (
	"slices"

	"github.com/no-dal/ndl-ce/internal/appdb"
	"github.com/no-dal/ndl-ce/internal/storage"
)

// poolOnRootFS reports whether a pool shares the host root filesystem.
func poolOnRootFS(p appdb.StoragePool) bool {
	return slices.Contains(p.Warnings, storage.WarnRootFilesystem)
}

func poolUsable(p appdb.StoragePool) bool {
	return p.Status == storage.StatusAvailable || p.Status == storage.StatusWarning
}

// preferredDefaultPool chooses the pool for a new workload when the request
// names none. A pool on its own disk wins over one that shares the host root
// filesystem, and among equals the one with the most usable space wins, so a
// large separate pool is not left empty while workloads fill the disk the
// operating system and PostgreSQL need. An explicit pool_id always wins and
// existing workloads are never moved.
func preferredDefaultPool(pools []appdb.StoragePool) *appdb.StoragePool {
	var best *appdb.StoragePool
	for i := range pools {
		p := pools[i]
		if !poolUsable(p) {
			continue
		}
		if best == nil || betterDefaultPool(p, *best) {
			cp := p
			best = &cp
		}
	}
	return best
}

func betterDefaultPool(a, b appdb.StoragePool) bool {
	if ra, rb := poolOnRootFS(a), poolOnRootFS(b); ra != rb {
		return !ra
	}
	return usableOf(a) > usableOf(b)
}

func usableOf(p appdb.StoragePool) int64 {
	if p.UsableBytes != nil {
		return *p.UsableBytes
	}
	return 0
}
