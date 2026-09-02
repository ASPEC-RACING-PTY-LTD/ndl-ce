package control

import (
	"crypto/tls"
	"crypto/x509"
	"net"
	"net/http"
	"os"
	"strings"

	"github.com/no-dal/ndl-ce/internal/cluster"
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

func clusterCADir() string {
	if v := strings.TrimSpace(os.Getenv("NODAL_CLUSTER_CA_DIR")); v != "" {
		return v
	}
	return "/var/lib/ndl/secrets/cluster-ca"
}

func tlsServerConfig(cert tls.Certificate, caDir string) *tls.Config {
	cfg := &tls.Config{
		MinVersion:   tls.VersionTLS12,
		Certificates: []tls.Certificate{cert},
	}
	ca := cluster.CA{Dir: caDir}
	pool, err := ca.ClientPool()
	if err != nil || pool == nil {
		return cfg
	}
	cfg.ClientCAs = pool
	cfg.ClientAuth = tls.VerifyClientCertIfGiven
	cfg.VerifyPeerCertificate = func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
		return ca.VerifyClientCerts(rawCerts)
	}
	return cfg
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
