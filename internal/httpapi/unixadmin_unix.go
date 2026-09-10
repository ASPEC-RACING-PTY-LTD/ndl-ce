//go:build unix

package httpapi

import (
	"context"
	"errors"
	"net"
	"net/http"

	"github.com/no-dal/ndl-ce/internal/appdb"
	"github.com/no-dal/ndl-ce/internal/peercred"
	"github.com/no-dal/ndl-ce/internal/rbac"
)

type unixConnKey struct{}
type peerCredKey struct{}

// UnixConnContext stores the accepted connection so SO_PEERCRED can be read.
func UnixConnContext(ctx context.Context, c net.Conn) context.Context {
	return context.WithValue(ctx, unixConnKey{}, c)
}

// WithUnixPeerCreds injects SO_PEERCRED into the request context.
func WithUnixPeerCreds(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if conn, ok := r.Context().Value(unixConnKey{}).(net.Conn); ok {
			if cred, err := peercred.FromConn(conn); err == nil {
				r = r.WithContext(context.WithValue(r.Context(), peerCredKey{}, cred))
			}
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) unixRootPrincipal(r *http.Request) (*principal, bool, error) {
	cred, ok := r.Context().Value(peerCredKey{}).(peercred.Creds)
	if !ok || cred.UID != 0 {
		return nil, false, nil
	}
	p, err := s.localRootPrincipal(r.Context())
	return p, true, err
}

func (s *Server) localRootPrincipal(ctx context.Context) (*principal, error) {
	c, err := s.Store.GetCluster(ctx)
	if err != nil {
		return nil, err
	}
	if c == nil {
		return nil, errors.New("cluster is not initialized")
	}
	return &principal{
		User: appdb.User{
			ID:        LocalRootUserID,
			ClusterID: c.ID,
			Username:  "root",
			Kind:      appdb.UserKindPerson,
		},
		Roles:  []string{rbac.Admin},
		Grants: rbac.New().PermissionsForRole(rbac.Admin),
		SessID: LocalRootUserID,
		AAL:    2,
	}, nil
}
