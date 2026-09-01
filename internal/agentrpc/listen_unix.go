//go:build unix

package agentrpc

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/user"
	"strconv"

	"github.com/no-dal/ndl-ce/gen/nodal/agent/v1/agentv1connect"
	"github.com/no-dal/ndl-ce/internal/peercred"
	"github.com/no-dal/ndl-ce/internal/transport"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
)

type ctxKey struct{}

// Serve listens on the systemd socket when present.
func Serve(h *Handler) error {
	if h.AllowedUID == 0 {
		if u, err := user.Lookup("ndl-control"); err == nil {
			if id, err := strconv.ParseUint(u.Uid, 10, 32); err == nil {
				h.AllowedUID = uint32(id)
			}
		}
	}
	if h.AllowedUID == 0 {
		return fmt.Errorf("ndl-control user is required for peer credential checks")
	}
	h.Peer = func(ctx context.Context) (peercred.Creds, error) {
		c, ok := ctx.Value(ctxKey{}).(peercred.Creds)
		if !ok {
			return peercred.Creds{}, errUnauthorized
		}
		return c, nil
	}
	ln, err := listenAgent()
	if err != nil {
		return err
	}
	mux := http.NewServeMux()
	mux.Handle(agentv1connect.NewAgentServiceHandler(h))
	srv := &http.Server{
		Handler: h2c.NewHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if conn, ok := r.Context().Value(connKey{}).(net.Conn); ok {
				if cred, err := peercred.FromConn(conn); err == nil {
					r = r.WithContext(context.WithValue(r.Context(), ctxKey{}, cred))
				}
			}
			mux.ServeHTTP(w, r)
		}), &http2.Server{}),
		ConnContext: func(ctx context.Context, c net.Conn) context.Context {
			return context.WithValue(ctx, connKey{}, c)
		},
	}
	return srv.Serve(ln)
}

func listenAgent() (net.Listener, error) {
	if os.Getenv("LISTEN_FDS") == "1" {
		return listenSystemd()
	}
	path := transport.AgentSocket
	if extra := os.Getenv("NODAL_AGENT_SOCKET"); extra != "" {
		path = extra
	}
	return net.Listen("unix", path)
}

type connKey struct{}

func listenSystemd() (net.Listener, error) {
	return net.FileListener(os.NewFile(3, "agent.sock"))
}

var errUnauthorized = errText("unauthorized peer")

type errText string

func (e errText) Error() string { return string(e) }
