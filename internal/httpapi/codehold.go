package httpapi

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/no-dal/ndl-ce/internal/appdb"
	cf "github.com/no-dal/ndl-ce/internal/cloudflare"
	"github.com/no-dal/ndl-ce/internal/codehold"
	"github.com/no-dal/ndl-ce/internal/docker"
	"github.com/no-dal/ndl-ce/internal/features"
	"github.com/no-dal/ndl-ce/internal/lxc"
	"github.com/no-dal/ndl-ce/internal/rbac"
)

// CodeHold runners. No-dal is one of CodeHold's host controllers (see
// docs/runner-controller.md and docs/codehold.md). CodeHold keeps each
// runner's desired state; No-dal gives every runner a system container of its
// own with Docker inside and the codehold-runner agent as a service, and
// reports back what it sees. Runners are created, started, stopped and removed
// in CodeHold; No-dal follows.

// Agents that manage CodeHold runner containers implement this.
type dockerCodeholdRPC interface {
	DockerCodehold(ctx context.Context, req docker.CodeholdRequest) (docker.CodeholdResult, error)
}

// Default size of a runner's container.
const (
	chDefaultCPUs     = 2
	chDefaultMemoryMB = 2048
	chDefaultDiskGB   = 50
	chDefaultImage    = "debian/trixie/amd64/default"
	// chGracePeriod is how long a force-disabled runner's container is kept.
	chGracePeriod = 7 * 24 * time.Hour
	// chKeyPath holds the key that seals the controller credential.
	chKeyPath = "/var/lib/ndl/control/keys/codehold.key"
)

// chPollEvery is how often setup checks whether Docker finished installing.
var chPollEvery = 10 * time.Second

// Secret names in secrets.codehold_secrets.
const (
	chSecretToken    = "token"
	chSecretPrevious = "previous_token"
	chSecretCFID     = "cf_client_id"
	chSecretCFSecret = "cf_client_secret"
)

// Controller connection states.
const (
	chConnected    = "connected"
	chUnreachable  = "unreachable"
	chRejected     = "rejected"
	chDisconnected = "disconnected"
)

type chSettings struct {
	CPUs     int    `json:"cpus"`
	MemoryMB int    `json:"memory_mb"`
	DiskGB   int    `json:"disk_gb"`
	PoolID   string `json:"pool_id,omitempty"`
	// NetworkID is the network runner containers join.
	NetworkID string `json:"network_id,omitempty"`
	ImagePin  string `json:"image_pin"`
	// CapacityCPUs and CapacityMemoryMB are what CodeHold may reserve on
	// this node; zero uses what the runner containers could use.
	CapacityCPUs     float64 `json:"capacity_cpus,omitempty"`
	CapacityMemoryMB int     `json:"capacity_memory_mb,omitempty"`
	// Source is "local" (a CodeHold container on this node) or "url".
	Source string `json:"source"`
	// LocalMachineID and LocalContainer find the local CodeHold container.
	LocalMachineID   string `json:"local_machine_id,omitempty"`
	LocalContainer   string `json:"local_container,omitempty"`
	DownloadURL      string `json:"download_url,omitempty"`
	ChecksumRequired bool   `json:"checksum_required"`
	SHA256           string `json:"sha256,omitempty"`
}

func (st *chSettings) defaults() {
	if st.CPUs <= 0 {
		st.CPUs = chDefaultCPUs
	}
	if st.MemoryMB <= 0 {
		st.MemoryMB = chDefaultMemoryMB
	}
	if st.DiskGB <= 0 {
		st.DiskGB = chDefaultDiskGB
	}
	if st.ImagePin == "" {
		st.ImagePin = chDefaultImage
	}
	if st.Source == "" {
		st.Source = "local"
	}
}

var chSHA256Re = regexp.MustCompile(`^[0-9a-f]{64}$`)

func (st chSettings) validate() error {
	if st.CPUs < 1 || st.CPUs > 64 {
		return errors.New("runner CPUs must be between 1 and 64")
	}
	if st.MemoryMB < 512 || st.MemoryMB > 262144 {
		return errors.New("runner memory must be between 512 MB and 256 GB")
	}
	if st.DiskGB < 10 || st.DiskGB > 4096 {
		return errors.New("runner disk must be between 10 and 4096 GB")
	}
	if err := lxc.ValidatePin(st.ImagePin); err != nil {
		return err
	}
	if st.CapacityCPUs < 0 || st.CapacityMemoryMB < 0 {
		return errors.New("capacity cannot be negative")
	}
	switch st.Source {
	case "local":
	case "url":
		u := strings.TrimSpace(st.DownloadURL)
		if !strings.HasPrefix(u, "https://") || strings.ContainsAny(u, " \n\r") {
			return errors.New("the runner download must be an https:// address")
		}
		if st.ChecksumRequired && !chSHA256Re.MatchString(strings.ToLower(st.SHA256)) {
			return errors.New("enter the runner's SHA-256 checksum (64 hexadecimal characters) or turn the checksum off")
		}
	default:
		return errors.New("the runner source must be local or url")
	}
	return nil
}

// chState is the in-memory side of the integration: the sync loop, runners
// being set up, and log lines waiting to be sent to CodeHold.
type chState struct {
	mu           sync.Mutex
	cancel       context.CancelFunc
	running      bool
	wake         chan struct{}
	provisioning map[string]bool
	logs         []string
	runnerLogs   map[string][]string
	lastAgent    map[string]string
	retryAfter   map[string]time.Time
	instances    []chInstance
	instancesAt  time.Time
	sealer       *cf.Sealer
}

// chInstance is a CodeHold server found on this node.
type chInstance struct {
	docker.CodeholdInstance
	// URL is the address No-dal and runners can use for it on the local
	// network, without public routing.
	URL string `json:"url,omitempty"`
}

func (s *Server) chSealer() *cf.Sealer {
	s.ch.mu.Lock()
	defer s.ch.mu.Unlock()
	if s.ch.sealer == nil {
		path := s.CHKeyPath
		if path == "" {
			path = chKeyPath
		}
		s.ch.sealer = &cf.Sealer{Path: path}
	}
	return s.ch.sealer
}

func (s *Server) chLog(format string, args ...any) {
	s.ch.mu.Lock()
	defer s.ch.mu.Unlock()
	if len(s.ch.logs) < 200 {
		s.ch.logs = append(s.ch.logs, fmt.Sprintf(format, args...))
	}
}

func (s *Server) chRunnerLog(id, format string, args ...any) {
	s.ch.mu.Lock()
	defer s.ch.mu.Unlock()
	if s.ch.runnerLogs == nil {
		s.ch.runnerLogs = map[string][]string{}
	}
	if len(s.ch.runnerLogs[id]) < 100 {
		s.ch.runnerLogs[id] = append(s.ch.runnerLogs[id], fmt.Sprintf(format, args...))
	}
}

func (s *Server) chRPC() (dockerCodeholdRPC, bool) {
	rpc, ok := s.dockerRPC().(dockerCodeholdRPC)
	return rpc, ok
}

func (s *Server) chEnabled(ctx context.Context, clusterID string) bool {
	row, _ := s.Store.GetFeature(ctx, clusterID, features.IDCodehold)
	return row != nil && row.Enabled
}

func (s *Server) chSettingsOf(c *appdb.CodeholdController) chSettings {
	var st chSettings
	if c != nil && len(c.Settings) > 0 {
		_ = json.Unmarshal(c.Settings, &st)
	}
	st.defaults()
	return st
}

func (s *Server) chSecret(ctx context.Context, clusterID, name string) string {
	sealed, err := s.Store.GetCodeholdSecret(ctx, clusterID, name)
	if err != nil || sealed == "" {
		return ""
	}
	plain, err := s.chSealer().Open(clusterID+"/"+name, sealed)
	if err != nil {
		return ""
	}
	return plain
}

func (s *Server) chPutSecret(ctx context.Context, clusterID, name, plain string) error {
	if plain == "" {
		return s.Store.PutCodeholdSecret(ctx, clusterID, name, "")
	}
	sealed, err := s.chSealer().Seal(clusterID+"/"+name, plain)
	if err != nil {
		return err
	}
	return s.Store.PutCodeholdSecret(ctx, clusterID, name, sealed)
}

// --- system calls --------------------------------------------------------

type systemPrincipalKey struct{}

// systemCall runs one API request in-process as the CodeHold controller, so
// runner containers are created, started, stopped and deleted by exactly the
// same code as any other workload, and audited.
func (s *Server) systemCall(ctx context.Context, clusterID, method, path string, body any) (int, []byte) {
	var raw []byte
	if body != nil {
		raw, _ = json.Marshal(body)
	}
	p := &principal{
		User:   appdb.User{ID: LocalRootUserID, ClusterID: clusterID, Username: "codehold-controller", Kind: appdb.UserKindPerson},
		Roles:  []string{rbac.SuperAdmin},
		Grants: rbac.New().PermissionsForRole(rbac.SuperAdmin),
		SessID: "codehold-controller",
		AAL:    2,
	}
	req, err := http.NewRequestWithContext(context.WithValue(ctx, systemPrincipalKey{}, p), method, path, strings.NewReader(string(raw)))
	if err != nil {
		return 0, nil
	}
	req.Header.Set("Content-Type", "application/json")
	rec := &memResponse{header: http.Header{}}
	s.internalHandler().ServeHTTP(rec, req)
	return rec.status, rec.body
}

func (s *Server) internalHandler() http.Handler {
	s.internalOnce.Do(func() { s.internalMux = s.Handler() })
	return s.internalMux
}

type memResponse struct {
	header http.Header
	status int
	body   []byte
}

func (m *memResponse) Header() http.Header { return m.header }
func (m *memResponse) Write(b []byte) (int, error) {
	if m.status == 0 {
		m.status = http.StatusOK
	}
	m.body = append(m.body, b...)
	return len(b), nil
}
func (m *memResponse) WriteHeader(code int) {
	if m.status == 0 {
		m.status = code
	}
}

func apiErrText(code int, body []byte) string {
	var e struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(body, &e) == nil && e.Error != "" {
		return e.Error
	}
	return fmt.Sprintf("HTTP %d", code)
}

// --- the loop ------------------------------------------------------------

// RunCodehold keeps the CodeHold sync loop running while the integration is
// on and connected, and deletes force-disabled runners once their grace
// period ends.
func (s *Server) RunCodehold(ctx context.Context) {
	s.ch.mu.Lock()
	if s.ch.wake == nil {
		s.ch.wake = make(chan struct{}, 1)
	}
	wake := s.ch.wake
	s.ch.mu.Unlock()
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		s.chTick(ctx)
		select {
		case <-ctx.Done():
			s.chStop()
			return
		case <-t.C:
		case <-wake:
		}
	}
}

func (s *Server) chWake() {
	s.ch.mu.Lock()
	defer s.ch.mu.Unlock()
	if s.ch.wake == nil {
		s.ch.wake = make(chan struct{}, 1)
	}
	select {
	case s.ch.wake <- struct{}{}:
	default:
	}
}

func (s *Server) chStop() {
	s.ch.mu.Lock()
	defer s.ch.mu.Unlock()
	if s.ch.cancel != nil {
		s.ch.cancel()
		s.ch.cancel = nil
	}
}

func (s *Server) chTick(ctx context.Context) {
	cl, err := s.Store.GetCluster(ctx)
	if err != nil || cl == nil {
		return
	}
	s.chSweep(ctx, cl.ID)
	ctrl, _ := s.Store.GetCodeholdController(ctx, cl.ID)
	want := ctrl != nil && ctrl.Status != chRejected && s.chEnabled(ctx, cl.ID) && s.chSecret(ctx, cl.ID, chSecretToken) != ""
	s.ch.mu.Lock()
	running := s.ch.running
	s.ch.mu.Unlock()
	switch {
	case want && !running:
		lctx, cancel := context.WithCancel(ctx)
		s.ch.mu.Lock()
		s.ch.cancel, s.ch.running = cancel, true
		s.ch.mu.Unlock()
		go func() {
			defer func() {
				s.ch.mu.Lock()
				s.ch.running = false
				s.ch.mu.Unlock()
			}()
			s.chLoop(lctx, cl.ID)
		}()
	case !want && running:
		s.chStop()
	}
}

// chSweep deletes containers of force-disabled runners after the grace period.
func (s *Server) chSweep(ctx context.Context, clusterID string) {
	runners, _ := s.Store.ListCodeholdRunners(ctx, clusterID)
	now := s.now()
	for _, r := range runners {
		if !r.Detached || r.DeleteAfter == nil || now.Before(*r.DeleteAfter) {
			continue
		}
		if err := s.chDeleteWorkload(ctx, clusterID, r.WorkloadID); err != nil {
			s.auditBackground(clusterID, LocalRootUserID, "codehold.runner.expire", "failed", r.Name+": "+err.Error())
			continue
		}
		_ = s.Store.DeleteCodeholdRunner(ctx, clusterID, r.RunnerID)
		s.auditBackground(clusterID, LocalRootUserID, "codehold.runner.expire", "ok", r.Name)
	}
}

func (s *Server) chLoop(ctx context.Context, clusterID string) {
	// Runners kept stopped while the integration was off are CodeHold's again.
	runners, _ := s.Store.ListCodeholdRunners(ctx, clusterID)
	for _, r := range runners {
		if r.Detached {
			r.Detached, r.DeleteAfter = false, nil
			_ = s.Store.PutCodeholdRunner(ctx, r)
		}
	}
	wait := false
	backoff := time.Second
	for ctx.Err() == nil {
		ctrl, _ := s.Store.GetCodeholdController(ctx, clusterID)
		if ctrl == nil {
			return
		}
		token := s.chSecret(ctx, clusterID, chSecretToken)
		obs := s.chObserve(ctx, clusterID)
		reply, err := s.chSync(ctx, ctrl, token, obs, wait)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			if codehold.Unauthorized(err) {
				// A rotated credential that was refused: go back to the old one.
				if prev := s.chSecret(ctx, clusterID, chSecretPrevious); prev != "" && prev != token {
					_ = s.chPutSecret(ctx, clusterID, chSecretToken, prev)
					_ = s.chPutSecret(ctx, clusterID, chSecretPrevious, "")
					s.chLog("the rotated credential was rejected; using the previous one")
					continue
				}
				s.chSetStatus(ctx, clusterID, chRejected, "CodeHold rejected this controller's credential ("+err.Error()+"). Create an enrollment token for it in CodeHold and reconnect.")
				s.auditBackground(clusterID, LocalRootUserID, "codehold.sync", "rejected", err.Error())
				return
			}
			s.chSetStatus(ctx, clusterID, chUnreachable, err.Error())
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
			}
			if backoff < 30*time.Second {
				backoff *= 2
			}
			continue
		}
		backoff = time.Second
		s.chSynced(ctx, clusterID, reply.Credential, token)
		wait = !s.chApply(ctx, clusterID, reply.Runners)
	}
}

func (s *Server) chSetStatus(ctx context.Context, clusterID, status, reason string) {
	c, _ := s.Store.GetCodeholdController(ctx, clusterID)
	if c == nil || (c.Status == status && c.StatusReason == reason) {
		return
	}
	c.Status, c.StatusReason = status, reason
	_ = s.Store.PutCodeholdController(context.WithoutCancel(ctx), *c)
}

// chSynced records a good sync and takes over a rotated credential: the new
// one is used from the next request, the old one kept until it is confirmed.
func (s *Server) chSynced(ctx context.Context, clusterID string, offer *codehold.CredentialOffer, used string) {
	c, _ := s.Store.GetCodeholdController(ctx, clusterID)
	if c == nil {
		return
	}
	now := s.now()
	c.Status, c.StatusReason, c.LastSyncAt = chConnected, "", &now
	if offer != nil && offer.Token != "" && offer.Token != used {
		if err := s.chPutSecret(ctx, clusterID, chSecretPrevious, used); err == nil {
			if err := s.chPutSecret(ctx, clusterID, chSecretToken, offer.Token); err == nil {
				exp := offer.ExpiresAt.UTC()
				c.CredentialExpiresAt = &exp
				s.chLog("credential rotated; the new one expires %s", exp.Format("2006-01-02"))
				s.auditBackground(clusterID, LocalRootUserID, "codehold.credential.rotate", "ok", "")
			}
		}
	} else if offer == nil && s.chSecret(ctx, clusterID, chSecretPrevious) != "" {
		_ = s.chPutSecret(ctx, clusterID, chSecretPrevious, "")
	}
	_ = s.Store.PutCodeholdController(context.WithoutCancel(ctx), *c)
}

func (s *Server) chClient(ctx context.Context, c *appdb.CodeholdController, token string) *codehold.Client {
	cl := &codehold.Client{URL: c.URL, Token: token, CFID: s.chSecret(ctx, c.ClusterID, chSecretCFID), CFSecret: s.chSecret(ctx, c.ClusterID, chSecretCFSecret)}
	if s.CodeholdHTTP != nil {
		cl.HTTP = s.CodeholdHTTP
	}
	return cl
}

func (s *Server) chSync(ctx context.Context, c *appdb.CodeholdController, token string, obs []codehold.ObservedRunner, wait bool) (codehold.SyncReply, error) {
	st := s.chSettingsOf(c)
	cpus, mem := s.chCapacity(ctx, c.ClusterID, st)
	host := "ndl-ce"
	if node, _ := s.Store.GetNode(ctx, c.ClusterID); node != nil && node.Name != "" {
		host = node.Name
	}
	s.ch.mu.Lock()
	logs := s.ch.logs
	s.ch.logs = nil
	s.ch.mu.Unlock()
	reply, err := s.chClient(ctx, c, token).Sync(ctx, codehold.SyncRequest{
		Wait: wait, Version: "ndl-ce " + edition, Hostname: host, OS: "linux", Arch: "amd64",
		Engine: fmt.Sprintf("ndl-ce system containers (%d vCPU, %d MB, %d GB each) with Docker", st.CPUs, st.MemoryMB, st.DiskGB),
		CPUs:   cpus, MemoryMB: mem, Image: st.ImagePin, Network: st.NetworkID, RunnerURL: firstNonEmpty(c.RunnerURL, c.URL),
		Logs: logs, Runners: obs,
	})
	if err != nil {
		s.ch.mu.Lock()
		s.ch.logs = append(logs, s.ch.logs...)
		s.ch.mu.Unlock()
	}
	return reply, err
}

// chCapacity is what CodeHold may reserve here: the configured budget, or
// the node's CPUs and three quarters of its memory.
func (s *Server) chCapacity(ctx context.Context, clusterID string, st chSettings) (float64, int) {
	cpus, mem := st.CapacityCPUs, st.CapacityMemoryMB
	if cpus > 0 && mem > 0 {
		return cpus, mem
	}
	if node, _ := s.Store.GetNode(ctx, clusterID); node != nil {
		inv, _ := s.Store.GetInventory(ctx, node.ID)
		if parsed, ok := decodeInv(inv); ok {
			if cpus <= 0 {
				cpus = float64(max(parsed.CPU.Threads, parsed.CPU.Online, parsed.CPU.Cores))
			}
			if mem <= 0 {
				mem = int(parsed.Memory.TotalBytes / (1 << 20) * 3 / 4)
			}
		}
	}
	// Without an inventory, this machine is the node (Community Edition
	// runs the control plane on it).
	if cpus <= 0 {
		cpus = float64(runtime.NumCPU())
	}
	if mem <= 0 {
		var si syscall.Sysinfo_t
		if syscall.Sysinfo(&si) == nil {
			mem = int(uint64(si.Totalram) * uint64(si.Unit) / (1 << 20) * 3 / 4)
		}
	}
	return cpus, mem
}

// chObserve reports every runner this controller holds.
func (s *Server) chObserve(ctx context.Context, clusterID string) []codehold.ObservedRunner {
	runners, _ := s.Store.ListCodeholdRunners(ctx, clusterID)
	rpc, _ := s.chRPC()
	s.ch.mu.Lock()
	logs := s.ch.runnerLogs
	s.ch.runnerLogs = nil
	s.ch.mu.Unlock()
	var out []codehold.ObservedRunner
	for _, r := range runners {
		if r.Detached {
			continue
		}
		o := codehold.ObservedRunner{ID: r.RunnerID, Project: r.WorkloadID, Logs: logs[r.RunnerID]}
		s.ch.mu.Lock()
		busy := s.ch.provisioning[r.RunnerID]
		s.ch.mu.Unlock()
		wl, _ := s.Store.GetWorkload(ctx, clusterID, r.WorkloadID)
		switch {
		case busy:
			o.State = "provisioning"
		case r.WorkloadID == "" || wl == nil:
			switch {
			case r.Desired == "removed":
				o.State = "removed"
			case r.Error != "":
				o.State, o.Error = "error", r.Error
			case r.Desired == "running":
				o.State = "provisioning"
			default:
				o.State = "stopped"
			}
		case wl.Status != "running":
			o.State = "stopped"
			if r.Error != "" && r.Desired == "running" {
				o.State, o.Error = "error", r.Error
			}
		default:
			o.State = "running"
			if rpc != nil {
				actx, cancel := context.WithTimeout(ctx, 15*time.Second)
				st, err := rpc.DockerCodehold(actx, docker.CodeholdRequest{Action: docker.CodeholdStatus, MachineID: r.WorkloadID})
				cancel()
				switch {
				case err != nil:
					o.State, o.Error = "error", "the runner's state could not be read: "+err.Error()
				case !st.Installed:
					o.State = "provisioning"
					if r.Error != "" {
						o.State, o.Error = "error", r.Error
					}
				case st.AuthFailed:
					o.State, o.AuthFailed = "provisioning", true
					o.Error = "the runner has no working credentials; waiting for a registration token"
				case !st.Active:
					o.State, o.Error = "error", "the runner service is "+firstNonEmpty(st.State, "not running")
				}
				if len(st.Logs) > 0 {
					text := strings.Join(st.Logs, "\n")
					s.ch.mu.Lock()
					if s.ch.lastAgent == nil {
						s.ch.lastAgent = map[string]string{}
					}
					if s.ch.lastAgent[r.RunnerID] != text {
						s.ch.lastAgent[r.RunnerID] = text
						for _, l := range st.Logs {
							o.Logs = append(o.Logs, "agent: "+l)
						}
					}
					s.ch.mu.Unlock()
				}
			}
		}
		if o.State != r.State || o.Error != r.Error && o.State == "error" {
			r.State = o.State
			if o.State == "error" {
				r.Error = o.Error
			}
			_ = s.Store.PutCodeholdRunner(ctx, r)
		}
		out = append(out, o)
	}
	return out
}

// chApply makes this node match what CodeHold wants. It reports whether it
// changed anything, so the next sync reports at once instead of waiting.
func (s *Server) chApply(ctx context.Context, clusterID string, desired []codehold.DesiredRunner) bool {
	acted := false
	runners, _ := s.Store.ListCodeholdRunners(ctx, clusterID)
	have := map[string]appdb.CodeholdRunner{}
	for _, r := range runners {
		have[r.RunnerID] = r
	}
	want := map[string]bool{}
	for _, d := range desired {
		want[d.ID] = true
		r, ok := have[d.ID]
		if !ok {
			r = appdb.CodeholdRunner{ClusterID: clusterID, RunnerID: d.ID, State: "provisioning"}
		}
		limits, _ := json.Marshal(d.Limits)
		changed := !ok || r.Name != d.Name || r.Desired != d.State || r.Busy != d.Busy || r.Spec != d.Spec || string(r.Limits) != string(limits)
		r.Name, r.Desired, r.Busy, r.Spec, r.Limits = d.Name, d.State, d.Busy, d.Spec, limits
		if changed {
			_ = s.Store.PutCodeholdRunner(ctx, r)
		}
		wl, _ := s.Store.GetWorkload(ctx, clusterID, r.WorkloadID)
		switch d.State {
		case "removed":
			if wl != nil {
				if err := s.chDeleteWorkload(ctx, clusterID, r.WorkloadID); err != nil {
					s.chRunnerLog(d.ID, "removing its container failed: %v", err)
				} else {
					s.chRunnerLog(d.ID, "container %s removed", wl.Name)
					r.WorkloadID, r.State = "", "removed"
					_ = s.Store.PutCodeholdRunner(ctx, r)
				}
				acted = true
			} else if r.State != "removed" {
				r.State = "removed"
				_ = s.Store.PutCodeholdRunner(ctx, r)
				acted = true
			}
		case "stopped":
			if wl != nil && wl.Status == "running" {
				if code, body := s.systemCall(ctx, clusterID, http.MethodPost, "/api/v1/workloads/"+wl.ID+"/stop", nil); code >= 300 {
					s.chRunnerLog(d.ID, "stopping its container failed: %s", apiErrText(code, body))
				} else {
					s.chRunnerLog(d.ID, "container stopped")
				}
				acted = true
			}
		case "running":
			if s.chEnsureRunning(ctx, clusterID, r, wl) {
				acted = true
			}
			if d.RegistrationToken != "" && wl != nil && wl.Status == "running" {
				sum := sha256.Sum256([]byte(d.RegistrationToken))
				hash := hex.EncodeToString(sum[:])
				if r.TokenHash != hash && s.chHandOver(ctx, clusterID, r, d.RegistrationToken) {
					r.TokenHash = hash
					_ = s.Store.PutCodeholdRunner(ctx, r)
					acted = true
				}
			}
			// Name or limits changed: CodeHold sends the new limits to the
			// agent itself; restart it when idle so it picks them up.
			if wl != nil && r.AppliedSpec != r.Spec && d.Busy == 0 && r.TokenHash != "" {
				if rpc, ok := s.chRPC(); ok {
					if _, err := rpc.DockerCodehold(ctx, docker.CodeholdRequest{Action: docker.CodeholdRestart, MachineID: wl.ID}); err == nil {
						s.chRunnerLog(d.ID, "configuration changed; runner restarted")
					}
				}
				r.AppliedSpec = r.Spec
				_ = s.Store.PutCodeholdRunner(ctx, r)
				acted = true
			}
		}
	}
	// Runners CodeHold no longer lists (removed while No-dal was away) are
	// torn down.
	for id, r := range have {
		if want[id] || r.Detached {
			continue
		}
		if r.WorkloadID != "" {
			if err := s.chDeleteWorkload(ctx, clusterID, r.WorkloadID); err != nil {
				s.chLog("removing the container of runner %s, which CodeHold no longer lists, failed: %v", r.Name, err)
				continue
			}
			s.chLog("removed the container of runner %s, which CodeHold no longer lists", r.Name)
		}
		_ = s.Store.DeleteCodeholdRunner(ctx, clusterID, id)
		acted = true
	}
	return acted
}

func (s *Server) chDeleteWorkload(ctx context.Context, clusterID, workloadID string) error {
	if workloadID == "" {
		return nil
	}
	wl, _ := s.Store.GetWorkload(ctx, clusterID, workloadID)
	if wl == nil {
		return nil
	}
	if wl.Status == "running" {
		_, _ = s.systemCall(ctx, clusterID, http.MethodPost, "/api/v1/workloads/"+workloadID+"/stop", nil)
	}
	code, body := s.systemCall(ctx, clusterID, http.MethodPost, "/api/v1/workloads/"+workloadID+"/delete", nil)
	if code >= 300 {
		return errors.New(apiErrText(code, body))
	}
	return nil
}

// chEnsureRunning creates or starts a runner's container. Creating runs in
// the background; it reports whether it started anything.
func (s *Server) chEnsureRunning(ctx context.Context, clusterID string, r appdb.CodeholdRunner, wl *appdb.Workload) bool {
	s.ch.mu.Lock()
	if s.ch.provisioning == nil {
		s.ch.provisioning = map[string]bool{}
	}
	if s.ch.provisioning[r.RunnerID] {
		s.ch.mu.Unlock()
		return false
	}
	if t, ok := s.ch.retryAfter[r.RunnerID]; ok && s.now().Before(t) {
		s.ch.mu.Unlock()
		return false
	}
	s.ch.mu.Unlock()
	if wl != nil && wl.Status != "running" {
		if code, body := s.systemCall(ctx, clusterID, http.MethodPost, "/api/v1/workloads/"+wl.ID+"/start", nil); code >= 300 {
			s.chRunnerLog(r.RunnerID, "starting its container failed: %s", apiErrText(code, body))
			return false
		}
		s.chRunnerLog(r.RunnerID, "container started")
		return true
	}
	if wl != nil {
		// Running: make sure the agent is installed (for example after the
		// source was fixed, or a failed install).
		if r.State == "error" || r.State == "provisioning" {
			if rpc, ok := s.chRPC(); ok {
				st, err := rpc.DockerCodehold(ctx, docker.CodeholdRequest{Action: docker.CodeholdStatus, MachineID: wl.ID})
				if err == nil && !st.Installed {
					s.chStartProvision(clusterID, r, false)
					return true
				}
			}
		}
		return false
	}
	s.chStartProvision(clusterID, r, true)
	return true
}

func (s *Server) chStartProvision(clusterID string, r appdb.CodeholdRunner, create bool) {
	s.ch.mu.Lock()
	s.ch.provisioning[r.RunnerID] = true
	s.ch.mu.Unlock()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Minute)
		defer cancel()
		err := s.chProvision(ctx, clusterID, r.RunnerID, create)
		s.ch.mu.Lock()
		delete(s.ch.provisioning, r.RunnerID)
		if s.ch.retryAfter == nil {
			s.ch.retryAfter = map[string]time.Time{}
		}
		if err != nil {
			// Try again later rather than in a tight loop.
			s.ch.retryAfter[r.RunnerID] = s.now().Add(5 * time.Minute)
		} else {
			delete(s.ch.retryAfter, r.RunnerID)
		}
		s.ch.mu.Unlock()
		cur, _ := s.chRunner(ctx, clusterID, r.RunnerID)
		if cur != nil {
			if err != nil {
				cur.State, cur.Error = "error", err.Error()
				s.chRunnerLog(r.RunnerID, "setup failed: %v", err)
				s.auditBackground(clusterID, LocalRootUserID, "codehold.runner.provision", "failed", cur.Name+": "+err.Error())
			} else {
				cur.Error = ""
				s.chRunnerLog(r.RunnerID, "runner installed; waiting for its registration token")
				s.auditBackground(clusterID, LocalRootUserID, "codehold.runner.provision", "ok", cur.Name)
			}
			_ = s.Store.PutCodeholdRunner(context.Background(), *cur)
		}
		// The change is reported on the next sync, which CodeHold answers
		// within 25 seconds.
	}()
}

func (s *Server) chRunner(ctx context.Context, clusterID, runnerID string) (*appdb.CodeholdRunner, error) {
	runners, err := s.Store.ListCodeholdRunners(ctx, clusterID)
	for _, r := range runners {
		if r.RunnerID == runnerID {
			return &r, nil
		}
	}
	return nil, err
}

var chNameRe = regexp.MustCompile(`[^a-z0-9-]+`)

// chWorkloadName names a runner's container after the runner.
func chWorkloadName(name, id string) string {
	n := strings.Trim(chNameRe.ReplaceAllString(strings.ToLower(name), "-"), "-")
	if len(n) > 30 {
		n = strings.Trim(n[:30], "-")
	}
	if n == "" {
		n = "runner"
	}
	short := strings.ReplaceAll(id, "-", "")
	if len(short) > 6 {
		short = short[:6]
	}
	return "codehold-" + n + "-" + strings.ToLower(short)
}

// chProvision creates a runner's container (2 vCPU, 2 GB and 50 GB by
// default, with Docker), waits for Docker, and installs the agent.
func (s *Server) chProvision(ctx context.Context, clusterID, runnerID string, create bool) error {
	r, _ := s.chRunner(ctx, clusterID, runnerID)
	if r == nil {
		return errors.New("runner is gone")
	}
	ctrl, _ := s.Store.GetCodeholdController(ctx, clusterID)
	if ctrl == nil {
		return errors.New("CodeHold is not connected")
	}
	st := s.chSettingsOf(ctrl)
	rpc, ok := s.chRPC()
	if !ok {
		return errors.New("this node's agent cannot set up CodeHold runners; update No-dal on the node")
	}
	if create {
		poolID, netID := s.chPlacement(ctx, clusterID, st)
		if poolID == "" || netID == "" {
			return errors.New("no usable storage pool or network for runner containers; create one, or choose them in the runner settings")
		}
		name := chWorkloadName(r.Name, r.RunnerID)
		s.chRunnerLog(runnerID, "creating container %s (%d vCPU, %d MB, %d GB)", name, st.CPUs, st.MemoryMB, st.DiskGB)
		code, body := s.systemCall(ctx, clusterID, http.MethodPost, "/api/v1/workloads", map[string]any{
			"name": name, "kind": "system-container", "image_pin": st.ImagePin,
			"cpus": st.CPUs, "memory_bytes": int64(st.MemoryMB) << 20, "disk_bytes": int64(st.DiskGB) << 30,
			"pool_id": poolID, "network_id": netID, "extras": []string{"docker"}, "desired_power": "running", "autostart": true,
		})
		if code >= 300 {
			return fmt.Errorf("creating the container failed: %s", apiErrText(code, body))
		}
		var wl struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(body, &wl); err != nil || wl.ID == "" {
			return errors.New("creating the container returned no workload")
		}
		r.WorkloadID, r.State, r.Error = wl.ID, "provisioning", ""
		if err := s.Store.PutCodeholdRunner(ctx, *r); err != nil {
			return err
		}
	}
	// Docker installs in the background as an extension; wait for it.
	deadline := time.Now().Add(30 * time.Minute)
	for {
		code, body := s.systemCall(ctx, clusterID, http.MethodGet, "/api/v1/workloads/"+r.WorkloadID+"/extensions", nil)
		if code == http.StatusOK {
			var ext struct {
				Busy  bool `json:"busy"`
				Items []struct {
					ID      string `json:"id"`
					State   string `json:"state"`
					Message string `json:"message"`
				} `json:"items"`
			}
			_ = json.Unmarshal(body, &ext)
			dockerState, msg := "", ""
			for _, it := range ext.Items {
				if it.ID == "docker" {
					dockerState, msg = it.State, it.Message
				}
			}
			if !ext.Busy && dockerState == "installed" {
				break
			}
			if !ext.Busy && dockerState == "failed" {
				return fmt.Errorf("installing Docker in the container failed: %s", firstNonEmpty(msg, "see its Extensions page"))
			}
			if !ext.Busy && dockerState == "missing" && create {
				// The install did not start (for example the container was
				// still booting); ask once more.
				_, _ = s.systemCall(ctx, clusterID, http.MethodPost, "/api/v1/workloads/"+r.WorkloadID+"/extensions", map[string]any{"action": "install", "extras": []string{"docker"}})
				create = false
			}
		}
		if time.Now().After(deadline) {
			return errors.New("Docker did not finish installing in 30 minutes")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(chPollEvery):
		}
	}
	src, err := s.chSource(ctx, clusterID, st)
	if err != nil {
		return err
	}
	cfID, cfSecret := s.chSecret(ctx, clusterID, chSecretCFID), s.chSecret(ctx, clusterID, chSecretCFSecret)
	s.chRunnerLog(runnerID, "installing the codehold-runner agent")
	res, err := rpc.DockerCodehold(ctx, docker.CodeholdRequest{
		Action: docker.CodeholdInstall, MachineID: r.WorkloadID, Source: src, Arch: "amd64",
		RunnerURL: firstNonEmpty(ctrl.RunnerURL, ctrl.URL), CFID: cfID, CFSecret: cfSecret,
	})
	if err != nil {
		return err
	}
	if res.Version != "" {
		s.chRunnerLog(runnerID, "agent %s installed", res.Version)
	}
	// A token CodeHold already handed out must be handed over again, and the
	// fresh agent already runs the current configuration.
	if cur, _ := s.chRunner(ctx, clusterID, runnerID); cur != nil {
		cur.TokenHash, cur.AppliedSpec = "", cur.Spec
		_ = s.Store.PutCodeholdRunner(ctx, *cur)
	}
	return nil
}

// chPlacement picks the storage pool and network for a runner's container:
// the configured ones, or the ones the create page would choose (the first
// usable pool, and a guest-safe network before the LAN bridge).
func (s *Server) chPlacement(ctx context.Context, clusterID string, st chSettings) (string, string) {
	usable := func(status string) bool { return status == "available" || status == "warning" }
	poolID, netID := st.PoolID, st.NetworkID
	if poolID == "" {
		pools, _ := s.Store.ListStoragePools(ctx, clusterID)
		for _, p := range pools {
			if usable(p.Status) {
				poolID = p.ID
				break
			}
		}
	}
	if netID == "" {
		nets, _ := s.Store.ListNetworks(ctx, clusterID)
		for _, n := range nets {
			if usable(n.Status) && n.Danger != "dangerous" && n.Kind != "lan-bridge" {
				netID = n.ID
				break
			}
		}
		if netID == "" {
			for _, n := range nets {
				if usable(n.Status) {
					netID = n.ID
					break
				}
			}
		}
	}
	return poolID, netID
}

// chSource finds where the runner binary comes from: the local CodeHold
// container when it is there, else the configured HTTPS address.
func (s *Server) chSource(ctx context.Context, clusterID string, st chSettings) (docker.CodeholdSource, error) {
	if st.Source == "url" {
		src := docker.CodeholdSource{URL: st.DownloadURL}
		if st.ChecksumRequired {
			src.SHA256 = strings.ToLower(st.SHA256)
		}
		return src, nil
	}
	for _, in := range s.chDetect(ctx, clusterID, false) {
		if !in.HasRunner {
			continue
		}
		if st.LocalMachineID != "" && in.MachineID != st.LocalMachineID {
			continue
		}
		if st.LocalContainer != "" && in.ContainerName != st.LocalContainer {
			continue
		}
		return docker.CodeholdSource{MachineID: in.MachineID, ContainerID: in.ContainerID}, nil
	}
	return docker.CodeholdSource{}, errors.New("no CodeHold instance with runner downloads was found on this node; start it, or set an HTTPS download address in the runner settings")
}

// chDetect looks for CodeHold server containers on this node and in system
// containers, cached for a minute.
func (s *Server) chDetect(ctx context.Context, clusterID string, fresh bool) []chInstance {
	s.ch.mu.Lock()
	if !fresh && time.Since(s.ch.instancesAt) < time.Minute {
		out := s.ch.instances
		s.ch.mu.Unlock()
		return out
	}
	s.ch.mu.Unlock()
	rpc, ok := s.chRPC()
	if !ok {
		return nil
	}
	dctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	res, err := rpc.DockerCodehold(dctx, docker.CodeholdRequest{Action: docker.CodeholdDetect, Hints: s.dockerHints(dctx, clusterID)})
	if err != nil {
		return nil
	}
	var out []chInstance
	for _, in := range res.Instances {
		ci := chInstance{CodeholdInstance: in}
		if in.HTTPPort > 0 {
			if in.MachineID == docker.HostMachineID {
				ci.URL = "http://127.0.0.1:" + strconv.Itoa(in.HTTPPort)
			} else if ip := s.workloadIPv4(dctx, clusterID, in.MachineID); ip != "" {
				ci.URL = "http://" + ip + ":" + strconv.Itoa(in.HTTPPort)
			}
		}
		out = append(out, ci)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].MachineName < out[j].MachineName })
	s.ch.mu.Lock()
	s.ch.instances, s.ch.instancesAt = out, time.Now()
	s.ch.mu.Unlock()
	return out
}

func (s *Server) chHandOver(ctx context.Context, clusterID string, r appdb.CodeholdRunner, token string) bool {
	rpc, ok := s.chRPC()
	if !ok {
		return false
	}
	s.ch.mu.Lock()
	busy := s.ch.provisioning[r.RunnerID]
	s.ch.mu.Unlock()
	if busy {
		return false
	}
	st, err := rpc.DockerCodehold(ctx, docker.CodeholdRequest{Action: docker.CodeholdStatus, MachineID: r.WorkloadID})
	if err != nil || !st.Installed {
		return false
	}
	if _, err := rpc.DockerCodehold(ctx, docker.CodeholdRequest{Action: docker.CodeholdToken, MachineID: r.WorkloadID, Token: token}); err != nil {
		s.chRunnerLog(r.RunnerID, "could not hand the registration token to the runner: %v", err)
		return false
	}
	s.chRunnerLog(r.RunnerID, "handed a one-time registration token to the runner")
	return true
}

// --- HTTP ----------------------------------------------------------------

func (s *Server) requireCodehold(w http.ResponseWriter, r *http.Request, perm string) (*principal, bool) {
	p, err := s.require(w, r, perm)
	if err != nil {
		return nil, false
	}
	if !s.chEnabled(r.Context(), p.User.ClusterID) {
		writeErr(w, http.StatusNotFound, "CodeHold runners is not enabled. Enable it from Add Features.")
		return nil, false
	}
	return p, true
}

type chRunnerView struct {
	ID           string          `json:"id"`
	Name         string          `json:"name"`
	Desired      string          `json:"desired"`
	State        string          `json:"state"`
	Error        string          `json:"error,omitempty"`
	Busy         int             `json:"busy"`
	Limits       json.RawMessage `json:"limits"`
	WorkloadID   string          `json:"workload_id,omitempty"`
	WorkloadName string          `json:"workload_name,omitempty"`
	WorkloadIP   string          `json:"workload_ip,omitempty"`
	Power        string          `json:"power,omitempty"`
	Settingup    bool            `json:"setting_up"`
	Detached     bool            `json:"detached"`
	DeleteAfter  *time.Time      `json:"delete_after,omitempty"`
	UpdatedAt    time.Time       `json:"updated_at"`
}

func (s *Server) chRunnerViews(ctx context.Context, clusterID string) []chRunnerView {
	runners, _ := s.Store.ListCodeholdRunners(ctx, clusterID)
	out := []chRunnerView{}
	for _, r := range runners {
		v := chRunnerView{ID: r.RunnerID, Name: r.Name, Desired: r.Desired, State: r.State, Error: r.Error, Busy: r.Busy, Limits: r.Limits,
			WorkloadID: r.WorkloadID, Detached: r.Detached, DeleteAfter: r.DeleteAfter, UpdatedAt: r.UpdatedAt}
		if len(v.Limits) == 0 {
			v.Limits = json.RawMessage(`{}`)
		}
		if wl, _ := s.Store.GetWorkload(ctx, clusterID, r.WorkloadID); wl != nil {
			v.WorkloadName, v.Power = wl.Name, wl.Status
			v.WorkloadIP = s.workloadIPv4(ctx, clusterID, wl.ID)
		}
		s.ch.mu.Lock()
		v.Settingup = s.ch.provisioning[r.RunnerID]
		s.ch.mu.Unlock()
		out = append(out, v)
	}
	return out
}

func (s *Server) getCodehold(w http.ResponseWriter, r *http.Request) {
	p, ok := s.requireCodehold(w, r, rbac.ComputeRead)
	if !ok {
		return
	}
	ctx, clusterID := r.Context(), p.User.ClusterID
	ctrl, _ := s.Store.GetCodeholdController(ctx, clusterID)
	st := s.chSettingsOf(ctrl)
	cpus, mem := s.chCapacity(ctx, clusterID, st)
	out := map[string]any{"connected": ctrl != nil, "settings": st, "runners": s.chRunnerViews(ctx, clusterID),
		"capacity": map[string]any{"cpus": cpus, "memory_mb": mem}, "instances": s.chDetect(ctx, clusterID, false)}
	if ctrl != nil {
		s.ch.mu.Lock()
		syncing := s.ch.running
		s.ch.mu.Unlock()
		c := map[string]any{"url": ctrl.URL, "runner_url": firstNonEmpty(ctrl.RunnerURL, ctrl.URL), "controller_id": ctrl.ControllerID, "name": ctrl.Name,
			"status": ctrl.Status, "status_reason": ctrl.StatusReason, "syncing": syncing,
			"cloudflare_access": s.chSecret(ctx, clusterID, chSecretCFID) != "", "created_by": ctrl.CreatedBy}
		if ctrl.LastSyncAt != nil {
			c["last_sync_at"] = ctrl.LastSyncAt.Format(time.RFC3339)
		}
		if ctrl.CredentialExpiresAt != nil {
			c["credential_expires_at"] = ctrl.CredentialExpiresAt.Format(time.RFC3339)
		}
		out["controller"] = c
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) detectCodehold(w http.ResponseWriter, r *http.Request) {
	p, ok := s.requireCodehold(w, r, rbac.ComputeRead)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"instances": s.chDetect(r.Context(), p.User.ClusterID, true)})
}

func (s *Server) connectCodehold(w http.ResponseWriter, r *http.Request) {
	p, ok := s.requireCodehold(w, r, rbac.FeatureManage)
	if !ok {
		return
	}
	var in struct {
		URL       string `json:"url"`
		RunnerURL string `json:"runner_url"`
		Token     string `json:"enrollment_token"`
		Name      string `json:"name"`
		CFID      string `json:"cf_client_id"`
		CFSecret  string `json:"cf_client_secret"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request")
		return
	}
	ctx, clusterID := r.Context(), p.User.ClusterID
	url, err := codehold.CleanURL(in.URL)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	runnerURL := url
	if strings.TrimSpace(in.RunnerURL) != "" {
		if runnerURL, err = codehold.CleanURL(in.RunnerURL); err != nil {
			writeErr(w, http.StatusBadRequest, "runner address: "+err.Error())
			return
		}
	}
	token := strings.TrimSpace(in.Token)
	if !strings.HasPrefix(token, "chce_") || strings.ContainsAny(token, " \n\r\t") {
		writeErr(w, http.StatusBadRequest, "enter the one-time enrollment token from CodeHold (it starts with chce_)")
		return
	}
	if (in.CFID == "") != (in.CFSecret == "") || strings.ContainsAny(in.CFID+in.CFSecret, "\n\r") {
		writeErr(w, http.StatusBadRequest, "enter both the Cloudflare Access client ID and secret, or neither")
		return
	}
	name := strings.TrimSpace(in.Name)
	host := "ndl-ce"
	if node, _ := s.Store.GetNode(ctx, clusterID); node != nil && node.Name != "" {
		host = node.Name
	}
	if name == "" {
		name = host
	}
	cl := &codehold.Client{URL: url, CFID: in.CFID, CFSecret: in.CFSecret, HTTP: s.CodeholdHTTP}
	ectx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	enrolled, err := cl.Enroll(ectx, token, name, host, "ndl-ce "+edition)
	if err != nil {
		s.audit(r, clusterID, p.User.ID, "codehold.connect", "failed", err.Error())
		writeErr(w, http.StatusBadGateway, "enrolling with CodeHold failed: "+err.Error())
		return
	}
	prev, _ := s.Store.GetCodeholdController(ctx, clusterID)
	c := appdb.CodeholdController{ClusterID: clusterID, URL: url, RunnerURL: runnerURL, ControllerID: enrolled.ControllerID,
		Name: firstNonEmpty(enrolled.Name, name), Status: chConnected, CreatedBy: p.User.Username}
	if prev != nil {
		c.Settings = prev.Settings
	}
	for _, kv := range [][2]string{{chSecretToken, enrolled.Token}, {chSecretPrevious, ""}, {chSecretCFID, in.CFID}, {chSecretCFSecret, in.CFSecret}} {
		if err := s.chPutSecret(ctx, clusterID, kv[0], kv[1]); err != nil {
			writeErr(w, http.StatusInternalServerError, "could not save the credential: "+err.Error())
			return
		}
	}
	if err := s.Store.PutCodeholdController(ctx, c); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.audit(r, clusterID, p.User.ID, "codehold.connect", "ok", c.Name+" at "+url)
	s.chWake()
	writeJSON(w, http.StatusOK, map[string]any{"connected": true, "name": c.Name, "controller_id": c.ControllerID})
}

func (s *Server) putCodeholdSettings(w http.ResponseWriter, r *http.Request) {
	p, ok := s.requireCodehold(w, r, rbac.FeatureManage)
	if !ok {
		return
	}
	var in chSettings
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request")
		return
	}
	in.defaults()
	in.SHA256 = strings.ToLower(strings.TrimSpace(in.SHA256))
	in.DownloadURL = strings.TrimSpace(in.DownloadURL)
	if err := in.validate(); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	ctx, clusterID := r.Context(), p.User.ClusterID
	ctrl, _ := s.Store.GetCodeholdController(ctx, clusterID)
	if ctrl == nil {
		writeErr(w, http.StatusConflict, "connect CodeHold first")
		return
	}
	raw, _ := json.Marshal(in)
	ctrl.Settings = raw
	if err := s.Store.PutCodeholdController(ctx, *ctrl); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.audit(r, clusterID, p.User.ID, "codehold.settings", "ok", fmt.Sprintf("%d vCPU, %d MB, %d GB, source %s", in.CPUs, in.MemoryMB, in.DiskGB, in.Source))
	writeJSON(w, http.StatusOK, in)
}

func (s *Server) retryCodeholdRunner(w http.ResponseWriter, r *http.Request) {
	p, ok := s.requireCodehold(w, r, rbac.FeatureManage)
	if !ok {
		return
	}
	id := r.PathValue("id")
	s.ch.mu.Lock()
	delete(s.ch.retryAfter, id)
	s.ch.mu.Unlock()
	cur, _ := s.chRunner(r.Context(), p.User.ClusterID, id)
	if cur == nil {
		writeErr(w, http.StatusNotFound, "runner not found")
		return
	}
	cur.Error = ""
	_ = s.Store.PutCodeholdRunner(r.Context(), *cur)
	s.audit(r, p.User.ClusterID, p.User.ID, "codehold.runner.retry", "ok", cur.Name)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) disconnectCodehold(w http.ResponseWriter, r *http.Request) {
	p, ok := s.requireCodehold(w, r, rbac.FeatureManage)
	if !ok {
		return
	}
	ctx, clusterID := r.Context(), p.User.ClusterID
	runners, _ := s.Store.ListCodeholdRunners(ctx, clusterID)
	for _, rr := range runners {
		if !rr.Detached && rr.WorkloadID != "" {
			writeErr(w, http.StatusConflict, "runners still have containers here; remove them in CodeHold first, or turn the integration off in Add Features and choose what happens to them")
			return
		}
	}
	s.chStop()
	if err := s.Store.DeleteCodeholdController(ctx, clusterID); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	for _, rr := range runners {
		if !rr.Detached {
			_ = s.Store.DeleteCodeholdRunner(ctx, clusterID, rr.RunnerID)
		}
	}
	s.audit(r, clusterID, p.User.ID, "codehold.disconnect", "ok", "")
	writeJSON(w, http.StatusOK, map[string]bool{"disconnected": true})
}

// chRunnerContainers lists runners whose containers still need a decision
// before the integration can be turned off.
func (s *Server) chRunnerContainers(ctx context.Context, clusterID string) []appdb.CodeholdRunner {
	runners, _ := s.Store.ListCodeholdRunners(ctx, clusterID)
	var out []appdb.CodeholdRunner
	for _, r := range runners {
		if r.Detached || r.WorkloadID == "" {
			continue
		}
		if wl, _ := s.Store.GetWorkload(ctx, clusterID, r.WorkloadID); wl != nil {
			out = append(out, r)
		}
	}
	return out
}

// disableCodehold turns the integration off after deciding, runner by
// runner, whether its container is stopped and kept or deleted. Force stops
// every container and deletes them after a grace period instead.
func (s *Server) disableCodehold(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.FeatureManage)
	if err != nil {
		return
	}
	var in struct {
		Decisions map[string]string `json:"decisions"`
		Force     bool              `json:"force"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request")
		return
	}
	ctx, clusterID := r.Context(), p.User.ClusterID
	pending := s.chRunnerContainers(ctx, clusterID)
	if !in.Force {
		var missing []string
		for _, rr := range pending {
			if d := in.Decisions[rr.RunnerID]; d != "stop" && d != "delete" {
				missing = append(missing, rr.Name)
			}
		}
		if len(missing) > 0 {
			writeJSON(w, http.StatusConflict, map[string]any{"error": "choose what happens to every runner first: " + strings.Join(missing, ", "), "code": "codehold_runners"})
			return
		}
	}
	// Stop syncing first, so CodeHold does not restart what is being stopped.
	s.chStop()
	type result struct {
		Runner string `json:"runner"`
		Action string `json:"action"`
		OK     bool   `json:"ok"`
		Error  string `json:"error,omitempty"`
	}
	var results []result
	failed := false
	until := s.now().Add(chGracePeriod)
	for _, rr := range pending {
		action := in.Decisions[rr.RunnerID]
		if in.Force {
			action = "force"
		}
		res := result{Runner: rr.Name, Action: action, OK: true}
		switch action {
		case "delete":
			if err := s.chDeleteWorkload(ctx, clusterID, rr.WorkloadID); err != nil {
				res.OK, res.Error = false, err.Error()
			} else {
				_ = s.Store.DeleteCodeholdRunner(ctx, clusterID, rr.RunnerID)
			}
		default: // stop, force
			if wl, _ := s.Store.GetWorkload(ctx, clusterID, rr.WorkloadID); wl != nil && wl.Status == "running" {
				if code, body := s.systemCall(ctx, clusterID, http.MethodPost, "/api/v1/workloads/"+wl.ID+"/stop", nil); code >= 300 {
					res.OK, res.Error = false, apiErrText(code, body)
				}
			}
			if res.OK || in.Force {
				rr.Detached = true
				if action == "force" {
					rr.DeleteAfter = &until
				}
				_ = s.Store.PutCodeholdRunner(ctx, rr)
			}
		}
		if !res.OK && !in.Force {
			failed = true
		}
		results = append(results, res)
	}
	if failed {
		s.audit(r, clusterID, p.User.ID, "codehold.disable", "failed", "some runners could not be stopped or deleted")
		s.chWake()
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "some runners could not be stopped or deleted, so the integration stays on", "results": results})
		return
	}
	if err := s.setFeatureEnabled(ctx, clusterID, features.IDCodehold, false, "turned off; runner containers were stopped or deleted as chosen"); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	detail := fmt.Sprintf("%d runner container(s)", len(pending))
	if in.Force {
		detail += " stopped; deleted after " + until.Format("2006-01-02")
	}
	s.audit(r, clusterID, p.User.ID, "codehold.disable", "ok", detail)
	writeJSON(w, http.StatusOK, map[string]any{"disabled": true, "results": results})
}

// getCodeholdPending lists runner containers waiting out a force-disable's
// grace period; the dashboard warns about them.
func (s *Server) getCodeholdPending(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.ComputeRead)
	if err != nil {
		return
	}
	var out []chRunnerView
	for _, v := range s.chRunnerViews(r.Context(), p.User.ClusterID) {
		if v.Detached && v.DeleteAfter != nil {
			out = append(out, v)
		}
	}
	if out == nil {
		out = []chRunnerView{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": out})
}

// actOnCodeholdPending keeps (cancels the deletion of) or deletes now the
// containers of force-disabled runners.
func (s *Server) actOnCodeholdPending(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.FeatureManage)
	if err != nil {
		return
	}
	var in struct {
		Action string   `json:"action"`
		IDs    []string `json:"ids"`
	}
	if err := readJSON(r, &in); err != nil || (in.Action != "keep" && in.Action != "delete") {
		writeErr(w, http.StatusBadRequest, "action must be keep or delete")
		return
	}
	ctx, clusterID := r.Context(), p.User.ClusterID
	only := map[string]bool{}
	for _, id := range in.IDs {
		only[id] = true
	}
	runners, _ := s.Store.ListCodeholdRunners(ctx, clusterID)
	done := 0
	var errs []string
	for _, rr := range runners {
		if !rr.Detached || rr.DeleteAfter == nil || (len(only) > 0 && !only[rr.RunnerID]) {
			continue
		}
		if in.Action == "keep" {
			rr.DeleteAfter = nil
			_ = s.Store.PutCodeholdRunner(ctx, rr)
			done++
			continue
		}
		if err := s.chDeleteWorkload(ctx, clusterID, rr.WorkloadID); err != nil {
			errs = append(errs, rr.Name+": "+err.Error())
			continue
		}
		_ = s.Store.DeleteCodeholdRunner(ctx, clusterID, rr.RunnerID)
		done++
	}
	s.audit(r, clusterID, p.User.ID, "codehold.pending."+in.Action, "ok", fmt.Sprintf("%d runner container(s)", done))
	if len(errs) > 0 {
		writeErr(w, http.StatusUnprocessableEntity, strings.Join(errs, "; "))
		return
	}
	writeJSON(w, http.StatusOK, map[string]int{"done": done})
}
