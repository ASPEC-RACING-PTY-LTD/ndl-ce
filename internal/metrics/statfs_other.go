//go:build !linux

package metrics

func statfsAvail(string) (uint64, bool) {
	return 0, false
}
