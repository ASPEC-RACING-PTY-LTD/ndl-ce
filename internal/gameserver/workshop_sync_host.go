package gameserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sync"
	"time"
)

// Live Workshop sync runs in the agent, next to the server folders and
// Docker: the control plane only says what its database knows (whether the
// server runs, its container and collection) and serves the panel. The
// agent keeps the state in its own folder, outside every server's.

// Sync operations.
const (
	SyncOpGet    = "get"    // the state
	SyncOpSet    = "set"    // turn sync on or off
	SyncOpCheck  = "check"  // queue a check, like a request from the server
	SyncOpTick   = "tick"   // do what is due now, in the background
	SyncOpRemove = "remove" // forget a deleted server
)

// ErrSyncBusy is returned while a check runs for the server.
var ErrSyncBusy = errors.New("a check is running now; try again when it finishes")

// SyncCall is one sync operation for one server.
type SyncCall struct {
	Op          string `json:"op"`
	Enabled     bool   `json:"enabled,omitempty"`
	Running     bool   `json:"running,omitempty"`
	ContainerID string `json:"container_id,omitempty"`
	WorkDir     string `json:"work_dir,omitempty"`
	Collection  string `json:"collection,omitempty"`
	// RequestID names a check queued from the panel.
	RequestID string `json:"request_id,omitempty"`
}

// SyncReply is the server's sync state after the operation.
type SyncReply struct {
	State    SyncState `json:"state"`
	Checking bool      `json:"checking"`
}

var syncCollectionID = regexp.MustCompile(`^[0-9]{1,20}$`)

// syncRunner holds per-server locks and the background work.
type syncRunner struct {
	mu    sync.Mutex
	locks map[string]*sync.Mutex
	busy  map[string]bool
	retry map[string]time.Time
}

func (r *syncRunner) lock(id string) *sync.Mutex {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.locks == nil {
		r.locks = map[string]*sync.Mutex{}
	}
	if r.locks[id] == nil {
		r.locks[id] = &sync.Mutex{}
	}
	return r.locks[id]
}

func (r *syncRunner) start(id string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.busy == nil {
		r.busy = map[string]bool{}
	}
	if r.busy[id] {
		return false
	}
	r.busy[id] = true
	return true
}

func (r *syncRunner) done(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.busy, id)
}

func (r *syncRunner) checking(id string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.busy[id]
}

func (h *LocalHost) syncRoot() string { return filepath.Join(h.RT.Root, "workshop-sync") }

// SyncItemsDir is where the agent keeps a server's packs. It is mounted
// read-only into the server, and is never taken from the control plane.
func (h *LocalHost) SyncItemsDir(id string) string {
	return filepath.Join(h.syncRoot(), id, "items")
}

func (h *LocalHost) syncStatePath(id string) string {
	return filepath.Join(h.syncRoot(), id, "state.json")
}

func (h *LocalHost) loadSyncState(id string) SyncState {
	var st SyncState
	if raw, err := os.ReadFile(h.syncStatePath(id)); err == nil {
		_ = json.Unmarshal(raw, &st)
	}
	return st
}

func (h *LocalHost) saveSyncState(id string, st SyncState) error {
	p := h.syncStatePath(id)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	raw, _ := json.MarshalIndent(st, "", "  ")
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

func (h *LocalHost) syncNow() time.Time {
	if h.SyncNow != nil {
		return h.SyncNow()
	}
	return time.Now()
}

func (h *LocalHost) syncEngine(id string) WorkshopSync {
	api := h.SyncAPI
	if api == nil {
		api = SteamWorkshopAPI{}
	}
	fetch := h.SyncFetch
	if fetch == nil {
		fetch = h.RT.WorkshopDownload
	}
	return WorkshopSync{API: api, Fetch: fetch, Now: h.syncNow, Work: filepath.Join(h.syncRoot(), id, "work"), Items: h.SyncItemsDir(id)}
}

func (h *LocalHost) syncReply(id string) SyncReply {
	return SyncReply{State: h.loadSyncState(id), Checking: h.sync.checking(id)}
}

// WorkshopSync runs one live Workshop sync operation for server id.
func (h *LocalHost) WorkshopSync(ctx context.Context, id string, call SyncCall) (SyncReply, error) {
	if !ValidHostID(id) {
		return SyncReply{}, fmt.Errorf("game server id is invalid")
	}
	if call.Collection != "" && !syncCollectionID.MatchString(call.Collection) {
		return SyncReply{}, fmt.Errorf("workshop collection id is invalid")
	}
	if call.WorkDir != "" && (!path.IsAbs(call.WorkDir) || path.Clean(call.WorkDir) != call.WorkDir) {
		return SyncReply{}, fmt.Errorf("working folder is invalid")
	}
	switch call.Op {
	case SyncOpGet:
		return h.syncReply(id), nil
	case SyncOpSet, SyncOpCheck:
		mu := h.sync.lock(id)
		if !mu.TryLock() {
			return h.syncReply(id), ErrSyncBusy
		}
		st := h.loadSyncState(id)
		if call.Op == SyncOpSet {
			if call.Enabled && !st.Enabled {
				// What the server runs now is the starting point.
				st.ContainerID = ""
			}
			st.Enabled = call.Enabled
		} else {
			if !st.Enabled {
				mu.Unlock()
				return h.syncReply(id), fmt.Errorf("turn live Workshop sync on first")
			}
			if !(SyncRequest{Version: 1, ID: call.RequestID}).Valid() {
				mu.Unlock()
				return h.syncReply(id), fmt.Errorf("request id is invalid")
			}
			if st.Pending == "" {
				st.Pending = call.RequestID
			}
		}
		err := h.saveSyncState(id, st)
		mu.Unlock()
		if err != nil {
			return h.syncReply(id), fmt.Errorf("saving the sync state: %w", err)
		}
		return h.syncReply(id), nil
	case SyncOpTick:
		if !h.sync.start(id) {
			return h.syncReply(id), nil
		}
		if h.SyncInline {
			h.syncRun(ctx, id, call)
			h.sync.done(id)
			return h.syncReply(id), nil
		}
		go func() {
			defer h.sync.done(id)
			h.syncRun(context.WithoutCancel(ctx), id, call)
		}()
		return h.syncReply(id), nil
	case SyncOpRemove:
		mu := h.sync.lock(id)
		mu.Lock()
		defer mu.Unlock()
		return SyncReply{}, os.RemoveAll(filepath.Join(h.syncRoot(), id))
	}
	return SyncReply{}, fmt.Errorf("unknown sync operation %q", call.Op)
}

// syncRun does what one server needs now: a new baseline after a restart,
// picking up a request, and a check when one is due.
func (h *LocalHost) syncRun(ctx context.Context, id string, call SyncCall) {
	mu := h.sync.lock(id)
	mu.Lock()
	defer mu.Unlock()
	st := h.loadSyncState(id)
	if !st.Enabled || !call.Running || call.ContainerID == "" {
		return
	}
	now := h.syncNow()
	h.sync.mu.Lock()
	wait := h.sync.retry[id]
	h.sync.mu.Unlock()
	if now.Before(wait) {
		return
	}
	fail := func(err error) {
		st.LastError = err.Error()
		_ = h.saveSyncState(id, st)
		h.sync.mu.Lock()
		if h.sync.retry == nil {
			h.sync.retry = map[string]time.Time{}
		}
		h.sync.retry[id] = now.Add(time.Minute)
		h.sync.mu.Unlock()
	}
	if call.Collection == "" {
		fail(errors.New("the server has no Workshop collection set"))
		return
	}
	if !h.RT.HasReadOnlyMount(ctx, id, call.WorkDir, SyncItemsRel) {
		fail(errors.New("restart the server once: it was started before ndl-ce could attach its read-only packs folder"))
		return
	}
	dir, err := OpenSyncDir(h.RT.DataDir(id), SyncDirRel)
	if err != nil {
		fail(fmt.Errorf("opening %s: %w", SyncDirRel, err))
		return
	}
	defer dir.Close()
	engine := h.syncEngine(id)
	cctx, cancel := context.WithTimeout(ctx, 45*time.Minute)
	defer cancel()

	if st.ContainerID != call.ContainerID {
		// The server (re)started: what it loaded is the collection as it is now.
		if err := engine.Baseline(cctx, &st, call.Collection); err != nil {
			fail(fmt.Errorf("reading the collection: %w", err))
			return
		}
		st.ContainerID = call.ContainerID
		st.LastError = ""
		_ = h.saveSyncState(id, st)
	}
	if req, _, err := ReadSyncRequest(dir); err == nil && req.Valid() && !st.Seen(req.ID) {
		st.Pending = req.ID
		_ = h.saveSyncState(id, st)
	}
	last := time.Unix(st.LastCheck, 0)
	due := (st.Pending != "" && now.Sub(last) >= SyncMinInterval) || now.Sub(last) >= SyncCheckEvery
	if !due {
		return
	}
	reqID := st.Pending
	m, err := engine.Check(cctx, &st, dir, call.Collection, reqID)
	if err != nil {
		fail(err)
		return
	}
	if reqID != "" {
		st.Handle(reqID)
	}
	_ = h.saveSyncState(id, st)
	if err := h.RT.Send(ctx, id, fmt.Sprintf("%s %d", SyncReadyCommand, m.Seq)); err != nil {
		log.Printf("workshop sync: %s: telling the server about manifest %d: %v", id, m.Seq, err)
	}
	if len(m.Items) > 0 || len(m.Errors) > 0 {
		log.Printf("workshop sync: %s: manifest %d, %d item(s) ready, %d error(s)", id, m.Seq, len(m.Items), len(m.Errors))
	}
}
