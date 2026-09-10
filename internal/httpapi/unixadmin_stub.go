//go:build !unix

package httpapi

import "net/http"

func (s *Server) unixRootPrincipal(*http.Request) (*principal, bool, error) {
	return nil, false, nil
}
