package agentrpc

import (
	"context"
	"os"
	"path/filepath"
	"strings"
)

type destTCPCtxKey struct{}

const defaultDestTCPListen = ":9444"

// destTCPListenAddr is set when this process should accept southbound dest-agent
// RPCs over TCP (a joined worker). The local unix socket stays peer-cred only.
func destTCPListenAddr() string {
	raw := strings.TrimSpace(os.Getenv("NODAL_AGENT_TCP_LISTEN"))
	switch strings.ToLower(raw) {
	case "off", "0", "false":
		return ""
	case "":
		if _, err := os.Stat(filepath.Join(destDataDir(), "node.crt")); err != nil {
			return ""
		}
		return defaultDestTCPListen
	default:
		return raw
	}
}

func destDataDir() string {
	if dir := strings.TrimSpace(os.Getenv("NODAL_DATA_DIR")); dir != "" {
		return dir
	}
	return "/var/lib/ndl"
}

func withDestTCP(ctx context.Context) context.Context {
	return context.WithValue(ctx, destTCPCtxKey{}, true)
}

func destTCPAuthorized(ctx context.Context) bool {
	ok, _ := ctx.Value(destTCPCtxKey{}).(bool)
	return ok
}
