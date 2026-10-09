//go:build !linux

package storage

// SameFilesystem cannot tell filesystems apart here and reports false.
func SameFilesystem(a, b string) (bool, error) { return false, nil }
