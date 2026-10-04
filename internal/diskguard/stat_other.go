//go:build !linux

package diskguard

import (
	"errors"
	"os"
)

func statPath(string) (statInfo, error) {
	return statInfo{}, errors.New("disk statistics are only read on Linux")
}

func preallocate(string, int64) error { return errReserveUnsupported }

func deviceOf(os.FileInfo) uint64 { return 0 }

func allocatedBytes(fi os.FileInfo) int64 { return fi.Size() }
