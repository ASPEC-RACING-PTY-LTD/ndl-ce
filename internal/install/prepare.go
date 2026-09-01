package install

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/google/uuid"
	"github.com/no-dal/ndl-ce/internal/hostos"
	"github.com/no-dal/ndl-ce/internal/identity"
	"github.com/no-dal/ndl-ce/internal/secutil"
)

const (
	dataDir = "/var/lib/ndl"
	etcDir  = "/etc/ndl"
)

// HostPrepare is the root package helper. It does not start services.
func HostPrepare() error {
	p, err := hostos.Detect()
	if err != nil {
		return err
	}
	if !hostos.IsSupported(p) {
		return hostos.Error{Platform: p}
	}
	ident := identity.Files{Dir: dataDir}
	if _, err := ident.EnsureHostKey(); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(dataDir, "control"), 0750); err != nil {
		return err
	}
	if err := os.MkdirAll(etcDir, 0750); err != nil {
		return err
	}
	hashPath := filepath.Join(etcDir, "setup.token.hash")
	if _, err := os.Stat(hashPath); err == nil {
		return nil
	}
	token, err := secutil.RandomHex(32)
	if err != nil {
		return err
	}
	if err := os.WriteFile(ident.SetupTokenPath(), []byte(token+"\n"), 0600); err != nil {
		return err
	}
	if err := os.WriteFile(hashPath, []byte(secutil.HashSHA256(token)+"\n"), 0640); err != nil {
		return err
	}
	if f, err := os.OpenFile("/dev/console", os.O_WRONLY, 0); err == nil {
		_, _ = fmt.Fprintf(f, "No-dal setup token: %s\n", token)
		_ = f.Close()
	}
	fmt.Println("No-dal host-prepare completed. Setup token written for local claim.")
	return nil
}

// RecoverAdmin resets a local admin password using the host key.
func RecoverAdmin(username, password string) error {
	if username == "" || password == "" {
		return fmt.Errorf("username and password are required")
	}
	if runtime.GOOS == "windows" {
		return fmt.Errorf("recover-admin requires a unix host")
	}
	if euid() != 0 {
		return fmt.Errorf("recover-admin must run as root on the appliance")
	}
	ident := identity.Files{Dir: dataDir}
	if _, err := os.Stat(ident.HostKeyPath()); err != nil {
		return fmt.Errorf("host key missing; this is not a No-dal host")
	}
	hash, err := hashPassword(password)
	if err != nil {
		return err
	}
	sql := fmt.Sprintf(
		`UPDATE users SET password_hash = %s WHERE username = %s;
UPDATE sessions SET revoked_at = now() WHERE user_id IN (SELECT id FROM users WHERE username = %s);
UPDATE api_tokens SET revoked_at = now() WHERE user_id IN (SELECT id FROM users WHERE username = %s) AND revoked_at IS NULL;
INSERT INTO audit_events (id, action, result, detail)
VALUES (%s, 'identity.recover', 'ok', '{"via":"recover-admin"}');`,
		pgQuote(hash), pgQuote(username), pgQuote(username), pgQuote(username), pgQuote(uuid.NewString()),
	)
	cmd := exec.Command("su", "-s", "/bin/sh", "postgres", "-c", "psql -X -v ON_ERROR_STOP=1 -d nodal -f -")
	cmd.Stdin = strings.NewReader(sql)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("recover-admin: %s: %w", strings.TrimSpace(string(out)), err)
	}
	fmt.Println("administrator password reset")
	return nil
}

func pgQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}
