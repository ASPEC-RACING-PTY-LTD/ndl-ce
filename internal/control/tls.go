package control

import (
	"crypto/tls"
	"fmt"
	"log"
	"net"
	"net/http"
	"strings"

	"github.com/no-dal/ndl-ce/internal/ndltls"
)

func redirectHandler(httpsHost string, acme http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if acme != nil && strings.HasPrefix(r.URL.Path, "/.well-known/acme-challenge/") {
			acme.ServeHTTP(w, r)
			return
		}
		host, ok := safeRedirectHost(httpsHost, r.Host)
		if !ok {
			http.Error(w, "invalid host", http.StatusBadRequest)
			return
		}
		target := "https://" + host + r.URL.RequestURI()
		http.Redirect(w, r, target, http.StatusMovedPermanently)
	})
}

func serveTLS(addr string, cert tls.Certificate, handler http.Handler) error {
	srv := &http.Server{
		Addr:    addr,
		Handler: handler,
		TLSConfig: &tls.Config{
			MinVersion:   tls.VersionTLS12,
			Certificates: []tls.Certificate{cert},
		},
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("tls listen %s: %w", addr, err)
	}
	log.Printf("ndl-control listening on https %s", addr)
	return srv.ServeTLS(ln, "", "")
}

func loadEnabledMaterial(dir string) (ndltls.Material, error) {
	return (ndltls.Dir{Root: dir}).Load()
}

func safeRedirectHost(httpsHost, reqHost string) (string, bool) {
	host := strings.TrimSpace(httpsHost)
	if host == "" {
		h, _, err := net.SplitHostPort(reqHost)
		if err != nil {
			h = reqHost
		}
		host = strings.TrimSpace(h)
	}
	if host == "" || strings.ContainsAny(host, "/\\@\n\r\t") || strings.Contains(host, "://") {
		return "", false
	}
	return host, true
}
