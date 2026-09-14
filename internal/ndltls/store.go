package ndltls

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	ModeSelfSigned = "self_signed"
	ModeImported   = "imported"
	ModeACME       = "acme"

	ACMENotConfigured = "not_configured"
	ACMEPending       = "pending"
	ACMEIssued        = "issued"
	ACMEFailed        = "failed"

	CertFile     = "current.crt"
	KeyFile      = "current.key"
	PrevCertFile = "previous.crt"
	PrevKeyFile  = "previous.key"
)

// Dir is on-disk certificate material. Private keys are never stored in PostgreSQL.
type Dir struct {
	Root string
}

func (d Dir) root() string {
	if d.Root != "" {
		return d.Root
	}
	return "/var/lib/ndl/certs"
}

func (d Dir) certPath() string { return filepath.Join(d.root(), CertFile) }
func (d Dir) keyPath() string  { return filepath.Join(d.root(), KeyFile) }

func (d Dir) ensure() error {
	if err := os.MkdirAll(d.root(), 0o700); err != nil {
		return err
	}
	return os.Chmod(d.root(), 0o700)
}

// Material is loaded PEM plus metadata. The private key is not included in JSON APIs.
type Material struct {
	Mode        string
	CommonName  string
	SANs        []string
	Fingerprint string
	NotBefore   time.Time
	NotAfter    time.Time
	CertPath    string
	KeyPath     string
	Certificate tls.Certificate
}

// Generate writes a self-signed ECDSA P-256 certificate. It keeps the last good pair.
func (d Dir) Generate(cn string, sans []string, now time.Time) (Material, error) {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	cn = strings.TrimSpace(cn)
	if cn == "" {
		cn = "nodal"
	}
	if err := d.ensure(); err != nil {
		return Material{}, err
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return Material{}, err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return Material{}, err
	}
	tpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: cn, Organization: []string{"No-dal"}},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(825 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		DNSNames:              dnsNames(cn, sans),
		IPAddresses:           ipSANs(sans),
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if err != nil {
		return Material{}, err
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return Material{}, err
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	return d.writePair(ModeSelfSigned, certPEM, keyPEM)
}

// Import writes operator-supplied PEM. The previous pair is kept if the new pair is invalid.
func (d Dir) Import(certPEM, keyPEM []byte) (Material, error) {
	if err := d.ensure(); err != nil {
		return Material{}, err
	}
	if _, err := tls.X509KeyPair(certPEM, keyPEM); err != nil {
		return Material{}, fmt.Errorf("certificate and key do not match")
	}
	return d.writePair(ModeImported, certPEM, keyPEM)
}

func (d Dir) writePair(mode string, certPEM, keyPEM []byte) (Material, error) {
	if err := d.backupCurrent(); err != nil {
		return Material{}, err
	}
	if err := os.WriteFile(d.certPath(), certPEM, 0o644); err != nil {
		_ = d.restorePrevious()
		return Material{}, err
	}
	if err := os.WriteFile(d.keyPath(), keyPEM, 0o600); err != nil {
		_ = d.restorePrevious()
		return Material{}, err
	}
	if err := os.Chmod(d.keyPath(), 0o600); err != nil {
		_ = d.restorePrevious()
		return Material{}, err
	}
	mat, err := d.Load()
	if err != nil {
		_ = d.restorePrevious()
		return Material{}, err
	}
	mat.Mode = mode
	return mat, nil
}

func (d Dir) backupCurrent() error {
	if _, err := os.Stat(d.certPath()); os.IsNotExist(err) {
		return nil
	}
	if err := copyFile(d.certPath(), filepath.Join(d.root(), PrevCertFile), 0o644); err != nil {
		return err
	}
	if _, err := os.Stat(d.keyPath()); err == nil {
		return copyFile(d.keyPath(), filepath.Join(d.root(), PrevKeyFile), 0o600)
	}
	return nil
}

func (d Dir) restorePrevious() error {
	prevCert := filepath.Join(d.root(), PrevCertFile)
	prevKey := filepath.Join(d.root(), PrevKeyFile)
	if _, err := os.Stat(prevCert); err != nil {
		return err
	}
	if err := copyFile(prevCert, d.certPath(), 0o644); err != nil {
		return err
	}
	if _, err := os.Stat(prevKey); err == nil {
		return copyFile(prevKey, d.keyPath(), 0o600)
	}
	return nil
}

// Load reads the current pair. If current is unreadable, last good is used.
func (d Dir) Load() (Material, error) {
	mat, err := d.loadFiles(d.certPath(), d.keyPath())
	if err == nil {
		return mat, nil
	}
	prev, perr := d.loadFiles(filepath.Join(d.root(), PrevCertFile), filepath.Join(d.root(), PrevKeyFile))
	if perr != nil {
		return Material{}, fmt.Errorf("tls material is unreadable: %w", err)
	}
	return prev, nil
}

func (d Dir) loadFiles(certPath, keyPath string) (Material, error) {
	certPEM, err := os.ReadFile(certPath)
	if err != nil {
		return Material{}, err
	}
	keyPEM, err := os.ReadFile(keyPath)
	if err != nil {
		return Material{}, err
	}
	pair, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return Material{}, err
	}
	if len(pair.Certificate) == 0 {
		return Material{}, fmt.Errorf("certificate is empty")
	}
	parsed, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		return Material{}, err
	}
	sans := append([]string{}, parsed.DNSNames...)
	for _, ip := range parsed.IPAddresses {
		sans = append(sans, ip.String())
	}
	return Material{
		CommonName:  parsed.Subject.CommonName,
		SANs:        sans,
		Fingerprint: Fingerprint(parsed.Raw),
		NotBefore:   parsed.NotBefore.UTC(),
		NotAfter:    parsed.NotAfter.UTC(),
		CertPath:    certPath,
		KeyPath:     keyPath,
		Certificate: pair,
	}, nil
}

func Fingerprint(der []byte) string {
	sum := sha256.Sum256(der)
	return hex.EncodeToString(sum[:])
}

func dnsNames(cn string, sans []string) []string {
	seen := map[string]struct{}{}
	var out []string
	add := func(s string) {
		s = strings.TrimSpace(strings.ToLower(s))
		if s == "" || net.ParseIP(s) != nil {
			return
		}
		if _, ok := seen[s]; ok {
			return
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	add(cn)
	for _, s := range sans {
		add(s)
	}
	if len(out) == 0 {
		out = []string{"localhost"}
	}
	return out
}

func ipSANs(sans []string) []net.IP {
	var out []net.IP
	for _, s := range sans {
		if ip := net.ParseIP(strings.TrimSpace(s)); ip != nil {
			out = append(out, ip)
		}
	}
	if ip := net.ParseIP("127.0.0.1"); ip != nil {
		out = append(out, ip)
	}
	return out
}

func copyFile(src, dst string, mode os.FileMode) error {
	b, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, b, mode)
}
