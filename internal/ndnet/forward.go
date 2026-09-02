package ndnet

import (
	"os"
	"path/filepath"
	"strings"
)

const (
	// ForwardSysctlFile is the No-dal-owned sysctl drop-in. It is not a generic host policy.
	ForwardSysctlFile = "50-ndl-ip-forward.conf"
	forwardOriginFile = "forwarding.origin"
	forwardSysctlBody = "# No-dal isolated-nat. Removed with the last isolated-nat network.\nnet.ipv4.ip_forward=1\n"
)

func (e *Engine) sysctlDir() string {
	if e.Root != "" && e.Root != "/" {
		return filepath.Join(e.Root, "etc/sysctl.d")
	}
	return "/etc/sysctl.d"
}

func (e *Engine) procForwardPath() string {
	if e.Root != "" && e.Root != "/" {
		return filepath.Join(e.Root, "proc/sys/net/ipv4/ip_forward")
	}
	return "/proc/sys/net/ipv4/ip_forward"
}

func (e *Engine) forwardSysctlPath() string {
	return filepath.Join(e.sysctlDir(), ForwardSysctlFile)
}

func (e *Engine) forwardOriginPath() string {
	return filepath.Join(e.stateDir(), forwardOriginFile)
}

func (e *Engine) readForwarding() string {
	raw, err := os.ReadFile(e.procForwardPath())
	if err != nil {
		return "0"
	}
	v := strings.TrimSpace(string(raw))
	if v == "1" {
		return "1"
	}
	return "0"
}

func (e *Engine) writeForwarding(v string) error {
	path := e.procForwardPath()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(v+"\n"), 0644)
}

func (e *Engine) enableForwarding() error {
	if err := os.MkdirAll(e.stateDir(), 0700); err != nil {
		return err
	}
	if err := os.MkdirAll(e.sysctlDir(), 0755); err != nil {
		return err
	}
	if _, err := os.Stat(e.forwardOriginPath()); err != nil {
		if err := os.WriteFile(e.forwardOriginPath(), []byte(e.readForwarding()+"\n"), 0600); err != nil {
			return err
		}
	}
	if err := os.WriteFile(e.forwardSysctlPath(), []byte(forwardSysctlBody), 0644); err != nil {
		return err
	}
	return e.writeForwarding("1")
}

func (e *Engine) disableForwardingIfUnused() error {
	if e.natPersistCount() > 0 {
		return nil
	}
	_ = os.Remove(e.forwardSysctlPath())
	origin := e.readOrigin()
	_ = os.Remove(e.forwardOriginPath())
	if origin == "0" {
		return e.writeForwarding("0")
	}
	return nil
}

func (e *Engine) readOrigin() string {
	raw, err := os.ReadFile(e.forwardOriginPath())
	if err != nil {
		return "1"
	}
	if strings.TrimSpace(string(raw)) == "0" {
		return "0"
	}
	return "1"
}

func (e *Engine) natPersistCount() int {
	dir := filepath.Join(e.stateDir(), "nft")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	n := 0
	for _, ent := range entries {
		if ent.IsDir() || !natPersistName(ent.Name()) {
			continue
		}
		n++
	}
	return n
}

func natPersistName(name string) bool {
	if !strings.HasSuffix(name, ".nft") {
		return false
	}
	if strings.HasSuffix(name, ".destroy.nft") || strings.HasSuffix(name, ".check.nft") {
		return false
	}
	return true
}
