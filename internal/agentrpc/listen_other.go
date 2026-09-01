//go:build !unix

package agentrpc

import "fmt"

// Serve is unavailable on this OS.
func Serve(*Handler) error {
	return fmt.Errorf("ndl-agent requires a unix host")
}
