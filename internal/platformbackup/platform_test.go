package platformbackup

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

var fastKDF = KDF{Alg: "argon2id", Time: 1, MemoryKiB: 8 * 1024, Threads: 1}

func mustKey(t *testing.T) []byte {
	t.Helper()
	k, err := NewKey()
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func TestEveryOwnerUnlocksAndNobodyElse(t *testing.T) {
	key := mustKey(t)
	now := time.Now()
	a, err := WrapKey(key, "Alice", "alice-pass", fastKDF, now)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := WrapKey(key, "bob@example.com", "bob-pass", fastKDF, now)
	other, _ := WrapKey(mustKey(t), "carol", "carol-pass", fastKDF, now)
	env := NewEnvelope(key, []Wrap{a, b, other}, now)
	if len(env.Wraps) != 2 {
		t.Fatalf("a wrap of another key must be left out: %d", len(env.Wraps))
	}
	for _, c := range [][2]string{{"alice", "alice-pass"}, {" BOB@example.com ", "bob-pass"}} {
		got, err := env.Unlock(c[0], c[1])
		if err != nil || !bytes.Equal(got, key) {
			t.Fatalf("%s: %v", c[0], err)
		}
	}
	for _, c := range [][2]string{{"alice", "bob-pass"}, {"carol", "carol-pass"}, {"mallory", "alice-pass"}} {
		if _, err := env.Unlock(c[0], c[1]); !errors.Is(err, ErrLocked) {
			t.Fatalf("%s must not unlock: %v", c[0], err)
		}
	}
	if strings.Contains(string(mustJSON(env)), "alice") {
		t.Fatal("the envelope must not name Owners")
	}
}

func TestSealedSnapshotsRefuseTamperingAndOtherKeys(t *testing.T) {
	key := mustKey(t)
	blob, err := Seal(key, []byte("archive"))
	if err != nil {
		t.Fatal(err)
	}
	if got, err := Open(key, blob); err != nil || string(got) != "archive" {
		t.Fatalf("open %q %v", got, err)
	}
	bad := append([]byte(nil), blob...)
	bad[len(bad)-1] ^= 1
	if _, err := Open(key, bad); err == nil {
		t.Fatal("tampered snapshot must fail")
	}
	if _, err := Open(mustKey(t), blob); err == nil {
		t.Fatal("another key must fail")
	}
}

func publishEnv(t *testing.T, st Store, key []byte, now time.Time, users ...string) *Pointer {
	t.Helper()
	var wraps []Wrap
	for _, u := range users {
		w, err := WrapKey(key, u, u+"-pass", fastKDF, now)
		if err != nil {
			t.Fatal(err)
		}
		wraps = append(wraps, w)
	}
	p, err := PublishEnvelope(context.Background(), st, NewEnvelope(key, wraps, now), func(e Envelope) error {
		if e.KeyCheck != KeyCheck(key) {
			return errors.New("key check")
		}
		return nil
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestEnvelopesAreVerifiedBeforeTheyReplaceTheWorkingOne(t *testing.T) {
	st := NewMemoryStore()
	key := mustKey(t)
	t0 := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	p1 := publishEnv(t, st, key, t0, "alice")
	p2 := publishEnv(t, st, key, t0.Add(time.Hour), "alice", "bob")
	if p2.PreviousEnvelope != p1.Envelope {
		t.Fatalf("the previous envelope is kept as a fallback: %+v", p2)
	}
	p3 := publishEnv(t, st, key, t0.Add(2*time.Hour), "alice", "bob")
	envs, _ := st.List(context.Background(), envelopeDir)
	if len(envs) != 2 || p3.PreviousEnvelope != p2.Envelope {
		t.Fatalf("only the last two verified envelopes stay: %v", envs)
	}

	// A corrupted read-back is not published and the working one stays.
	st.Corrupt = func(k string, b []byte) []byte {
		if strings.HasPrefix(k, envelopeDir) && k != p3.Envelope && k != p2.Envelope {
			return append(b, ' ')
		}
		return b
	}
	w, _ := WrapKey(key, "alice", "alice-pass", fastKDF, t0)
	if _, err := PublishEnvelope(context.Background(), st, NewEnvelope(key, []Wrap{w}, t0), nil, t0.Add(3*time.Hour)); err == nil {
		t.Fatal("a corrupted envelope must not be published")
	}
	st.Corrupt = nil
	cur, _ := LoadPointer(context.Background(), st)
	if cur.Envelope != p3.Envelope {
		t.Fatalf("the working envelope must stay: %s", cur.Envelope)
	}
	if envs, _ := st.List(context.Background(), envelopeDir); len(envs) != 2 {
		t.Fatalf("the failed envelope must be removed: %v", envs)
	}
	// A verification failure is also refused.
	if _, err := PublishEnvelope(context.Background(), st, NewEnvelope(key, []Wrap{w}, t0), func(Envelope) error { return errors.New("no") }, t0.Add(4*time.Hour)); err == nil {
		t.Fatal("verify must gate the switch")
	}
	if cur, _ := LoadPointer(context.Background(), st); cur.Envelope != p3.Envelope {
		t.Fatal("the working envelope must stay")
	}
}

func TestSnapshotsArePublishedOnlyAfterVerificationAndRotated(t *testing.T) {
	ctx := context.Background()
	st := NewMemoryStore()
	key := mustKey(t)
	t0 := time.Date(2026, 9, 1, 3, 0, 0, 0, time.UTC)
	if _, err := PublishSnapshot(ctx, st, key, []byte("x"), SnapshotRef{}, DefaultRetention, t0); err == nil {
		t.Fatal("a snapshot no Owner can unlock must not be published")
	}
	publishEnv(t, st, key, t0, "alice")
	archive, err := BuildArchive(Manifest{Version: 1, SchemaLevel: 65, CreatedAt: t0}, map[string][]byte{DatabaseFile: []byte("dump")})
	if err != nil {
		t.Fatal(err)
	}
	var p *Pointer
	for d := 0; d < 40; d++ {
		p, err = PublishSnapshot(ctx, st, key, archive, SnapshotRef{SchemaLevel: 65}, DefaultRetention, t0.AddDate(0, 0, d))
		if err != nil {
			t.Fatal(err)
		}
	}
	// The last 7 days, plus the newest of each of the last 4 weeks.
	oldest, newest := p.Snapshots[0].CreatedAt, p.Snapshots[0].CreatedAt
	for _, s := range p.Snapshots {
		if s.CreatedAt.Before(oldest) {
			oldest = s.CreatedAt
		}
		if s.CreatedAt.After(newest) {
			newest = s.CreatedAt
		}
	}
	if n := len(p.Snapshots); n < 8 || n > 11 || newest.Sub(oldest) < 20*24*time.Hour || newest.Sub(oldest) > 28*24*time.Hour {
		t.Fatalf("7 daily and 4 weekly: %d snapshots over %s", n, newest.Sub(oldest))
	}
	objs, _ := st.List(ctx, snapshotDir)
	if len(objs) != len(p.Snapshots) {
		t.Fatalf("pruned snapshots must be deleted: %d objects, %d listed", len(objs), len(p.Snapshots))
	}
	if tmp, _ := st.List(ctx, tmpDir); len(tmp) != 0 {
		t.Fatalf("temporary objects must go: %v", tmp)
	}

	// Corruption on the way up keeps the published state.
	before := len(p.Snapshots)
	st.Corrupt = func(k string, b []byte) []byte {
		if strings.HasPrefix(k, tmpDir) {
			b[len(b)-1] ^= 1
		}
		return b
	}
	if _, err := PublishSnapshot(ctx, st, key, archive, SnapshotRef{}, DefaultRetention, t0.AddDate(0, 0, 41)); err == nil {
		t.Fatal("a snapshot that does not read back must not be published")
	}
	st.Corrupt = nil
	cur, _ := LoadPointer(ctx, st)
	if len(cur.Snapshots) != before {
		t.Fatal("the pointer must not change")
	}

	// Recovery: locate, unlock, fetch, read.
	loc, err := Locate(ctx, st)
	if err != nil {
		t.Fatal(err)
	}
	env, _ := LoadEnvelope(ctx, st, loc.Envelope)
	k, err := env.Unlock("alice", "alice-pass")
	if err != nil {
		t.Fatal(err)
	}
	plain, err := FetchSnapshot(ctx, st, k, *loc.Latest(env.KeyID))
	if err != nil {
		t.Fatal(err)
	}
	m, files, err := ReadArchive(plain)
	if err != nil || m.SchemaLevel != 65 || string(files[DatabaseFile]) != "dump" {
		t.Fatalf("archive %+v %v", m, err)
	}

	// Without a pointer, listing still finds everything.
	delete(st.Objs, PointerKey)
	loc, err = Locate(ctx, st)
	if err != nil || loc.Envelope == "" || len(loc.Snapshots) != before {
		t.Fatalf("locate by listing %+v %v", loc, err)
	}
}

func TestRotatedKeyDropsOldSnapshotsOnlyAfterANewOneIsIn(t *testing.T) {
	ctx := context.Background()
	st := NewMemoryStore()
	old := mustKey(t)
	t0 := time.Date(2026, 9, 1, 3, 0, 0, 0, time.UTC)
	publishEnv(t, st, old, t0, "alice")
	archive, _ := BuildArchive(Manifest{Version: 1, CreatedAt: t0}, nil)
	for d := 0; d < 3; d++ {
		if _, err := PublishSnapshot(ctx, st, old, archive, SnapshotRef{}, DefaultRetention, t0.AddDate(0, 0, d)); err != nil {
			t.Fatal(err)
		}
	}
	fresh := mustKey(t)
	p := publishEnv(t, st, fresh, t0.AddDate(0, 0, 3), "alice")
	if len(p.Snapshots) != 3 {
		t.Fatal("old snapshots stay until a new one is verified")
	}
	p, err := PublishSnapshot(ctx, st, fresh, archive, SnapshotRef{}, DefaultRetention, t0.AddDate(0, 0, 4))
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Snapshots) != 1 || p.Snapshots[0].KeyID != KeyID(fresh) {
		t.Fatalf("snapshots of the old key go: %+v", p.Snapshots)
	}
	if objs, _ := st.List(ctx, snapshotDir); len(objs) != 1 {
		t.Fatalf("old snapshot objects go: %v", objs)
	}
}

func TestArchiveRefusesUnsafeMembers(t *testing.T) {
	if _, err := BuildArchive(Manifest{}, map[string][]byte{"../etc/passwd": nil}); err == nil {
		t.Fatal("unsafe member")
	}
}

func mustJSON(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}
