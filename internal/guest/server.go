package guest

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net"
	"os"
	"path"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/no-dal/ndl-ce/internal/iojail"
)

const filesChunk = 8 << 20

// Host is the in-guest implementation. It never executes a host command.
type Host struct {
	Root     string
	OS       string
	Arch     string
	Version  string
	Features []string
	FakePTY  bool
	Now      func() time.Time

	mu   sync.Mutex
	ptys map[string]*ptySlot
}

type ptySlot struct {
	id     string
	buf    *bytes.Buffer
	closed bool
	linux  ptySession
}

type ptySession interface {
	Read(p []byte) (int, error)
	Write(p []byte) (int, error)
	Resize(rows, cols uint16) error
	Close() error
}

func (h *Host) root() string {
	if strings.TrimSpace(h.Root) != "" {
		return h.Root
	}
	if runtime.GOOS == "windows" {
		return `C:\`
	}
	return "/"
}

func (h *Host) info() Info {
	osName := h.OS
	if osName == "" {
		osName = runtime.GOOS
	}
	arch := h.Arch
	if arch == "" {
		arch = runtime.GOARCH
	}
	ver := h.Version
	if ver == "" {
		ver = Version
	}
	features := h.Features
	if len(features) == 0 {
		features = []string{"files", "network", "shutdown"}
		if osName != "windows" {
			features = append(features, "pty")
		}
	}
	host, _ := os.Hostname()
	return Info{Version: ver, OS: osName, Arch: arch, Hostname: host, Features: features}
}

// ServeConn answers NDJSON guest RPCs until the channel closes.
func (h *Host) ServeConn(ctx context.Context, rw io.ReadWriteCloser) error {
	defer rw.Close()
	rd := bufio.NewReader(rw)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		line, err := rd.ReadBytes('\n')
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
		var req Request
		if err := json.Unmarshal(line, &req); err != nil {
			_ = writeJSONLine(rw, Response{OK: false, Error: "invalid guest frame"})
			continue
		}
		res := h.handle(ctx, req)
		if err := writeJSONLine(rw, res); err != nil {
			return err
		}
	}
}

func (h *Host) handle(ctx context.Context, req Request) Response {
	res := Response{ID: req.ID}
	raw, err := h.dispatch(ctx, req.Method, req.Params)
	if err != nil {
		res.Error = err.Error()
		return res
	}
	res.OK = true
	res.Result = raw
	return res
}

func (h *Host) dispatch(ctx context.Context, method string, params json.RawMessage) (json.RawMessage, error) {
	switch method {
	case MethodPing:
		return okResult(map[string]any{"pong": true}), nil
	case MethodInfo, MethodOSInfo:
		return okResult(h.info()), nil
	case MethodNetwork:
		return okResult(map[string]any{"ipv4": guestIPv4()}), nil
	case MethodMetrics:
		return okResult(map[string]any{"status": "collecting"}), nil
	case MethodShutdown:
		return h.shutdown(params)
	case MethodFreeze:
		return okResult(map[string]any{"coordinated": false, "reason": "qemu-ga remains freeze/shutdown"}), nil
	case MethodFilesOp:
		return h.filesOp(params)
	case MethodFilesPut:
		return h.filesPut(params)
	case MethodFilesGet:
		return h.filesGet(params)
	case MethodPTYOpen:
		return h.ptyOpen(ctx, params)
	case MethodPTYWrite:
		return h.ptyWrite(params)
	case MethodPTYRead:
		return h.ptyRead(params)
	case MethodPTYResize:
		return h.ptyResize(params)
	case MethodPTYClose:
		return h.ptyClose(params)
	default:
		return nil, fmt.Errorf("unknown guest method %q", method)
	}
}

func (h *Host) filesOp(params json.RawMessage) (json.RawMessage, error) {
	var p FilesParams
	if err := json.Unmarshal(nonzero(params), &p); err != nil {
		return nil, err
	}
	root := h.root()
	switch strings.ToLower(strings.TrimSpace(p.Action)) {
	case "list":
		return filesList(root, p.Path)
	case "stat":
		return filesStat(root, p.Path)
	case "mkdir":
		mode := fs.FileMode(p.Mode)
		if mode == 0 {
			mode = 0o755
		}
		if err := iojail.MkdirBeneath(root, p.Path, mode); err != nil {
			return nil, err
		}
		return okResult(map[string]any{"ok": true, "path": p.Path}), nil
	case "delete":
		if err := iojail.RemoveBeneath(root, p.Path); err != nil {
			return nil, err
		}
		return okResult(map[string]any{"ok": true, "path": p.Path}), nil
	case "rename":
		if err := iojail.RenameBeneath(root, p.Path, p.Dest); err != nil {
			return nil, err
		}
		return okResult(map[string]any{"ok": true, "path": p.Dest}), nil
	case "copy":
		if err := filesCopy(root, p.Path, p.Dest); err != nil {
			return nil, err
		}
		return okResult(map[string]any{"ok": true, "path": p.Dest}), nil
	default:
		return nil, fmt.Errorf("unknown files action %q", p.Action)
	}
}

func (h *Host) filesPut(params json.RawMessage) (json.RawMessage, error) {
	var p FilesParams
	if err := json.Unmarshal(nonzero(params), &p); err != nil {
		return nil, err
	}
	raw, err := base64.StdEncoding.DecodeString(p.DataB64)
	if err != nil {
		return nil, fmt.Errorf("data_b64 is invalid")
	}
	if len(raw) > filesChunk {
		return nil, fmt.Errorf("upload exceeds 8 MiB")
	}
	mode := p.Mode
	if mode == 0 {
		mode = 0o644
	}
	part := p.Path + ".part"
	out, _, err := iojail.OpenBeneath(h.root(), part, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, fs.FileMode(mode))
	if err != nil {
		return nil, err
	}
	if _, err := out.Write(raw); err != nil {
		_ = out.Close()
		_ = iojail.RemoveBeneath(h.root(), part)
		return nil, err
	}
	if err := out.Close(); err != nil {
		_ = iojail.RemoveBeneath(h.root(), part)
		return nil, err
	}
	if err := iojail.RenameBeneath(h.root(), part, p.Path); err != nil {
		_ = iojail.RemoveBeneath(h.root(), part)
		return nil, err
	}
	sum := sha256.Sum256(raw)
	return okResult(map[string]any{"ok": true, "path": p.Path, "size": len(raw), "sha256": hex.EncodeToString(sum[:])}), nil
}

func (h *Host) filesGet(params json.RawMessage) (json.RawMessage, error) {
	var p FilesParams
	if err := json.Unmarshal(nonzero(params), &p); err != nil {
		return nil, err
	}
	f, _, err := iojail.OpenBeneath(h.root(), p.Path, os.O_RDONLY, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, filesChunk+1))
	if err != nil {
		return nil, err
	}
	if len(raw) > filesChunk {
		return nil, fmt.Errorf("file exceeds 8 MiB")
	}
	sum := sha256.Sum256(raw)
	return okResult(map[string]any{
		"path":     p.Path,
		"data_b64": base64.StdEncoding.EncodeToString(raw),
		"sha256":   hex.EncodeToString(sum[:]),
		"size":     len(raw),
	}), nil
}

func (h *Host) shutdown(params json.RawMessage) (json.RawMessage, error) {
	var p ShutdownParams
	_ = json.Unmarshal(nonzero(params), &p)
	mode := strings.ToLower(strings.TrimSpace(p.Mode))
	if mode == "" {
		mode = "powerdown"
	}
	if h.FakePTY || h.Root != "" {
		return okResult(map[string]any{"accepted": true, "mode": mode, "fixture": true}), nil
	}
	if err := guestShutdown(mode); err != nil {
		return nil, err
	}
	return okResult(map[string]any{"accepted": true, "mode": mode}), nil
}

func (h *Host) ptyOpen(ctx context.Context, params json.RawMessage) (json.RawMessage, error) {
	var p PTYParams
	_ = json.Unmarshal(nonzero(params), &p)
	info := h.info()
	if info.OS == "windows" && !h.FakePTY {
		return nil, fmt.Errorf("Windows guest PTY is not implemented; use Console plus qemu-ga")
	}
	id := uuid.NewString()
	slot := &ptySlot{id: id, buf: bytes.NewBuffer(nil)}
	if h.FakePTY || strings.TrimSpace(h.Root) != "" {
		_, _ = slot.buf.WriteString("nodal-guest-pty\n")
	} else {
		sess, err := openGuestPTY(ctx, p.CWD)
		if err != nil {
			return nil, err
		}
		slot.linux = sess
	}
	h.mu.Lock()
	if h.ptys == nil {
		h.ptys = map[string]*ptySlot{}
	}
	h.ptys[id] = slot
	h.mu.Unlock()
	return okResult(map[string]any{"session": id}), nil
}

func (h *Host) ptyWrite(params json.RawMessage) (json.RawMessage, error) {
	var p PTYParams
	if err := json.Unmarshal(nonzero(params), &p); err != nil {
		return nil, err
	}
	slot, err := h.pty(p.Session)
	if err != nil {
		return nil, err
	}
	raw, err := base64.StdEncoding.DecodeString(p.DataB64)
	if err != nil {
		return nil, fmt.Errorf("data_b64 is invalid")
	}
	if slot.linux != nil {
		_, err = slot.linux.Write(raw)
		return okResult(map[string]any{"ok": true}), err
	}
	_, _ = slot.buf.Write(raw)
	return okResult(map[string]any{"ok": true}), nil
}

func (h *Host) ptyRead(params json.RawMessage) (json.RawMessage, error) {
	var p PTYParams
	if err := json.Unmarshal(nonzero(params), &p); err != nil {
		return nil, err
	}
	slot, err := h.pty(p.Session)
	if err != nil {
		return nil, err
	}
	buf := make([]byte, 32*1024)
	if slot.linux != nil {
		n, rerr := slot.linux.Read(buf)
		if n > 0 {
			return okResult(PTYParams{Session: p.Session, DataB64: base64.StdEncoding.EncodeToString(buf[:n]), EOF: rerr == io.EOF}), nil
		}
		if rerr == io.EOF {
			return okResult(PTYParams{Session: p.Session, EOF: true}), nil
		}
		if rerr != nil {
			return nil, rerr
		}
		return okResult(PTYParams{Session: p.Session}), nil
	}
	raw := slot.buf.Next(len(buf))
	return okResult(PTYParams{Session: p.Session, DataB64: base64.StdEncoding.EncodeToString(raw), EOF: slot.closed && len(raw) == 0}), nil
}

func (h *Host) ptyResize(params json.RawMessage) (json.RawMessage, error) {
	var p PTYParams
	if err := json.Unmarshal(nonzero(params), &p); err != nil {
		return nil, err
	}
	slot, err := h.pty(p.Session)
	if err != nil {
		return nil, err
	}
	if slot.linux != nil {
		return okResult(map[string]any{"ok": true}), slot.linux.Resize(p.Rows, p.Cols)
	}
	return okResult(map[string]any{"ok": true}), nil
}

func (h *Host) ptyClose(params json.RawMessage) (json.RawMessage, error) {
	var p PTYParams
	if err := json.Unmarshal(nonzero(params), &p); err != nil {
		return nil, err
	}
	h.mu.Lock()
	slot, ok := h.ptys[p.Session]
	if ok {
		delete(h.ptys, p.Session)
	}
	h.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("pty session is gone")
	}
	slot.closed = true
	if slot.linux != nil {
		_ = slot.linux.Close()
	}
	return okResult(map[string]any{"ok": true}), nil
}

func (h *Host) pty(id string) (*ptySlot, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	slot, ok := h.ptys[id]
	if !ok || slot.closed {
		return nil, fmt.Errorf("pty session is gone")
	}
	return slot, nil
}

func filesList(root, rel string) (json.RawMessage, error) {
	f, _, err := iojail.OpenBeneath(root, rel, os.O_RDONLY, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("path is not a directory")
	}
	names, err := f.Readdirnames(-1)
	if err != nil {
		return nil, err
	}
	entries := make([]map[string]any, 0, len(names))
	for _, name := range names {
		child := path.Join(rel, name)
		if rel == "" || rel == "." || rel == "/" {
			child = name
		}
		ent, err := filesStatMap(root, child, name)
		if err != nil {
			continue
		}
		entries = append(entries, ent)
	}
	return okResult(map[string]any{"path": rel, "entries": entries}), nil
}

func filesStat(root, rel string) (json.RawMessage, error) {
	ent, err := filesStatMap(root, rel, path.Base(rel))
	if err != nil {
		return nil, err
	}
	return okResult(ent), nil
}

func filesStatMap(root, rel, name string) (map[string]any, error) {
	f, _, err := iojail.OpenBeneath(root, rel, os.O_RDONLY, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	kind := "file"
	if info.IsDir() {
		kind = "dir"
	}
	return map[string]any{
		"name": name, "type": kind, "size": info.Size(),
		"mode": uint32(info.Mode().Perm()), "path": rel,
		"mtime": info.ModTime().UTC().Format(time.RFC3339),
	}, nil
}

func filesCopy(root, src, dest string) error {
	in, _, err := iojail.OpenBeneath(root, src, os.O_RDONLY, 0)
	if err != nil {
		return err
	}
	defer in.Close()
	info, err := in.Stat()
	if err != nil {
		return err
	}
	if info.IsDir() {
		return fmt.Errorf("directory copy is not implemented in the guest subset")
	}
	part := dest + ".part"
	out, _, err := iojail.OpenBeneath(root, part, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, info.Mode().Perm())
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, in)
	closeErr := out.Close()
	if copyErr != nil {
		_ = iojail.RemoveBeneath(root, part)
		return copyErr
	}
	if closeErr != nil {
		_ = iojail.RemoveBeneath(root, part)
		return closeErr
	}
	return iojail.RenameBeneath(root, part, dest)
}

func nonzero(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return json.RawMessage(`{}`)
	}
	return raw
}

func guestIPv4() []string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	var out []string
	for _, iface := range ifaces {
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			ipnet, ok := a.(*net.IPNet)
			if !ok || ipnet.IP == nil || ipnet.IP.IsLoopback() {
				continue
			}
			v4 := ipnet.IP.To4()
			if v4 == nil {
				continue
			}
			out = append(out, v4.String())
		}
	}
	return out
}

// ListenAndServe serves one unix or virtio-serial path.
func ListenAndServe(ctx context.Context, network, address string, h *Host) error {
	if network == "unix" {
		_ = os.Remove(address)
		ln, err := net.Listen("unix", address)
		if err != nil {
			return err
		}
		defer ln.Close()
		for {
			conn, err := ln.Accept()
			if err != nil {
				if ctx.Err() != nil {
					return nil
				}
				return err
			}
			go func(c net.Conn) { _ = h.ServeConn(ctx, c) }(conn)
		}
	}
	f, err := os.OpenFile(address, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	return h.ServeConn(ctx, f)
}
