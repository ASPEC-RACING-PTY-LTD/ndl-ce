//go:build !linux

package hostos

import "errors"

func statFree(string) (uint64, error) { return 0, errors.New("not supported on this platform") }

func packageLockHeld(string) bool { return false }
