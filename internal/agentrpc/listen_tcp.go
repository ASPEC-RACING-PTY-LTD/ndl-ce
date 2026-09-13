//go:build unix

package agentrpc

import (
	"context"
	"net"
	"net/http"

	"github.com/no-dal/ndl-ce/gen/nodal/agent/v1/agentv1connect"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
)

func serveDestTCP(h *Handler, addr string) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	mux := http.NewServeMux()
	mux.Handle(agentv1connect.NewAgentServiceHandler(h))
	srv := &http.Server{
		Handler: h2c.NewHandler(mux, &http2.Server{}),
		ConnContext: func(ctx context.Context, _ net.Conn) context.Context {
			return withDestTCP(ctx)
		},
	}
	return srv.Serve(ln)
}
