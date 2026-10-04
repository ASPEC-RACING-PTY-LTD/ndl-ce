// Package diskguard protects the host filesystems No-dal and PostgreSQL
// live on. It watches free space, refuses bulk writes (backups, staging,
// conversions, uploads, installs) once a filesystem is critically full,
// and keeps an emergency reserve file it releases automatically when the
// disk is nearly full, so PostgreSQL can keep writing.
//
// The guard never deletes user data. The only file it removes on its own
// is its own reserve file.
package diskguard

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Level is how full a filesystem is.
type Level string

const (
	LevelOK        Level = "ok"
	LevelWarning   Level = "warning"
	LevelCritical  Level = "critical"
	LevelEmergency Level = "emergency"
)

func (l Level) rank() int {
	switch l {
	case LevelWarning:
		return 1
	case LevelCritical:
		return 2
	case LevelEmergency:
		return 3
	}
	return 0
}

// AtLeast reports whether l is at least as severe as other.
func (l Level) AtLeast(other Level) bool { return l.rank() >= other.rank() }

const gib = int64(1) << 30

// Policy sets when a filesystem counts as warning, critical or emergency.
// Each level is a free-space threshold: its percentage of the disk,
// clamped between a floor and a cap in bytes. Small disks are judged by
// the floors and very large disks by the caps, so a 4 TB disk is not in
// an emergency with 30 GiB free and a 20 GiB disk is warned early.
type Policy struct {
	WarnPercent        float64 `json:"warn_percent"`
	CriticalPercent    float64 `json:"critical_percent"`
	EmergencyPercent   float64 `json:"emergency_percent"`
	WarnFreeBytes      int64   `json:"warn_free_bytes"`
	CriticalFreeBytes  int64   `json:"critical_free_bytes"`
	EmergencyFreeBytes int64   `json:"emergency_free_bytes"`
	WarnMaxBytes       int64   `json:"warn_max_bytes"`
	CriticalMaxBytes   int64   `json:"critical_max_bytes"`
	EmergencyMaxBytes  int64   `json:"emergency_max_bytes"`
	// ReserveBytes is the emergency reserve file size. 0 disables it.
	ReserveBytes int64 `json:"reserve_bytes"`
}

// DefaultPolicy: warn at 85% used, stop bulk writes at 92%, emergency at
// 97%, each clamped to sensible byte bounds, with a 4 GiB reserve kept
// for PostgreSQL.
func DefaultPolicy() Policy {
	return Policy{
		WarnPercent: 85, CriticalPercent: 92, EmergencyPercent: 97,
		WarnFreeBytes: 10 * gib, CriticalFreeBytes: 5 * gib, EmergencyFreeBytes: 2 * gib,
		WarnMaxBytes: 200 * gib, CriticalMaxBytes: 75 * gib, EmergencyMaxBytes: 20 * gib,
		ReserveBytes: 4 * gib,
	}
}

// threshold is the free bytes below which a level applies.
func threshold(total int64, percent float64, floor, ceiling int64) int64 {
	t := int64(float64(total) * (100 - percent) / 100)
	if ceiling > 0 && t > ceiling {
		t = ceiling
	}
	if t < floor {
		t = floor
	}
	return t
}

// Thresholds returns the free-space thresholds for a disk of total bytes.
func (p Policy) Thresholds(total int64) (warn, critical, emergency int64) {
	return threshold(total, p.WarnPercent, p.WarnFreeBytes, p.WarnMaxBytes),
		threshold(total, p.CriticalPercent, p.CriticalFreeBytes, p.CriticalMaxBytes),
		threshold(total, p.EmergencyPercent, p.EmergencyFreeBytes, p.EmergencyMaxBytes)
}

// Evaluate returns the level for a filesystem of total bytes with avail
// bytes free to unprivileged writers.
func (p Policy) Evaluate(total, avail int64) Level {
	if total <= 0 {
		return LevelOK
	}
	if avail < 0 {
		avail = 0
	}
	warn, critical, emergency := p.Thresholds(total)
	switch {
	case avail < emergency:
		return LevelEmergency
	case avail < critical:
		return LevelCritical
	case avail < warn:
		return LevelWarning
	}
	return LevelOK
}

// FS is one watched filesystem.
type FS struct {
	Device      uint64   `json:"-"`
	Mount       string   `json:"mount"`
	Paths       []string `json:"paths"`
	Roles       []string `json:"roles"`
	TotalBytes  int64    `json:"total_bytes"`
	FreeBytes   int64    `json:"free_bytes"`
	UsedPercent float64  `json:"used_percent"`
	Level       Level    `json:"level"`
	// Free-space thresholds for this disk.
	WarnBelowBytes      int64 `json:"warn_below_bytes"`
	CriticalBelowBytes  int64 `json:"critical_below_bytes"`
	EmergencyBelowBytes int64 `json:"emergency_below_bytes"`
}

// Status is the guard's view of the host.
type Status struct {
	Level        Level  `json:"level"`
	Filesystems  []FS   `json:"filesystems"`
	Policy       Policy `json:"policy"`
	ReservePath  string `json:"reserve_path"`
	ReserveBytes int64  `json:"reserve_bytes"`
	ReserveHeld  bool   `json:"reserve_held"`
	ReserveNote  string `json:"reserve_note,omitempty"`
	// ReserveReleasedAt is when the reserve was last released, by the
	// guard in an emergency or by an operator.
	ReserveReleasedAt *time.Time `json:"reserve_released_at,omitempty"`
	CheckedAt         time.Time  `json:"checked_at"`
}

// Watch is a path the guard protects and why.
type Watch struct {
	Path string
	Role string
}

// statInfo is what the guard needs from statfs and stat.
type statInfo struct {
	device       uint64
	total, avail int64
}

// Guard watches filesystems and gates bulk writes. It is safe for
// concurrent use.
type Guard struct {
	Watches     []Watch
	Policy      Policy
	ReservePath string

	// stat and allocate are replaced in tests.
	stat     func(path string) (statInfo, error)
	allocate func(path string, size int64) error

	mu     sync.Mutex
	status Status
	// refreshMu serializes refreshes, which manage the reserve file.
	refreshMu sync.Mutex
	now       func() time.Time
	// heldOffUntil stops the reserve being recreated after an operator
	// released it, so the space stays available while they recover.
	heldOffUntil time.Time
	releasedAt   time.Time
}

// ReleaseHoldOff is how long a manually released reserve stays released.
const ReleaseHoldOff = 6 * time.Hour

// New returns a guard over the given paths with the default policy.
func New(reservePath string, watches ...Watch) *Guard {
	return &Guard{Watches: watches, Policy: DefaultPolicy(), ReservePath: reservePath}
}

func (g *Guard) statFn() func(string) (statInfo, error) {
	if g.stat != nil {
		return g.stat
	}
	return statPath
}

func (g *Guard) clock() time.Time {
	if g.now != nil {
		return g.now()
	}
	return time.Now().UTC()
}

// nearestExisting walks up to the closest path that exists, so a
// destination that is about to be created is judged by its filesystem.
func nearestExisting(p string) string {
	p = filepath.Clean(p)
	for {
		if _, err := os.Stat(p); err == nil {
			return p
		}
		parent := filepath.Dir(p)
		if parent == p {
			return p
		}
		p = parent
	}
}

// Refresh re-reads every watched filesystem and manages the reserve file.
func (g *Guard) Refresh() Status {
	g.refreshMu.Lock()
	defer g.refreshMu.Unlock()
	stat := g.statFn()
	byDev := map[uint64]*FS{}
	var order []uint64
	for _, w := range g.Watches {
		info, err := stat(nearestExisting(w.Path))
		if err != nil {
			continue
		}
		fs, ok := byDev[info.device]
		if !ok {
			fs = &FS{Device: info.device, Mount: w.Path, TotalBytes: info.total, FreeBytes: info.avail}
			if info.total > 0 {
				fs.UsedPercent = 100 * float64(info.total-info.avail) / float64(info.total)
			}
			fs.Level = g.Policy.Evaluate(info.total, info.avail)
			fs.WarnBelowBytes, fs.CriticalBelowBytes, fs.EmergencyBelowBytes = g.Policy.Thresholds(info.total)
			byDev[info.device] = fs
			order = append(order, info.device)
		}
		fs.Paths = append(fs.Paths, w.Path)
		fs.Roles = append(fs.Roles, w.Role)
	}
	st := Status{Level: LevelOK, Policy: g.Policy, ReservePath: g.ReservePath, ReserveBytes: g.Policy.ReserveBytes, CheckedAt: g.clock()}
	for _, dev := range order {
		fs := *byDev[dev]
		if fs.Level.rank() > st.Level.rank() {
			st.Level = fs.Level
		}
		st.Filesystems = append(st.Filesystems, fs)
	}
	sort.SliceStable(st.Filesystems, func(i, j int) bool { return st.Filesystems[i].Level.rank() > st.Filesystems[j].Level.rank() })
	g.manageReserve(&st)
	g.mu.Lock()
	g.status = st
	g.mu.Unlock()
	return st
}

// Status returns the last refreshed status, refreshing once if empty.
func (g *Guard) Status() Status {
	g.mu.Lock()
	st := g.status
	g.mu.Unlock()
	if st.CheckedAt.IsZero() {
		return g.Refresh()
	}
	return st
}

func (g *Guard) reserveFS(st *Status) *FS {
	if g.ReservePath == "" {
		return nil
	}
	info, err := g.statFn()(nearestExisting(g.ReservePath))
	if err != nil {
		return nil
	}
	for i := range st.Filesystems {
		if st.Filesystems[i].Device == info.device {
			return &st.Filesystems[i]
		}
	}
	return nil
}

// manageReserve releases the reserve file in an emergency and recreates it
// once the filesystem has room for it and still stays below warning.
func (g *Guard) manageReserve(st *Status) {
	if g.ReservePath == "" || g.Policy.ReserveBytes <= 0 {
		return
	}
	held := false
	if fi, err := os.Stat(g.ReservePath); err == nil && fi.Mode().IsRegular() {
		held = true
	}
	fs := g.reserveFS(st)
	switch {
	case fs == nil:
	case held && fs.Level == LevelEmergency:
		if err := os.Remove(g.ReservePath); err == nil {
			held = false
			g.releasedAt = g.clock()
			st.ReserveNote = fmt.Sprintf("Released the %s emergency reserve because %s is nearly full.", humanBytes(g.Policy.ReserveBytes), fs.Mount)
		}
	case !held && g.clock().Before(g.heldOffUntil):
		st.ReserveNote = "The emergency reserve was released by an operator and is recreated after " + g.heldOffUntil.Format(time.RFC3339) + "."
	case !held && fs.Level == LevelOK && g.Policy.Evaluate(fs.TotalBytes, fs.FreeBytes-g.Policy.ReserveBytes) == LevelOK:
		if err := g.createReserve(); err == nil {
			held = true
		} else if !errors.Is(err, errReserveUnsupported) {
			st.ReserveNote = "The emergency reserve could not be created: " + err.Error()
		} else {
			st.ReserveNote = "This filesystem cannot preallocate space, so no emergency reserve is kept."
		}
	}
	st.ReserveHeld = held
	if !g.releasedAt.IsZero() {
		at := g.releasedAt
		st.ReserveReleasedAt = &at
	}
}

var errReserveUnsupported = errors.New("preallocation is not supported")

func (g *Guard) createReserve() error {
	if err := os.MkdirAll(filepath.Dir(g.ReservePath), 0o700); err != nil {
		return err
	}
	tmp := g.ReservePath + ".tmp"
	alloc := g.allocate
	if alloc == nil {
		alloc = preallocate
	}
	if err := alloc(tmp, g.Policy.ReserveBytes); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, g.ReservePath)
}

// ReleaseReserve deletes the reserve file now, for an operator who needs
// the space to recover.
func (g *Guard) ReleaseReserve() (Status, error) {
	if g.ReservePath == "" {
		return g.Refresh(), fmt.Errorf("no emergency reserve is configured")
	}
	if err := os.Remove(g.ReservePath); err != nil && !os.IsNotExist(err) {
		return g.Refresh(), err
	}
	g.refreshMu.Lock()
	g.heldOffUntil = g.clock().Add(ReleaseHoldOff)
	g.releasedAt = g.clock()
	g.refreshMu.Unlock()
	st := g.Refresh()
	return st, nil
}

// ErrDiskFull is returned for refused writes.
type ErrDiskFull struct {
	Op    string
	FS    FS
	Level Level
}

func (e ErrDiskFull) Error() string {
	return fmt.Sprintf("%s was stopped to protect the host: %s is %.0f%% full with %s free (%s). Free space under Storage, Host disk, then try again.",
		e.Op, e.FS.Mount, e.FS.UsedPercent, humanBytes(e.FS.FreeBytes), e.Level)
}

// Allow refuses a bulk write of op when the filesystem it writes to is
// critical or worse. An empty dest checks every watched filesystem.
// needBytes, when known, is also checked against the critical floor.
func (g *Guard) Allow(op, dest string, needBytes int64) error {
	if g == nil {
		return nil
	}
	st := g.Refresh()
	check := func(fs FS) error {
		level := fs.Level
		if needBytes > 0 {
			after := g.Policy.Evaluate(fs.TotalBytes, fs.FreeBytes-needBytes)
			if after.rank() > level.rank() {
				level = after
			}
		}
		if level.AtLeast(LevelCritical) {
			return ErrDiskFull{Op: op, FS: fs, Level: level}
		}
		return nil
	}
	if strings.TrimSpace(dest) != "" {
		info, err := g.statFn()(nearestExisting(dest))
		if err != nil {
			return nil
		}
		for _, fs := range st.Filesystems {
			if fs.Device == info.device {
				return check(fs)
			}
		}
		// Not a watched filesystem (a separate pool disk): not ours to gate.
		return nil
	}
	for _, fs := range st.Filesystems {
		if err := check(fs); err != nil {
			return err
		}
	}
	return nil
}

func humanBytes(n int64) string {
	switch {
	case n >= 1<<40:
		return fmt.Sprintf("%.1f TiB", float64(n)/float64(1<<40))
	case n >= gib:
		return fmt.Sprintf("%.1f GiB", float64(n)/float64(gib))
	case n >= 1<<20:
		return fmt.Sprintf("%.0f MiB", float64(n)/float64(1<<20))
	}
	return fmt.Sprintf("%d B", n)
}
