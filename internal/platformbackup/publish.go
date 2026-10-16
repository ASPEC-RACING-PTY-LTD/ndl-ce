package platformbackup

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

// Prefix is where every platform backup object lives, at the bucket root,
// so recovery finds it with nothing but the bucket.
const Prefix = "ndl-platform/"

// PointerKey names the current envelope and the published snapshots.
const PointerKey = Prefix + "pointer.json"

const (
	envelopeDir = Prefix + "envelopes/"
	snapshotDir = Prefix + "snapshots/"
	tmpDir      = Prefix + "tmp/"
)

// ErrNotFound is returned by a Store for a missing object.
var ErrNotFound = errors.New("object not found")

// Object is one listed object.
type Object struct {
	Key  string
	Size int64
}

// Store is the bucket. Implementations must return ErrNotFound (or wrap it)
// for a missing object on Get.
type Store interface {
	Put(ctx context.Context, key string, body []byte) error
	Get(ctx context.Context, key string) ([]byte, error)
	Delete(ctx context.Context, key string) error
	List(ctx context.Context, prefix string) ([]Object, error)
}

// Envelope holds the snapshot key wrapped for each Owner.
type Envelope struct {
	Version   int       `json:"version"`
	KeyID     string    `json:"key_id"`
	KeyCheck  string    `json:"key_check"`
	CreatedAt time.Time `json:"created_at"`
	Wraps     []Wrap    `json:"wraps"`
}

// NewEnvelope builds the envelope for key from its Owners' wraps; wraps of
// any other key are left out.
func NewEnvelope(key []byte, wraps []Wrap, now time.Time) Envelope {
	id := KeyID(key)
	env := Envelope{Version: 1, KeyID: id, KeyCheck: KeyCheck(key), CreatedAt: now.UTC()}
	for _, w := range wraps {
		if w.KeyID == id {
			env.Wraps = append(env.Wraps, w)
		}
	}
	sort.Slice(env.Wraps, func(i, j int) bool { return env.Wraps[i].Owner < env.Wraps[j].Owner })
	return env
}

// Unlock returns the snapshot key for an Owner's sign-in name and password.
func (e Envelope) Unlock(username, password string) ([]byte, error) {
	owner := OwnerID(username)
	for _, w := range e.Wraps {
		if w.Owner != owner || w.KeyID != e.KeyID {
			continue
		}
		key, err := UnwrapKey(w, password)
		if err != nil {
			continue
		}
		if KeyCheck(key) != e.KeyCheck {
			return nil, fmt.Errorf("the envelope's key check does not match")
		}
		return key, nil
	}
	return nil, ErrLocked
}

// SnapshotRef is one published snapshot.
type SnapshotRef struct {
	Key         string    `json:"key"`
	CreatedAt   time.Time `json:"created_at"`
	SHA256      string    `json:"sha256"`
	Size        int64     `json:"size"`
	KeyID       string    `json:"key_id"`
	SchemaLevel int       `json:"schema_level"`
	Workloads   int       `json:"workloads"`
}

// Pointer is the entry point recovery reads first. It is plaintext and
// holds no secrets.
type Pointer struct {
	Version          int           `json:"version"`
	Envelope         string        `json:"envelope"`
	PreviousEnvelope string        `json:"previous_envelope,omitempty"`
	Snapshots        []SnapshotRef `json:"snapshots"`
	UpdatedAt        time.Time     `json:"updated_at"`
}

// Latest returns the newest snapshot sealed with keyID, or the newest of any
// key when keyID is empty.
func (p *Pointer) Latest(keyID string) *SnapshotRef {
	var best *SnapshotRef
	for i := range p.Snapshots {
		s := &p.Snapshots[i]
		if keyID != "" && s.KeyID != keyID {
			continue
		}
		if best == nil || s.CreatedAt.After(best.CreatedAt) {
			best = s
		}
	}
	return best
}

// LoadPointer reads the pointer; a bucket without one returns nil, nil.
func LoadPointer(ctx context.Context, st Store) (*Pointer, error) {
	raw, err := st.Get(ctx, PointerKey)
	if errors.Is(err, ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var p Pointer
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, fmt.Errorf("the platform backup pointer is unreadable: %w", err)
	}
	return &p, nil
}

// LoadEnvelope reads one envelope.
func LoadEnvelope(ctx context.Context, st Store, key string) (*Envelope, error) {
	raw, err := st.Get(ctx, key)
	if err != nil {
		return nil, err
	}
	var e Envelope
	if err := json.Unmarshal(raw, &e); err != nil {
		return nil, fmt.Errorf("envelope %s is unreadable: %w", key, err)
	}
	return &e, nil
}

func objectName(dir, ext string, now time.Time) string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return dir + now.UTC().Format("20060102T150405Z") + "-" + hex.EncodeToString(b) + ext
}

// putVerified writes an object and reads it back; the bytes must match.
func putVerified(ctx context.Context, st Store, key string, body []byte) error {
	if err := st.Put(ctx, key, body); err != nil {
		return fmt.Errorf("upload %s: %w", key, err)
	}
	got, err := st.Get(ctx, key)
	if err != nil {
		return fmt.Errorf("read back %s: %w", key, err)
	}
	if !bytes.Equal(got, body) {
		return fmt.Errorf("%s read back differently from what was written", key)
	}
	return nil
}

func savePointer(ctx context.Context, st Store, p Pointer, now time.Time) error {
	p.Version = 1
	p.UpdatedAt = now.UTC()
	raw, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	return putVerified(ctx, st, PointerKey, raw)
}

// PublishEnvelope writes env as a new object, reads it back, checks it with
// verify (which must at least confirm the key check against the local key),
// and only then points the pointer at it. The previous envelope is kept as
// a fallback; older ones are deleted. Nothing already published is
// overwritten before the new envelope is verified.
func PublishEnvelope(ctx context.Context, st Store, env Envelope, verify func(Envelope) error, now time.Time) (*Pointer, error) {
	if len(env.Wraps) == 0 {
		return nil, fmt.Errorf("no Owner can unlock this key yet")
	}
	cur, err := LoadPointer(ctx, st)
	if err != nil {
		return nil, err
	}
	raw, err := json.MarshalIndent(env, "", "  ")
	if err != nil {
		return nil, err
	}
	key := objectName(envelopeDir, ".json", now)
	if err := putVerified(ctx, st, key, raw); err != nil {
		_ = st.Delete(ctx, key)
		return nil, err
	}
	back, err := LoadEnvelope(ctx, st, key)
	if err == nil && verify != nil {
		err = verify(*back)
	}
	if err != nil {
		_ = st.Delete(ctx, key)
		return nil, fmt.Errorf("the new envelope failed verification and was not published: %w", err)
	}
	next := Pointer{Envelope: key}
	if cur != nil {
		next.PreviousEnvelope = cur.Envelope
		next.Snapshots = cur.Snapshots
	}
	if err := savePointer(ctx, st, next, now); err != nil {
		return nil, err
	}
	keep := map[string]bool{next.Envelope: true, next.PreviousEnvelope: true}
	if objs, err := st.List(ctx, envelopeDir); err == nil {
		for _, o := range objs {
			if !keep[o.Key] {
				_ = st.Delete(ctx, o.Key)
			}
		}
	}
	return &next, nil
}

// Retention keeps the newest snapshot of each of the last daily days and of
// each of the last weekly ISO weeks.
type Retention struct{ Daily, Weekly int }

// DefaultRetention is 7 daily and 4 weekly snapshots.
var DefaultRetention = Retention{Daily: 7, Weekly: 4}

func (r Retention) keep(snaps []SnapshotRef) map[string]bool {
	sorted := append([]SnapshotRef(nil), snaps...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].CreatedAt.After(sorted[j].CreatedAt) })
	keep := map[string]bool{}
	days, weeks := map[string]bool{}, map[string]bool{}
	for _, s := range sorted {
		t := s.CreatedAt.UTC()
		day := t.Format("2006-01-02")
		y, w := t.ISOWeek()
		week := fmt.Sprintf("%d-%02d", y, w)
		if !days[day] && len(days) < r.Daily {
			days[day] = true
			keep[s.Key] = true
		}
		if !weeks[week] && len(weeks) < r.Weekly {
			weeks[week] = true
			keep[s.Key] = true
		}
	}
	if len(sorted) > 0 {
		keep[sorted[0].Key] = true
	}
	return keep
}

// PublishSnapshot uploads a sealed snapshot to a temporary object, checks
// its checksum, downloads and decrypts it, writes it under snapshots/,
// checks that copy, and only then adds it to the pointer. Retention then
// drops snapshots beyond it, and snapshots of other keys once a snapshot of
// the current key exists. A failure leaves the published state as it was.
func PublishSnapshot(ctx context.Context, st Store, key, archive []byte, ref SnapshotRef, keep Retention, now time.Time) (*Pointer, error) {
	cur, err := LoadPointer(ctx, st)
	if err != nil {
		return nil, err
	}
	if cur == nil || cur.Envelope == "" {
		return nil, fmt.Errorf("no envelope is published yet, so a snapshot could not be unlocked; an Owner must sign in first")
	}
	env, err := LoadEnvelope(ctx, st, cur.Envelope)
	if err != nil {
		return nil, err
	}
	if env.KeyID != KeyID(key) || env.KeyCheck != KeyCheck(key) {
		return nil, fmt.Errorf("the published envelope is for another key; an Owner must sign in to publish the current one")
	}
	blob, err := Seal(key, archive)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(blob)
	ref.SHA256 = hex.EncodeToString(sum[:])
	ref.Size = int64(len(blob))
	ref.KeyID = KeyID(key)
	ref.CreatedAt = now.UTC()
	plainSum := sha256.Sum256(archive)

	tmp := objectName(tmpDir, ".ndlp", now)
	defer func() { _ = st.Delete(context.WithoutCancel(ctx), tmp) }()
	if err := st.Put(ctx, tmp, blob); err != nil {
		return nil, fmt.Errorf("upload snapshot: %w", err)
	}
	if err := verifyBlob(ctx, st, tmp, key, ref.SHA256, plainSum); err != nil {
		return nil, err
	}
	ref.Key = objectName(snapshotDir, ".ndlp", now)
	if err := st.Put(ctx, ref.Key, blob); err != nil {
		return nil, fmt.Errorf("publish snapshot: %w", err)
	}
	if err := verifyBlob(ctx, st, ref.Key, key, ref.SHA256, plainSum); err != nil {
		_ = st.Delete(ctx, ref.Key)
		return nil, err
	}
	next := *cur
	next.Snapshots = append(append([]SnapshotRef(nil), cur.Snapshots...), ref)
	// Snapshots of an older key go once one of the current key is in.
	current := next.Snapshots[:0:0]
	for _, s := range next.Snapshots {
		if s.KeyID == ref.KeyID {
			current = append(current, s)
		}
	}
	kept := keep.keep(current)
	var stay, drop []SnapshotRef
	for _, s := range next.Snapshots {
		if kept[s.Key] {
			stay = append(stay, s)
		} else {
			drop = append(drop, s)
		}
	}
	next.Snapshots = stay
	if err := savePointer(ctx, st, next, now); err != nil {
		_ = st.Delete(ctx, ref.Key)
		return nil, err
	}
	for _, s := range drop {
		_ = st.Delete(ctx, s.Key)
	}
	// Leftovers of interrupted runs.
	if objs, err := st.List(ctx, tmpDir); err == nil {
		for _, o := range objs {
			if o.Key != tmp {
				_ = st.Delete(ctx, o.Key)
			}
		}
	}
	listed := map[string]bool{}
	for _, s := range next.Snapshots {
		listed[s.Key] = true
	}
	if objs, err := st.List(ctx, snapshotDir); err == nil {
		for _, o := range objs {
			if !listed[o.Key] && strings.HasSuffix(o.Key, ".ndlp") {
				_ = st.Delete(ctx, o.Key)
			}
		}
	}
	return &next, nil
}

func verifyBlob(ctx context.Context, st Store, objKey string, key []byte, sha string, plainSum [32]byte) error {
	got, err := st.Get(ctx, objKey)
	if err != nil {
		return fmt.Errorf("download %s for verification: %w", objKey, err)
	}
	sum := sha256.Sum256(got)
	if hex.EncodeToString(sum[:]) != sha {
		return fmt.Errorf("%s checksum differs after upload", objKey)
	}
	plain, err := Open(key, got)
	if err != nil {
		return fmt.Errorf("%s: %w", objKey, err)
	}
	if sha256.Sum256(plain) != plainSum {
		return fmt.Errorf("%s decrypted to different content", objKey)
	}
	return nil
}

// FetchSnapshot downloads, checks and decrypts a published snapshot.
func FetchSnapshot(ctx context.Context, st Store, key []byte, ref SnapshotRef) ([]byte, error) {
	got, err := st.Get(ctx, ref.Key)
	if err != nil {
		return nil, err
	}
	if ref.SHA256 != "" {
		sum := sha256.Sum256(got)
		if hex.EncodeToString(sum[:]) != ref.SHA256 {
			return nil, fmt.Errorf("snapshot %s does not match its checksum", ref.Key)
		}
	}
	return Open(key, got)
}

// Locate finds the published state of a bucket for recovery: the pointer,
// or, when it is missing or unreadable, the newest envelope and the
// snapshots found by listing.
func Locate(ctx context.Context, st Store) (*Pointer, error) {
	p, err := LoadPointer(ctx, st)
	if err == nil && p != nil && p.Envelope != "" {
		return p, nil
	}
	envs, lerr := st.List(ctx, envelopeDir)
	if lerr != nil {
		if err != nil {
			return nil, err
		}
		return nil, lerr
	}
	if len(envs) == 0 {
		return nil, fmt.Errorf("no NDL-CE platform backup was found in this bucket (looked under %s)", Prefix)
	}
	sort.Slice(envs, func(i, j int) bool { return envs[i].Key > envs[j].Key })
	out := &Pointer{Version: 1, Envelope: envs[0].Key}
	if len(envs) > 1 {
		out.PreviousEnvelope = envs[1].Key
	}
	snaps, _ := st.List(ctx, snapshotDir)
	for _, o := range snaps {
		if !strings.HasSuffix(o.Key, ".ndlp") {
			continue
		}
		name := strings.TrimPrefix(o.Key, snapshotDir)
		t, _ := time.Parse("20060102T150405Z", strings.SplitN(name, "-", 2)[0])
		out.Snapshots = append(out.Snapshots, SnapshotRef{Key: o.Key, Size: o.Size, CreatedAt: t})
	}
	return out, nil
}
