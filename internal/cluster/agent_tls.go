package cluster

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	// ControlURI names the control plane in its agent client certificate.
	ControlURI = "spiffe://no-dal/control"
	// NodeURIPrefix starts every node certificate URI.
	NodeURIPrefix = "spiffe://no-dal/node/"

	controlCertFile = "control.crt"
	controlKeyFile  = "control.key"
)

func (c CA) controlCertPath() string { return filepath.Join(c.root(), controlCertFile) }
func (c CA) controlKeyPath() string  { return filepath.Join(c.root(), controlKeyFile) }

// ControlCertificate returns the control plane's client certificate for
// worker agents, issuing it from the cluster CA on first use and again
// when it is within 30 days of expiry.
func (c CA) ControlCertificate(now time.Time) (tls.Certificate, error) {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	if cert, err := tls.LoadX509KeyPair(c.controlCertPath(), c.controlKeyPath()); err == nil {
		if leaf, perr := x509.ParseCertificate(cert.Certificate[0]); perr == nil && now.Add(30*24*time.Hour).Before(leaf.NotAfter) {
			cert.Leaf = leaf
			return cert, nil
		}
	}
	if err := c.Ensure(now); err != nil {
		return tls.Certificate{}, err
	}
	caCert, caKey, err := c.loadSigner()
	if err != nil {
		return tls.Certificate{}, err
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return tls.Certificate{}, err
	}
	uri, _ := url.Parse(ControlURI)
	tpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "No-dal control", Organization: []string{"No-dal"}},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(825 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
		BasicConstraintsValid: true,
		URIs:                  []*url.URL{uri},
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, caCert, &key.PublicKey, caKey)
	if err != nil {
		return tls.Certificate{}, err
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return tls.Certificate{}, err
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	if err := os.WriteFile(c.controlKeyPath(), keyPEM, 0o600); err != nil {
		return tls.Certificate{}, err
	}
	if err := os.WriteFile(c.controlCertPath(), certPEM, 0o640); err != nil {
		return tls.Certificate{}, err
	}
	return tls.X509KeyPair(certPEM, keyPEM)
}

func (c CA) loadSigner() (*x509.Certificate, *ecdsa.PrivateKey, error) {
	caCertPEM, err := os.ReadFile(c.certPath())
	if err != nil {
		return nil, nil, err
	}
	caKeyPEM, err := os.ReadFile(c.keyPath())
	if err != nil {
		return nil, nil, err
	}
	certBlock, _ := pem.Decode(caCertPEM)
	if certBlock == nil {
		return nil, nil, fmt.Errorf("cluster CA certificate is unreadable")
	}
	caCert, err := x509.ParseCertificate(certBlock.Bytes)
	if err != nil {
		return nil, nil, err
	}
	keyBlock, _ := pem.Decode(caKeyPEM)
	if keyBlock == nil {
		return nil, nil, fmt.Errorf("cluster CA key is unreadable")
	}
	caKey, err := x509.ParseECPrivateKey(keyBlock.Bytes)
	if err != nil {
		return nil, nil, err
	}
	return caCert, caKey, nil
}

// AgentClientTLS is the control plane's TLS config for dialing a worker
// agent. It presents the control certificate and accepts only a server
// certificate the cluster CA issued to nodeID, which is not revoked.
func (c CA) AgentClientTLS(nodeID string, now time.Time) (*tls.Config, error) {
	cert, err := c.ControlCertificate(now)
	if err != nil {
		return nil, err
	}
	pool, err := c.ClientPool()
	if err != nil {
		return nil, err
	}
	if pool == nil {
		return nil, fmt.Errorf("cluster CA is unavailable")
	}
	want := NodeURIPrefix + strings.TrimSpace(nodeID)
	return &tls.Config{
		MinVersion:   tls.VersionTLS13,
		Certificates: []tls.Certificate{cert},
		NextProtos:   []string{"h2"},
		// Node certificates carry a URI, not a DNS name, so the chain and
		// the URI are checked below instead of the hostname.
		InsecureSkipVerify: true,
		VerifyPeerCertificate: func(raw [][]byte, _ [][]*x509.Certificate) error {
			leaf, err := verifyChain(raw, pool, x509.ExtKeyUsageServerAuth)
			if err != nil {
				return err
			}
			if c.serialRevoked(leaf.SerialNumber) {
				return errors.New("worker certificate is revoked")
			}
			if strings.TrimSpace(nodeID) != "" && !hasURI(leaf, want) {
				return fmt.Errorf("worker certificate is not for node %s", nodeID)
			}
			return nil
		},
	}, nil
}

// AgentServerTLS is a worker agent's TLS config: it serves the node
// certificate and accepts only the control plane's client certificate.
func AgentServerTLS(caPEM, certPEM, keyPEM []byte) (*tls.Config, error) {
	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return nil, err
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caPEM) {
		return nil, fmt.Errorf("cluster CA certificate is unreadable")
	}
	return &tls.Config{
		MinVersion:   tls.VersionTLS13,
		Certificates: []tls.Certificate{cert},
		NextProtos:   []string{"h2"},
		ClientCAs:    pool,
		ClientAuth:   tls.RequireAndVerifyClientCert,
		VerifyPeerCertificate: func(raw [][]byte, _ [][]*x509.Certificate) error {
			leaf, err := verifyChain(raw, pool, x509.ExtKeyUsageClientAuth)
			if err != nil {
				return err
			}
			if !hasURI(leaf, ControlURI) {
				return errors.New("only the control plane may call this agent")
			}
			return nil
		},
	}, nil
}

func verifyChain(raw [][]byte, pool *x509.CertPool, usage x509.ExtKeyUsage) (*x509.Certificate, error) {
	if len(raw) == 0 {
		return nil, errors.New("no certificate presented")
	}
	leaf, err := x509.ParseCertificate(raw[0])
	if err != nil {
		return nil, err
	}
	inter := x509.NewCertPool()
	for _, r := range raw[1:] {
		if ic, ierr := x509.ParseCertificate(r); ierr == nil {
			inter.AddCert(ic)
		}
	}
	if _, err := leaf.Verify(x509.VerifyOptions{Roots: pool, Intermediates: inter, KeyUsages: []x509.ExtKeyUsage{usage}}); err != nil {
		return nil, err
	}
	return leaf, nil
}

func hasURI(cert *x509.Certificate, want string) bool {
	for _, u := range cert.URIs {
		if u.String() == want {
			return true
		}
	}
	return false
}
