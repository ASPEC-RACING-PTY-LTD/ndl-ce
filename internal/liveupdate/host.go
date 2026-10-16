package liveupdate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os/exec"
	"strings"
	"time"
)

// Host is the System of a real NDL-CE host: systemd for restarts and the
// local control socket (root is an administrator there) for health,
// running operations and migrations.
type Host struct {
	ControlSocket string
	HealthTimeout time.Duration
}

func (h Host) client() *http.Client {
	sock := h.ControlSocket
	if sock == "" {
		sock = "/run/ndl/control.sock"
	}
	return &http.Client{
		Timeout: 20 * time.Second,
		Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", sock)
		}},
	}
}

func (h Host) get(ctx context.Context, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://ndl"+path, nil)
	if err != nil {
		return err
	}
	res, err := h.client().Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("%s answered %s", path, res.Status)
	}
	return json.NewDecoder(res.Body).Decode(out)
}

// Status is what the control plane reports to the updater.
type Status struct {
	Busy              []string `json:"busy"`
	AppliedMigrations []string `json:"applied_migrations"`
	PackageVersion    string   `json:"package_version"`
}

func (h Host) status(ctx context.Context) (Status, error) {
	var st Status
	err := h.get(ctx, "/api/v1/live-updates", &st)
	return st, err
}

func (h Host) Busy(ctx context.Context) ([]string, error) {
	st, err := h.status(ctx)
	return st.Busy, err
}

func (h Host) AppliedMigrations(ctx context.Context) ([]string, error) {
	st, err := h.status(ctx)
	return st.AppliedMigrations, err
}

func (h Host) PackageVersion() string {
	out, err := exec.Command("dpkg-query", "-W", "-f=${Version}", "ndl-control").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// CheckBinary runs "<binary> version" and expects the bundle's build ID.
func (h Host) CheckBinary(ctx context.Context, path, build string) error {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, path, "version").CombinedOutput()
	if err != nil {
		return fmt.Errorf("%v: %s", err, strings.TrimSpace(string(out)))
	}
	if !strings.Contains(string(out), build) {
		return fmt.Errorf("it reports %q, not build %s", strings.TrimSpace(string(out)), build)
	}
	return nil
}

func unit(component string) string {
	return binName(component) + ".service"
}

func (h Host) Restart(ctx context.Context, component string) error {
	out, err := exec.CommandContext(ctx, "systemctl", "restart", unit(component)).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// Healthy waits for the control plane to answer as build, or for the agent
// to be active and reachable through the control plane.
func (h Host) Healthy(ctx context.Context, component, build string) error {
	timeout := h.HealthTimeout
	if timeout <= 0 {
		timeout = 90 * time.Second
	}
	deadline := time.Now().Add(timeout)
	var last error
	for time.Now().Before(deadline) {
		last = h.check(ctx, component, build)
		if last == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
	if last == nil {
		last = errors.New("timed out")
	}
	return last
}

func (h Host) check(ctx context.Context, component, build string) error {
	if out, err := exec.CommandContext(ctx, "systemctl", "is-active", unit(component)).Output(); err != nil || strings.TrimSpace(string(out)) != "active" {
		return fmt.Errorf("%s is %s", unit(component), strings.TrimSpace(string(out)))
	}
	var health struct {
		Status     string `json:"status"`
		Build      string `json:"build"`
		AgentBuild string `json:"agent_build"`
	}
	if err := h.get(ctx, "/api/v1/health", &health); err != nil {
		return err
	}
	if health.Status != "ok" {
		return fmt.Errorf("control plane reports %q", health.Status)
	}
	switch component {
	case CompControl:
		if build != "" && health.Build != build {
			return fmt.Errorf("control plane still runs build %q", health.Build)
		}
	case CompAgent:
		if health.AgentBuild == "" {
			return errors.New("the agent does not answer")
		}
		if build != "" && health.AgentBuild != build {
			return fmt.Errorf("agent still runs build %q", health.AgentBuild)
		}
	}
	return nil
}
