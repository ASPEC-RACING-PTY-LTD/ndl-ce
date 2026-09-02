package control

import (
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/no-dal/ndl-ce/internal/cluster"
)

func TestRedirectPreservesACMEChallenge(t *testing.T) {
	hit := false
	acme := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hit = true
		w.WriteHeader(http.StatusOK)
	})
	h := redirectHandler("nodal.example", acme)
	ts := httptest.NewServer(h)
	defer ts.Close()
	client := &http.Client{
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	res, err := client.Get(ts.URL + "/.well-known/acme-challenge/tok")
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if !hit {
		t.Fatal("acme challenge must not redirect")
	}
	res, err = client.Get(ts.URL + "/setup")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusMovedPermanently {
		t.Fatalf("redirect %d", res.StatusCode)
	}
	loc := res.Header.Get("Location")
	if loc != "https://nodal.example/setup" {
		t.Fatalf("location %s", loc)
	}
}

func TestRedirectRejectsUnsafeHost(t *testing.T) {
	h := redirectHandler("", nil)
	req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1/setup", nil)
	req.Host = "evil.example/phish"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("unsafe host %d", rec.Code)
	}
}

func TestTLSServerConfigRequestsClientCertWhenClusterCAExists(t *testing.T) {
	dir := t.TempDir()
	ca := cluster.CA{Dir: dir}
	if _, _, err := ca.IssueNode("node-a", time.Time{}); err != nil {
		t.Fatal(err)
	}
	cfg := tlsServerConfig(tls.Certificate{}, dir)
	if cfg.ClientAuth != tls.VerifyClientCertIfGiven {
		t.Fatalf("ClientAuth %v", cfg.ClientAuth)
	}
	if cfg.ClientCAs == nil || cfg.VerifyPeerCertificate == nil {
		t.Fatal("cluster CA pool must be loaded for optional mTLS")
	}
	cfgMissing := tlsServerConfig(tls.Certificate{}, t.TempDir())
	if cfgMissing.ClientAuth != tls.NoClientCert {
		t.Fatalf("missing CA must not require client certs %v", cfgMissing.ClientAuth)
	}
}
