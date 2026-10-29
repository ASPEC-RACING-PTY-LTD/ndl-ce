package agentrpc

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"

	"github.com/no-dal/ndl-ce/internal/ndnet"
)

// Worker agents behind NAT can be reached through a Cloudflare tunnel: the
// worker runs a cloudflared connector with a TCP route to its agent port,
// and the control node opens each connection with `cloudflared access tcp`
// over stdin and stdout. Mutual TLS still runs end to end inside it, so
// Cloudflare only ever carries ciphertext.

var cloudflaredPaths = []string{"/usr/bin/cloudflared", "/usr/local/bin/cloudflared"}

// CloudflareAccess returns the Cloudflare Access service token tunnel
// connections present, when the tunnel hostname sits behind Access. The
// control plane sets it; empty values send none.
var CloudflareAccess func() (clientID, clientSecret string)

// errNoCloudflared is returned when the control node has no cloudflared.
var errNoCloudflared = errors.New("cloudflared is not installed on the control node; install it to reach agents through Cloudflare")

func findCloudflaredBin() string {
	for _, p := range cloudflaredPaths {
		if st, err := os.Stat(p); err == nil && !st.IsDir() && st.Mode()&0o111 != 0 {
			return p
		}
	}
	return ""
}

// dialAgent opens the raw connection to a worker agent address.
func dialAgent(ctx context.Context, network, addr string) (net.Conn, error) {
	if host, ok := ndnet.CloudflareAgentHost(addr); ok {
		return dialCloudflare(ctx, host)
	}
	var d net.Dialer
	return d.DialContext(ctx, network, addr)
}

// cfConn is one `cloudflared access tcp` process wired to a socket pair.
type cfConn struct {
	net.Conn
	cmd    *exec.Cmd
	stderr *tailBuffer
	once   sync.Once
}

func (c *cfConn) Close() error {
	err := c.Conn.Close()
	c.once.Do(func() {
		if c.cmd.Process != nil {
			_ = c.cmd.Process.Kill()
		}
		_ = c.cmd.Wait()
	})
	return err
}

// explain adds what cloudflared said to a failed handshake.
func (c *cfConn) explain(err error) error {
	if msg := c.stderr.last(); msg != "" {
		return fmt.Errorf("%w (cloudflared: %s)", err, msg)
	}
	return err
}

func dialCloudflare(ctx context.Context, host string) (net.Conn, error) {
	bin := findCloudflaredBin()
	if bin == "" {
		return nil, errNoCloudflared
	}
	fds, err := syscall.Socketpair(syscall.AF_UNIX, syscall.SOCK_STREAM|syscall.SOCK_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	ours := os.NewFile(uintptr(fds[0]), "cf-access")
	theirs := os.NewFile(uintptr(fds[1]), "cf-access-child")
	defer theirs.Close()
	conn, err := net.FileConn(ours)
	_ = ours.Close()
	if err != nil {
		return nil, err
	}
	stderr := &tailBuffer{}
	cmd := exec.Command(bin, "access", "tcp", "--hostname", host, "--loglevel", "error")
	cmd.Stdin, cmd.Stdout, cmd.Stderr = theirs, theirs, stderr
	// cloudflared keeps Access tokens under HOME; the control plane's own
	// user may have none.
	cmd.Env = append(os.Environ(), "HOME="+os.TempDir())
	if CloudflareAccess != nil {
		// Through the environment, so the secret never shows in ps.
		if id, secret := CloudflareAccess(); id != "" && secret != "" {
			cmd.Env = append(cmd.Env, "TUNNEL_SERVICE_TOKEN_ID="+id, "TUNNEL_SERVICE_TOKEN_SECRET="+secret)
		}
	}
	if err := cmd.Start(); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("start cloudflared: %w", err)
	}
	if err := ctx.Err(); err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		_ = conn.Close()
		return nil, err
	}
	return &cfConn{Conn: conn, cmd: cmd, stderr: stderr}, nil
}

// tailBuffer keeps the last line a process wrote.
type tailBuffer struct {
	mu  sync.Mutex
	buf []byte
}

func (b *tailBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.buf = append(b.buf, p...)
	if len(b.buf) > 4096 {
		b.buf = b.buf[len(b.buf)-4096:]
	}
	return len(p), nil
}

func (b *tailBuffer) last() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	lines := bytes.Split(bytes.TrimSpace(b.buf), []byte("\n"))
	return strings.TrimSpace(string(lines[len(lines)-1]))
}
