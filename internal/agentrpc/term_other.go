//go:build !linux

package agentrpc

import (
	"context"
	"fmt"
)

func startPTYSession(_ context.Context, _ termRequest) (termSession, error) {
	return newClosedTerm(fmt.Errorf("PTY terminals are implemented on Linux hosts")), nil
}
