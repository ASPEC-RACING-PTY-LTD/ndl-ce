package cluster

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"time"
)

const (
	CACertFile = "ca.crt"
	CAKeyFile  = "ca.key"
)

// CA is the cluster mTLS issuer. The private key stays on disk, never in Postgres.
type CA struct {
	Dir string
}

func (c CA) root() string {
	if c.Dir != "" {
		return c.Dir
	}
	return "/var/lib/ndl/secrets/cluster-ca"
}

func (c CA) certPath() string { return filepath.Join(c.root(), CACertFile) }
func (c CA) keyPath() string  { return filepath.Join(c.root(), CAKeyFile) }

func (c CA) ensureDir() error {
	if err := os.MkdirAll(c.root(), 0o700); err != nil {
		return err
	}
	return os.Chmod(c.root(), 0o700)
}

// Ensure creates an ECDSA P-256 cluster CA when missing.
func (c CA) Ensure(now time.Time) error {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	if _, err := os.Stat(c.certPath()); err == nil {
		if _, err := os.Stat(c.keyPath()); err == nil {
			return nil
		}
	}
	if err := c.ensureDir(); err != nil {
		return err
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return err
	}
	tpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "No-dal cluster CA", Organization: []string{"No-dal"}},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(10 * 365 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLen:            1,
		MaxPathLenZero:        false,
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if err != nil {
		return err
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return err
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	if err := os.WriteFile(c.certPath(), certPEM, 0o640); err != nil {
		return err
	}
	return os.WriteFile(c.keyPath(), keyPEM, 0o600)
}

// CertPEM returns the CA certificate. The private key is never included.
func (c CA) CertPEM() ([]byte, error) {
	b, err := os.ReadFile(c.certPath())
	if err != nil {
		return nil, err
	}
	return b, nil
}

// IssueNode signs a node certificate. The node private key is returned once to the caller.
func (c CA) IssueNode(nodeID string, now time.Time) (certPEM, keyPEM []byte, err error) {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	if err := c.Ensure(now); err != nil {
		return nil, nil, err
	}
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
	nodeKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, nil, err
	}
	cn := nodeID
	if len(cn) > 64 {
		cn = cn[:64]
	}
	tpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: cn, Organization: []string{"No-dal"}},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(825 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, caCert, &nodeKey.PublicKey, caKey)
	if err != nil {
		return nil, nil, err
	}
	keyDER, err := x509.MarshalECPrivateKey(nodeKey)
	if err != nil {
		return nil, nil, err
	}
	certPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM = pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	return certPEM, keyPEM, nil
}
