//go:build !linux

package backup

import "math"

// hostFreeBytes reports effectively unlimited space on platforms without a
// portable statfs, so the reserve check is a no-op there.
func hostFreeBytes(string) (int64, error) {
	return math.MaxInt64, nil
}
