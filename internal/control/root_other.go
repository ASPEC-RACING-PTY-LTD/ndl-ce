//go:build !unix

package control

// RefuseRoot is a no-op on non-unix hosts.
func RefuseRoot() error { return nil }
