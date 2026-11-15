package gameserver

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

// Live Workshop sync delivers new and updated Workshop items to a running
// Garry's Mod server between restarts. ndl-ce checks the server's Workshop
// collection, downloads what changed with SteamCMD and hands it over in
// garrysmod/data/cs_sync/; an addon on the server mounts it. The contract is
// docs/workshop-live-sync.md.

const (
	SyncDirRel       = "garrysmod/data/cs_sync"
	SyncRequestFile  = "request.json"
	SyncManifestFile = "manifest.json"
	SyncStatusFile   = "status.json"
	SyncItemsDir     = "items"
	// SyncItemsRel is where the server sees its packs, read-only.
	SyncItemsRel = SyncDirRel + "/" + SyncItemsDir
	// SyncReadyCommand tells the server a new manifest is ready.
	SyncReadyCommand = "cs_sync_ready"
	SyncCheckEvery   = time.Hour
	// SyncMinInterval only stops repeated presses from running back-to-back
	// checks; a request is otherwise answered within seconds.
	SyncMinInterval  = 10 * time.Second
	syncMaxItemBytes = 4 << 30
	syncMaxJSONBytes = 1 << 20
	gmodWorkshopApp  = "4000"
)

// syncAlwaysBlocked are Workshop items never delivered: known backdoors.
var syncAlwaysBlocked = []string{"1485877786"}

var syncRequestIDRe = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,80}$`)

// WorkshopSyncSupported reports whether a template's servers can use live
// sync: it delivers Garry's Mod packs.
func WorkshopSyncSupported(t Template) bool {
	return t.Game == "gmod" && t.Content.Provider == "workshop"
}

// SyncState is ndl-ce's own record for one server. It lives outside the
// server's folder, where the server cannot change it.
type SyncState struct {
	Enabled     bool             `json:"enabled"`
	Seq         int64            `json:"seq"`
	LastCheck   int64            `json:"last_check,omitempty"`
	LastResult  string           `json:"last_result,omitempty"`
	LastError   string           `json:"last_error,omitempty"`
	LastErrors  []SyncError      `json:"last_errors,omitempty"`
	Pending     string           `json:"pending_request,omitempty"`
	Handled     []string         `json:"handled_requests,omitempty"`
	ContainerID string           `json:"container_id,omitempty"`
	Baseline    map[string]int64 `json:"baseline,omitempty"`
	// BaselineAt is when the baseline was taken, just after the server
	// started. A status.json older than that is from the previous run.
	BaselineAt int64                 `json:"baseline_at,omitempty"`
	Synced     map[string]SyncedItem `json:"synced,omitempty"`
	Blocked    []string              `json:"blocked,omitempty"`
}

// SyncedItem is a pack ndl-ce delivered and still keeps in items/.
type SyncedItem struct {
	Title       string `json:"title"`
	State       string `json:"state"` // new or updated
	Kind        string `json:"kind"`  // assets or lua
	TimeUpdated int64  `json:"time_updated"`
	Size        int64  `json:"size"`
	SHA256      string `json:"sha256"`
}

type SyncError struct {
	WSID  string `json:"wsid"`
	Error string `json:"error"`
}

type SyncManifestItem struct {
	WSID        string `json:"wsid"`
	Title       string `json:"title"`
	State       string `json:"state"`
	Kind        string `json:"kind"`
	TimeUpdated int64  `json:"time_updated"`
	Size        int64  `json:"size"`
	SHA256      string `json:"sha256"`
	File        string `json:"file"`
}

type SyncManifest struct {
	Version    int                `json:"version"`
	Seq        int64              `json:"seq"`
	Generated  int64              `json:"generated"`
	RequestID  *string            `json:"request_id"`
	Collection string             `json:"collection"`
	Items      []SyncManifestItem `json:"items"`
	Errors     []SyncError        `json:"errors"`
}

// SyncRequest is request.json, written by the server.
type SyncRequest struct {
	Version       int    `json:"version"`
	ID            string `json:"id"`
	Type          string `json:"type"`
	RequestedBy   string `json:"requested_by"`
	RequestedName string `json:"requested_name"`
	At            int64  `json:"at"`
}

// SyncStatus is status.json, written by the server.
type SyncStatus struct {
	Version int              `json:"version"`
	SeqSeen int64            `json:"seq_seen"`
	Updated int64            `json:"updated"`
	Blocked []string         `json:"blocked"`
	Items   []SyncStatusItem `json:"items"`
}

type SyncStatusItem struct {
	WSID        string `json:"wsid"`
	TimeUpdated int64  `json:"time_updated"`
	State       string `json:"state"`
	Detail      string `json:"detail"`
}

// ValidRequest reports whether a request is one ndl-ce acts on.
func (r SyncRequest) Valid() bool {
	return syncRequestIDRe.MatchString(r.ID) && (r.Type == "" || r.Type == "check")
}

// Handle records that a request was answered; ids are never processed twice.
func (s *SyncState) Handle(id string) {
	s.Handled = append(s.Handled, id)
	if len(s.Handled) > 50 {
		s.Handled = s.Handled[len(s.Handled)-50:]
	}
	if s.Pending == id {
		s.Pending = ""
	}
}

func (s *SyncState) Seen(id string) bool {
	if id == s.Pending {
		return true
	}
	for _, h := range s.Handled {
		if h == id {
			return true
		}
	}
	return false
}

// WorkshopFile is one Workshop item as Steam reports it.
type WorkshopFile struct {
	ID          string
	Title       string
	TimeUpdated int64
	Size        int64
	Available   bool
	Banned      bool
	AppID       int
}

// WorkshopAPI reads collections and items from Steam.
type WorkshopAPI interface {
	Collection(ctx context.Context, id string) ([]string, error)
	Files(ctx context.Context, ids []string) (map[string]WorkshopFile, error)
}

// WorkshopFetcher downloads items into dir and returns each item's file,
// or why it failed.
type WorkshopFetcher func(ctx context.Context, ids []string, dir string) (map[string]string, map[string]string)

// WorkshopSync runs checks for one server folder.
type WorkshopSync struct {
	API   WorkshopAPI
	Fetch WorkshopFetcher
	Now   func() time.Time
	// Work is a private scratch folder outside the server's folder.
	Work string
	// Items holds the packs, outside the server's folder. The server sees it
	// read-only as cs_sync/items, so only ndl-ce can put a pack there.
	Items string
}

func (w WorkshopSync) now() time.Time {
	if w.Now != nil {
		return w.Now()
	}
	return time.Now()
}

// Baseline records the collection's versions as what a server loaded when
// it started, for use until the server writes its own status.json.
func (w WorkshopSync) Baseline(ctx context.Context, st *SyncState, collection string) error {
	ids, err := w.API.Collection(ctx, collection)
	if err != nil {
		return err
	}
	files, err := w.API.Files(ctx, ids)
	if err != nil {
		return err
	}
	st.Baseline = map[string]int64{}
	st.BaselineAt = w.now().Unix()
	for id, f := range files {
		if f.Available {
			st.Baseline[id] = f.TimeUpdated
		}
	}
	return nil
}

// Check compares the collection with what the server runs, delivers what is
// new or updated and writes the manifest. requestID is "" for a scheduled
// check. The caller tells the server about the new seq.
func (w WorkshopSync) Check(ctx context.Context, st *SyncState, dir *SyncDir, collection, requestID string) (SyncManifest, error) {
	now := w.now()
	st.LastCheck = now.Unix()
	status, statusAt, hasStatus := readSyncStatus(dir)
	blocked := w.blocked(st, status)
	if hasStatus && st.BaselineAt > 0 && statusAt < st.BaselineAt*int64(time.Second) {
		// Written before this run of the server: it describes what the
		// last run loaded, so the baseline is the truth until the server
		// writes a new one. Its blocklist still counts.
		status, hasStatus = SyncStatus{}, false
	}

	ids, err := w.API.Collection(ctx, collection)
	if err != nil {
		return SyncManifest{}, err
	}
	files, err := w.API.Files(ctx, ids)
	if err != nil {
		return SyncManifest{}, err
	}
	known := map[string]int64{}
	if hasStatus {
		for _, it := range status.Items {
			if it.State != "failed" && isDigits(it.WSID) && it.TimeUpdated > known[it.WSID] {
				known[it.WSID] = it.TimeUpdated
			}
		}
	} else {
		for id, t := range st.Baseline {
			known[id] = t
		}
	}
	if st.Synced == nil {
		st.Synced = map[string]SyncedItem{}
	}
	for id, it := range st.Synced {
		if it.TimeUpdated > known[id] {
			known[id] = it.TimeUpdated
		}
	}

	var errs []SyncError
	var want []string
	for _, id := range ids {
		f, ok := files[id]
		switch {
		case blocked[id]:
			errs = append(errs, SyncError{WSID: id, Error: "blocked"})
		case !ok || !f.Available:
			errs = append(errs, SyncError{WSID: id, Error: "not available on Steam"})
		case f.Banned:
			errs = append(errs, SyncError{WSID: id, Error: "banned on Steam"})
		case f.AppID != 0 && strconv.Itoa(f.AppID) != gmodWorkshopApp:
			errs = append(errs, SyncError{WSID: id, Error: "not a Garry's Mod item"})
		case f.Size > syncMaxItemBytes:
			errs = append(errs, SyncError{WSID: id, Error: "larger than 4 GiB"})
		case f.TimeUpdated > known[id]:
			want = append(want, id)
		}
	}

	if len(want) > 0 {
		errs = append(errs, w.deliver(ctx, st, dir, want, files, known)...)
	}
	w.cleanup(st, status, hasStatus)

	st.Seq++
	m := SyncManifest{Version: 1, Seq: st.Seq, Generated: now.Unix(), Collection: collection, Items: []SyncManifestItem{}, Errors: errs}
	if m.Errors == nil {
		m.Errors = []SyncError{}
	}
	if requestID != "" {
		m.RequestID = &requestID
	}
	acked := map[string]bool{}
	for _, it := range status.Items {
		acked[it.WSID+"@"+strconv.FormatInt(it.TimeUpdated, 10)] = true
	}
	for _, id := range sortedSyncedIDs(st.Synced) {
		it := st.Synced[id]
		if acked[id+"@"+strconv.FormatInt(it.TimeUpdated, 10)] {
			continue
		}
		m.Items = append(m.Items, SyncManifestItem{
			WSID: id, Title: it.Title, State: it.State, Kind: it.Kind, TimeUpdated: it.TimeUpdated,
			Size: it.Size, SHA256: it.SHA256, File: SyncItemsDir + "/" + id + ".gma",
		})
	}
	body, _ := json.MarshalIndent(m, "", "  ")
	if err := dir.WriteAtomic(SyncManifestFile, append(body, '\n')); err != nil {
		st.Seq--
		return SyncManifest{}, fmt.Errorf("writing the manifest: %w", err)
	}
	st.LastErrors = errs
	st.LastError = ""
	st.LastResult = fmt.Sprintf("%d delivered, %d waiting for the server, %d errors", len(want)-countErrIDs(errs, want), len(m.Items), len(errs))
	return m, nil
}

func countErrIDs(errs []SyncError, ids []string) int {
	in := map[string]bool{}
	for _, id := range ids {
		in[id] = true
	}
	n := 0
	for _, e := range errs {
		if in[e.WSID] {
			n++
		}
	}
	return n
}

func sortedSyncedIDs(m map[string]SyncedItem) []string {
	out := make([]string, 0, len(m))
	for id := range m {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

func (w WorkshopSync) blocked(st *SyncState, status SyncStatus) map[string]bool {
	out := map[string]bool{}
	for _, id := range syncAlwaysBlocked {
		out[id] = true
	}
	for _, id := range st.Blocked {
		out[id] = true
	}
	// The server may add to the blocklist, never take from it.
	for _, id := range status.Blocked {
		if isDigits(id) && len(id) <= 20 && !out[id] && len(st.Blocked) < 1000 {
			st.Blocked = append(st.Blocked, id)
			out[id] = true
		}
	}
	return out
}

// deliver downloads, unpacks, checks and stores each wanted item.
func (w WorkshopSync) deliver(ctx context.Context, st *SyncState, dir *SyncDir, want []string, files map[string]WorkshopFile, known map[string]int64) []SyncError {
	var errs []SyncError
	dl := filepath.Join(w.Work, "download")
	_ = os.RemoveAll(dl)
	if err := os.MkdirAll(dl, 0o700); err != nil {
		for _, id := range want {
			errs = append(errs, SyncError{WSID: id, Error: "download folder: " + err.Error()})
		}
		return errs
	}
	defer os.RemoveAll(dl)
	got, failed := w.Fetch(ctx, want, dl)
	if err := os.MkdirAll(w.Items, 0o755); err != nil {
		for _, id := range want {
			errs = append(errs, SyncError{WSID: id, Error: "items folder: " + err.Error()})
		}
		return errs
	}
	for _, id := range want {
		src, ok := got[id]
		if !ok {
			msg := failed[id]
			if msg == "" {
				msg = "SteamCMD did not download it"
			}
			errs = append(errs, SyncError{WSID: id, Error: "download failed: " + msg})
			continue
		}
		gma := filepath.Join(dl, id+".unpacked.gma")
		if err := unpackWorkshopFile(src, gma); err != nil {
			errs = append(errs, SyncError{WSID: id, Error: err.Error()})
			continue
		}
		info, err := ReadGMA(gma)
		if err != nil {
			errs = append(errs, SyncError{WSID: id, Error: err.Error()})
			continue
		}
		in, err := os.Open(gma)
		if err != nil {
			errs = append(errs, SyncError{WSID: id, Error: err.Error()})
			continue
		}
		h := sha256.New()
		size, err := writeFileAtomic(filepath.Join(w.Items, id+".gma"), io.TeeReader(in, h))
		_ = in.Close()
		_ = os.Remove(gma)
		if err != nil {
			errs = append(errs, SyncError{WSID: id, Error: "storing the pack: " + err.Error()})
			continue
		}
		state := "new"
		if _, ok := known[id]; ok {
			state = "updated"
		}
		kind := "assets"
		if info.HasLua() {
			kind = "lua"
		}
		f := files[id]
		st.Synced[id] = SyncedItem{Title: clip(f.Title, 200), State: state, Kind: kind, TimeUpdated: f.TimeUpdated, Size: size, SHA256: hex.EncodeToString(h.Sum(nil))}
	}
	return errs
}

// cleanup deletes packs the server now loads from the Workshop itself:
// status.json marks them in_collection at the delivered version or later.
func (w WorkshopSync) cleanup(st *SyncState, status SyncStatus, hasStatus bool) {
	if !hasStatus || len(st.Synced) == 0 {
		return
	}
	loaded := map[string]int64{}
	for _, it := range status.Items {
		if it.State == "in_collection" && it.TimeUpdated > loaded[it.WSID] {
			loaded[it.WSID] = it.TimeUpdated
		}
	}
	for id, it := range st.Synced {
		if t, ok := loaded[id]; ok && t >= it.TimeUpdated {
			if err := os.Remove(filepath.Join(w.Items, id+".gma")); err == nil || errors.Is(err, os.ErrNotExist) {
				delete(st.Synced, id)
			}
		}
	}
}

// writeFileAtomic writes path through a temporary file and a rename.
func writeFileAtomic(path string, r io.Reader) (int64, error) {
	tmp := path + ".tmp"
	_ = os.Remove(tmp)
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return 0, err
	}
	n, err := io.Copy(f, r)
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp, path)
	}
	if err != nil {
		_ = os.Remove(tmp)
		return 0, err
	}
	return n, nil
}

// readSyncStatus returns the server's status and when it was written, in
// nanoseconds.
func readSyncStatus(dir *SyncDir) (SyncStatus, int64, bool) {
	var s SyncStatus
	raw, mtime, err := dir.Read(SyncStatusFile, syncMaxJSONBytes)
	if err != nil || json.Unmarshal(raw, &s) != nil {
		return SyncStatus{}, 0, false
	}
	return s, mtime, true
}

// ReadSyncRequest returns the server's request and when it was written.
func ReadSyncRequest(dir *SyncDir) (SyncRequest, int64, error) {
	var r SyncRequest
	raw, mtime, err := dir.Read(SyncRequestFile, syncMaxJSONBytes)
	if err != nil {
		return r, 0, err
	}
	if err := json.Unmarshal(raw, &r); err != nil {
		return r, mtime, err
	}
	return r, mtime, nil
}

// SyncDir is a folder inside a server's data that is reached without
// following links: the game server can change anything there, and ndl-ce
// writes into it as root.
type SyncDir struct {
	fd int
}

// OpenSyncDir opens base/rel, creating missing folders, refusing links.
func OpenSyncDir(base, rel string) (*SyncDir, error) {
	fd, err := unix.Open(base, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	d := &SyncDir{fd: fd}
	for _, part := range strings.Split(strings.Trim(rel, "/"), "/") {
		next, err := d.Sub(part)
		d.Close()
		if err != nil {
			return nil, err
		}
		d = next
	}
	return d, nil
}

// Sub opens (and creates) a child folder.
func (d *SyncDir) Sub(name string) (*SyncDir, error) {
	if name == "" || name == "." || name == ".." || strings.Contains(name, "/") {
		return nil, errors.New("bad folder name")
	}
	if err := unix.Mkdirat(d.fd, name, 0o755); err != nil && err != unix.EEXIST {
		return nil, err
	}
	fd, err := unix.Openat(d.fd, name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, fmt.Errorf("%s is not a plain folder", name)
	}
	return &SyncDir{fd: fd}, nil
}

func (d *SyncDir) Close() {
	if d != nil && d.fd >= 0 {
		_ = unix.Close(d.fd)
		d.fd = -1
	}
}

// Read reads a regular file of at most max bytes and its modification time.
func (d *SyncDir) Read(name string, max int64) ([]byte, int64, error) {
	fd, err := unix.Openat(d.fd, name, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC|unix.O_NONBLOCK, 0)
	if err != nil {
		if err == unix.ENOENT {
			return nil, 0, os.ErrNotExist
		}
		return nil, 0, err
	}
	f := os.NewFile(uintptr(fd), name)
	defer f.Close()
	st, err := f.Stat()
	if err != nil || !st.Mode().IsRegular() {
		return nil, 0, errors.New("not a regular file")
	}
	if st.Size() > max {
		return nil, 0, errors.New("file too large")
	}
	raw, err := io.ReadAll(io.LimitReader(f, max))
	return raw, st.ModTime().UnixNano(), err
}

// WriteAtomic writes name through a temporary file and a rename.
func (d *SyncDir) WriteAtomic(name string, body []byte) error {
	_, err := d.WriteFromAtomic(name, strings.NewReader(string(body)))
	return err
}

func (d *SyncDir) WriteFromAtomic(name string, r io.Reader) (int64, error) {
	tmp := name + ".tmp"
	_ = unix.Unlinkat(d.fd, tmp, 0)
	fd, err := unix.Openat(d.fd, tmp, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0o644)
	if err != nil {
		return 0, err
	}
	f := os.NewFile(uintptr(fd), tmp)
	n, err := io.Copy(f, r)
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = unix.Fchmodat(d.fd, tmp, 0o644, 0)
	}
	if err == nil {
		err = unix.Renameat(d.fd, tmp, d.fd, name)
	}
	if err != nil {
		_ = unix.Unlinkat(d.fd, tmp, 0)
		return 0, err
	}
	return n, nil
}

// Remove deletes a file (never what a link points at).
func (d *SyncDir) Remove(name string) error {
	if err := unix.Unlinkat(d.fd, name, 0); err != nil {
		if err == unix.ENOENT {
			return os.ErrNotExist
		}
		return err
	}
	return nil
}

// SteamWorkshopAPI is the public Steam Web API; it needs no key.
type SteamWorkshopAPI struct {
	Client *http.Client
	Base   string
}

func (a SteamWorkshopAPI) post(ctx context.Context, path string, form url.Values, out any) error {
	base := a.Base
	if base == "" {
		base = "https://api.steampowered.com"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+path, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", "No-DAL-GameServers/1.0")
	client := a.Client
	if client == nil {
		client = defaultHTTPClient()
	}
	res, err := client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode >= 400 {
		return fmt.Errorf("Steam returned HTTP %d", res.StatusCode)
	}
	if err := json.NewDecoder(io.LimitReader(res.Body, 32<<20)).Decode(out); err != nil {
		return errors.New("Steam's answer is not valid JSON")
	}
	return nil
}

func (a SteamWorkshopAPI) Collection(ctx context.Context, id string) ([]string, error) {
	if !isDigits(id) {
		return nil, errors.New("the Workshop collection ID must be a number")
	}
	form := url.Values{"collectioncount": {"1"}, "publishedfileids[0]": {id}}
	var out struct {
		Response struct {
			CollectionDetails []struct {
				Result   int `json:"result"`
				Children []struct {
					ID       string `json:"publishedfileid"`
					FileType int    `json:"filetype"`
				} `json:"children"`
			} `json:"collectiondetails"`
		} `json:"response"`
	}
	if err := a.post(ctx, "/ISteamRemoteStorage/GetCollectionDetails/v1/", form, &out); err != nil {
		return nil, err
	}
	if len(out.Response.CollectionDetails) == 0 || out.Response.CollectionDetails[0].Result != 1 {
		return nil, fmt.Errorf("Workshop collection %s was not found", id)
	}
	var ids []string
	for _, c := range out.Response.CollectionDetails[0].Children {
		if c.FileType == 0 && isDigits(c.ID) {
			ids = append(ids, c.ID)
		}
	}
	return ids, nil
}

func (a SteamWorkshopAPI) Files(ctx context.Context, ids []string) (map[string]WorkshopFile, error) {
	out := map[string]WorkshopFile{}
	for start := 0; start < len(ids); start += 100 {
		end := start + 100
		if end > len(ids) {
			end = len(ids)
		}
		form := url.Values{"itemcount": {strconv.Itoa(end - start)}}
		for i, id := range ids[start:end] {
			form.Set("publishedfileids["+strconv.Itoa(i)+"]", id)
		}
		var res struct {
			Response struct {
				Details []struct {
					ID          string      `json:"publishedfileid"`
					Result      int         `json:"result"`
					Title       string      `json:"title"`
					TimeUpdated int64       `json:"time_updated"`
					FileSize    json.Number `json:"file_size"`
					Banned      int         `json:"banned"`
					AppID       int         `json:"consumer_app_id"`
				} `json:"publishedfiledetails"`
			} `json:"response"`
		}
		if err := a.post(ctx, "/ISteamRemoteStorage/GetPublishedFileDetails/v1/", form, &res); err != nil {
			return nil, err
		}
		for _, d := range res.Response.Details {
			size, _ := d.FileSize.Int64()
			out[d.ID] = WorkshopFile{ID: d.ID, Title: d.Title, TimeUpdated: d.TimeUpdated, Size: size, Available: d.Result == 1, Banned: d.Banned != 0, AppID: d.AppID}
		}
	}
	return out, nil
}

// WorkshopDownload downloads Garry's Mod Workshop items with SteamCMD,
// anonymously, into dir.
func (r *Runtime) WorkshopDownload(ctx context.Context, ids []string, dir string) (map[string]string, map[string]string) {
	got, failed := map[string]string{}, map[string]string{}
	args := []string{"run", "--rm", "-v", dir + ":/data"}
	args = append(args, r.networkArgs()...)
	args = append(args, "--security-opt", "no-new-privileges", "steamcmd/steamcmd:debian", "+force_install_dir", "/data", "+login", "anonymous")
	for _, id := range ids {
		args = append(args, "+workshop_download_item", gmodWorkshopApp, id)
	}
	args = append(args, "+quit")
	out, err := r.run(ctx, r.dockerBin(), args...)
	for _, line := range strings.Split(string(out), "\n") {
		if i := strings.Index(line, "ERROR! Download item "); i >= 0 {
			rest := strings.Fields(line[i+len("ERROR! Download item "):])
			if len(rest) > 0 && isDigits(rest[0]) {
				failed[rest[0]] = strings.TrimSpace(strings.Join(rest[1:], " "))
			}
		}
	}
	for _, id := range ids {
		itemDir := filepath.Join(dir, "steamapps", "workshop", "content", gmodWorkshopApp, id)
		entries, rerr := os.ReadDir(itemDir)
		var best string
		var bestSize int64 = -1
		for _, e := range entries {
			info, ierr := e.Info()
			if ierr != nil || !info.Mode().IsRegular() {
				continue
			}
			if info.Size() > bestSize {
				best, bestSize = filepath.Join(itemDir, e.Name()), info.Size()
			}
		}
		switch {
		case best != "":
			got[id] = best
		case failed[id] != "":
		case err != nil:
			failed[id] = "SteamCMD failed: " + clip(strings.TrimSpace(err.Error()), 200)
		case rerr != nil:
			failed[id] = "SteamCMD did not download it"
		}
	}
	return got, failed
}
