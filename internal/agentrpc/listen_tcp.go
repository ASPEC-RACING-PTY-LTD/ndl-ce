//go:build unix

package agentrpc

import (
	"context"
	"crypto/tls"
	"net"
	"net/http"
	"os"
	"path/filepath"

	"github.com/no-dal/ndl-ce/gen/nodal/agent/v1/agentv1connect"
	"github.com/no-dal/ndl-ce/internal/cluster"
	"golang.org/x/net/http2"
)

// serveDestTCP serves the worker agent over mutual TLS only. The node
// certificate from the join is the server identity and only the control
// plane's cluster certificate may call, so the port can face the internet.
func serveDestTCP(h *Handler, addr string) error {
	cfg, err := destTCPTLS()
	if err != nil {
		return err
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	mux := http.NewServeMux()
	mux.Handle(agentv1connect.NewAgentServiceHandler(h))
	srv := &http.Server{
		Handler:   mux,
		TLSConfig: cfg,
		ConnContext: func(ctx context.Context, _ net.Conn) context.Context {
			return withDestTCP(ctx)
		},
	}
	if err := http2.ConfigureServer(srv, &http2.Server{}); err != nil {
		return err
	}
	return srv.Serve(tls.NewListener(ln, cfg))
}

func destTCPTLS() (*tls.Config, error) {
	dir := destDataDir()
	caPEM, err := os.ReadFile(filepath.Join(dir, "cluster-ca.crt"))
	if err != nil {
		return nil, err
	}
	certPEM, err := os.ReadFile(filepath.Join(dir, "node.crt"))
	if err != nil {
		return nil, err
	}
	keyPEM, err := os.ReadFile(filepath.Join(dir, "node.key"))
	if err != nil {
		return nil, err
	}
	return cluster.AgentServerTLS(caPEM, certPEM, keyPEM)
}
