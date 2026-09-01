package qemu

import (
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// Engine writes frozen argv and supervises nodal-vm@ via systemd.
type Engine struct {
	DataDir      string
	SkipHostCmds bool
	Now          func() time.Time
	LiveUnits    map[string]bool
}

func (e *Engine) dataDir() string {
	if e.DataDir != "" {
		return e.DataDir
	}
	return "/var/lib/ndl"
}

func (e *Engine) runtimeDir(id string) string {
	return filepath.Join(e.dataDir(), "runtime", "qemu", id)
}

func (e *Engine) workloadDir(id string) string {
	return filepath.Join(e.dataDir(), "workloads", id)
}

func (e *Engine) argvPath(id string) string {
	return filepath.Join(e.workloadDir(id), "qemu-argv.json")
}

func (e *Engine) appliedPath(id string) string {
	return filepath.Join(e.workloadDir(id), "qemu-last-applied.json")
}

func (e *Engine) qmpPath(id string) string {
	return filepath.Join(e.runtimeDir(id), "qmp.sock")
}

func (e *Engine) serialPath(id string) string {
	return filepath.Join(e.runtimeDir(id), "serial.sock")
}

func (e *Engine) vncPath(id string) string {
	return filepath.Join(e.runtimeDir(id), "vnc.sock")
}

func (e *Engine) qgaPath(id string) string {
	return filepath.Join(e.runtimeDir(id), "qga.sock")
}

func (e *Engine) now() time.Time {
	if e.Now != nil {
		return e.Now()
	}
	return time.Now().UTC()
}

func (e *Engine) ensureDirs(id string) error {
	parents := []string{
		e.dataDir(),
		filepath.Join(e.dataDir(), "workloads"),
		filepath.Join(e.dataDir(), "runtime"),
		filepath.Join(e.dataDir(), "runtime", "qemu"),
		e.runtimeDir(id),
		e.workloadDir(id),
	}
	for _, d := range parents {
		if err := os.MkdirAll(d, 0o751); err != nil {
			return err
		}
		if err := ensureOtherTraverse(d); err != nil {
			return err
		}
	}
	return nil
}

// ensureOtherTraverse adds world-execute so ndl-qemu can walk a root-owned
// parent without listing it. It never adds world-read.
func ensureOtherTraverse(path string) error {
	st, err := os.Stat(path)
	if err != nil {
		return err
	}
	if !st.IsDir() {
		return nil
	}
	mode := st.Mode().Perm()
	if mode&0o001 != 0 {
		return nil
	}
	chmodErr := os.Chmod(path, mode|0o001)
	if chmodErr == nil {
		return nil
	}
	// ndl-agent's bounding set has no CAP_FOWNER. After chown to ndl-qemu,
	// chmod fails. Take ownership with CAP_CHOWN, add traverse, restore.
	sys, ok := st.Sys().(*syscall.Stat_t)
	if !ok {
		return chmodErr
	}
	uid, gid := int(sys.Uid), int(sys.Gid)
	if err := os.Chown(path, 0, 0); err != nil {
		return err
	}
	if err := os.Chmod(path, mode|0o001); err != nil {
		_ = os.Chown(path, uid, gid)
		return err
	}
	return os.Chown(path, uid, gid)
}

func ensurePathTraverse(abs string) error {
	root := filepath.Clean("/var/lib/ndl")
	p := filepath.Clean(abs)
	for p != "/" && p != "." {
		if err := ensureOtherTraverse(p); err != nil && !os.IsNotExist(err) {
			return err
		}
		if p == root {
			break
		}
		next := filepath.Dir(p)
		if next == p {
			break
		}
		p = next
	}
	return nil
}
