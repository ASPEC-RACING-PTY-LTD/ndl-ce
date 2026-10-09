// Package diskguard protects the host filesystems No-dal and PostgreSQL
// live on. It watches free space, refuses bulk writes (backups, staging,
// conversions, uploads, installs) once a filesystem is critically full,
// and keeps space permanently reserved for No-dal: a preallocated reserve
// file (50 GiB by default) that nothing else can use. When the disk is
// nearly full anyway, the reserve is released automatically, so the
// control plane, the agent and PostgreSQL keep running and the operator can
// clean up from the UI instead of over SSH.
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
	// ReserveBytes is the space permanently reserved for No-dal on the
	// filesystem that holds its state. 0 disables it.
	ReserveBytes int64 `json:"reserve_bytes"`
	// ReserveMaxPercent caps the reserve on small disks.
	ReserveMaxPercent float64 `json:"reserve_max_percent"`
}

// DefaultPolicy: warn at 85% used, stop bulk writes at 92%, emergency at
// 97%, each clamped to sensible byte bounds, plus 50 GiB reserved for
// No-dal (at most 10% of a small disk). The reserve is the main safety
// margin, so the critical and emergency caps stay small.
func DefaultPolicy() Policy {
	return Policy{
		WarnPercent: 85, CriticalPercent: 92, EmergencyPercent: 97,
		WarnFreeBytes: 10 * gib, CriticalFreeBytes: 5 * gib, EmergencyFreeBytes: 2 * gib,
		WarnMaxBytes: 200 * gib, CriticalMaxBytes: 30 * gib, EmergencyMaxBytes: 10 * gib,
		ReserveBytes: 50 * gib, ReserveMaxPercent: 10,
	}
}

// ReserveTarget is the reserve kept on a disk of total bytes.
func (p Policy) ReserveTarget(total int64) int64 {
	target := p.ReserveBytes
	if p.ReserveMaxPercent > 0 && total > 0 {
		if limit := int64(float64(total) * p.ReserveMaxPercent / 100); limit < target {
			target = limit
		}
	}
	if target < 0 {
		return 0
	}
	return target
}

// reserveStep is the smallest amount the reserve grows by, and the margin
// it always leaves above the critical threshold.
const reserveStep = gib

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
	Policy      Policy `json:"policy"`
	ReservePath string `json:"reserve_path"`
	// ReserveBytes is how much is reserved right now.
	ReserveBytes int64 `json:"reserve_bytes"`
	// ReserveTargetBytes is how much should be reserved.
	ReserveTargetBytes int64  `json:"reserve_target_bytes"`
	ReserveHeld        bool   `json:"reserve_held"`
	ReserveNote        string `json:"reserve_note,omitempty"`
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
	// autoReleased is set when the guard released the reserve in an
	// emergency. It is taken again once the disk is healthy.
	autoReleased bool
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

// manageReserve keeps the reserve at its target. It grows the reserve
// whenever the disk has room for it above the critical threshold (in steps,
// so a fuller disk holds part of it until space frees up), releases it all
// in an emergency, and after an emergency takes it again only once the disk
// is healthy, so it does not immediately fill the space it just freed.
func (g *Guard) manageReserve(st *Status) {
	if g.ReservePath == "" || g.Policy.ReserveBytes <= 0 {
		return
	}
	size := reserveSize(g.ReservePath)
	fs := g.reserveFS(st)
	now := g.clock()
	if fs != nil {
		target := g.Policy.ReserveTarget(fs.TotalBytes)
		st.ReserveTargetBytes = target
		switch {
		case size > 0 && fs.Level == LevelEmergency:
			if err := os.Remove(g.ReservePath); err == nil {
				st.ReserveNote = fmt.Sprintf("Released the %s reserve because %s is nearly full. Free up space; it is reserved again once the disk is healthy.", humanBytes(size), fs.Mount)
				g.adjustFree(fs, size)
				size = 0
				g.releasedAt = now
				g.autoReleased = true
			}
		case size >= target:
		case now.Before(g.heldOffUntil):
			st.ReserveNote = "The reserve was released by an operator and is taken again after " + g.heldOffUntil.Format(time.RFC3339) + "."
		case g.autoReleased && fs.Level != LevelOK:
			st.ReserveNote = "The reserve was released because the disk filled up. It is reserved again once the disk is healthy."
		default:
			_, critical, _ := g.Policy.Thresholds(fs.TotalBytes)
			want := target
			if room := fs.FreeBytes - critical - reserveStep; size+room < want {
				want = size + room
			}
			if want-size >= reserveStep || (want == target && want > size) {
				switch err := g.growReserve(want); {
				case err == nil:
					g.adjustFree(fs, size-want)
					size = want
					g.autoReleased = false
				case errors.Is(err, errReserveUnsupported):
					st.ReserveNote = "This filesystem cannot preallocate space, so no reserve is kept."
				default:
					st.ReserveNote = "The reserve could not be extended: " + err.Error()
				}
			}
			if size < target && st.ReserveNote == "" {
				st.ReserveNote = fmt.Sprintf("Holding %s of the %s reserve. It grows as space frees up.", humanBytes(size), humanBytes(target))
			}
		}
	}
	st.ReserveHeld = size > 0
	st.ReserveBytes = size
	if !g.releasedAt.IsZero() {
		at := g.releasedAt
		st.ReserveReleasedAt = &at
	}
}

// adjustFree moves delta bytes into (positive) or out of a filesystem's
// free space after the reserve changed, and re-judges it.
func (g *Guard) adjustFree(fs *FS, delta int64) {
	fs.FreeBytes += delta
	if fs.TotalBytes > 0 {
		fs.UsedPercent = 100 * float64(fs.TotalBytes-fs.FreeBytes) / float64(fs.TotalBytes)
	}
	fs.Level = g.Policy.Evaluate(fs.TotalBytes, fs.FreeBytes)
}

func reserveSize(path string) int64 {
	fi, err := os.Stat(path)
	if err != nil || !fi.Mode().IsRegular() {
		return 0
	}
	return fi.Size()
}

var errReserveUnsupported = errors.New("preallocation is not supported")

// growReserve makes the reserve file hold size bytes. Existing blocks are
// kept, so growing never needs the space twice.
func (g *Guard) growReserve(size int64) error {
	if err := os.MkdirAll(filepath.Dir(g.ReservePath), 0o700); err != nil {
		return err
	}
	alloc := g.allocate
	if alloc == nil {
		alloc = preallocate
	}
	return alloc(g.ReservePath, size)
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
	g.autoReleased = false
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
