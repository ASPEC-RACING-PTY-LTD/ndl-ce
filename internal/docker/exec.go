package docker

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

type execSession struct {
	conn   net.Conn
	id     string
	cli    *unixClient
	done   chan struct{}
	mu     sync.Mutex
	closed bool
}

type ExecSession interface {
	Read([]byte) (int, error)
	Write([]byte) (int, error)
	Resize(rows, cols uint16) error
	CWD() (string, bool)
	Pong() error
	Done() <-chan struct{}
	Close()
}

func (e *Engine) Exec(ctx context.Context, req ExecRequest) (ExecSession, error) {
	sock, err := e.SocketFor(ctx, req.MachineID)
	if err != nil {
		return nil, err
	}
	cli, ok := e.client(sock).(*unixClient)
	if !ok {
		if e.ClientFor != nil {
			if u, ok := e.ClientFor(sock).(*unixClient); ok {
				cli = u
			}
		}
	}
	if cli == nil {
		cli = newUnixClient(sock)
	}
	body, _ := json.Marshal(map[string]any{
		"AttachStdin":  true,
		"AttachStdout": true,
		"AttachStderr": true,
		"Tty":          true,
		"Cmd":          []string{"/bin/sh", "-c", "cd / 2>/dev/null; if [ -x /bin/bash ]; then exec /bin/bash --login; fi; exec /bin/sh -l"},
	})
	var created struct {
		ID string `json:"Id"`
	}
	if err := cli.json(ctx, http.MethodPost, "/containers/"+url.PathEscape(req.ContainerID)+"/exec", body, &created); err != nil {
		return nil, err
	}
	if strings.TrimSpace(created.ID) == "" {
		return nil, fmt.Errorf("docker exec id was not returned")
	}
	conn, err := hijackExecStart(ctx, sock, created.ID)
	if err != nil {
		return nil, err
	}
	sess := &execSession{conn: conn, id: created.ID, cli: cli, done: make(chan struct{})}
	if req.Rows > 0 && req.Cols > 0 {
		_ = sess.Resize(req.Rows, req.Cols)
	}
	return sess, nil
}

func hijackExecStart(ctx context.Context, socket, execID string) (net.Conn, error) {
	d := net.Dialer{Timeout: 8 * time.Second}
	conn, err := d.DialContext(ctx, "unix", socket)
	if err != nil {
		return nil, err
	}
	payload := []byte(`{"Detach":false,"Tty":true}`)
	req := fmt.Sprintf("POST %s/exec/%s/start HTTP/1.1\r\nHost: docker\r\nContent-Type: application/json\r\nConnection: Upgrade\r\nUpgrade: tcp\r\nContent-Length: %d\r\n\r\n%s",
		dockerAPIPrefix, url.PathEscape(execID), len(payload), payload)
	if _, err := conn.Write([]byte(req)); err != nil {
		_ = conn.Close()
		return nil, err
	}
	br := bufio.NewReader(conn)
	res, err := http.ReadResponse(br, nil)
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	if res.StatusCode != http.StatusSwitchingProtocols && res.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(io.LimitReader(res.Body, 4<<10))
		_ = conn.Close()
		return nil, fmt.Errorf("docker exec start: %s %s", res.Status, compactDockerError(string(raw)))
	}
	if buffered := br.Buffered(); buffered > 0 {
		peek, _ := br.Peek(buffered)
		return &prefixConn{Conn: conn, prefix: peek}, nil
	}
	return conn, nil
}

type prefixConn struct {
	net.Conn
	prefix []byte
}

func (c *prefixConn) Read(p []byte) (int, error) {
	if len(c.prefix) > 0 {
		n := copy(p, c.prefix)
		c.prefix = c.prefix[n:]
		return n, nil
	}
	return c.Conn.Read(p)
}

func (s *execSession) Read(p []byte) (int, error) {
	return s.conn.Read(p)
}

func (s *execSession) Write(p []byte) (int, error) {
	return s.conn.Write(p)
}

func (s *execSession) Resize(rows, cols uint16) error {
	if s.cli == nil || s.id == "" {
		return nil
	}
	path := fmt.Sprintf("/exec/%s/resize?h=%d&w=%d", url.PathEscape(s.id), rows, cols)
	return s.cli.json(context.Background(), http.MethodPost, path, nil, nil)
}

func (s *execSession) CWD() (string, bool) {
	return "/", false
}

func (s *execSession) Pong() error {
	return nil
}

func (s *execSession) Done() <-chan struct{} {
	return s.done
}

func (s *execSession) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	s.closed = true
	_ = s.conn.Close()
	select {
	case <-s.done:
	default:
		close(s.done)
	}
}
