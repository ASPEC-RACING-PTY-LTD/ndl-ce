//go:build !linux

package netns

import "errors"

// Do is only supported on Linux.
func Do(pid int, fn func() error) error {
	return errors.New("network namespaces are only supported on Linux")
}
