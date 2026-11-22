package httpapi

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/no-dal/ndl-ce/internal/appdb"
)

func TestGameScheduleParsing(t *testing.T) {
	mel, _ := time.LoadLocation("Australia/Melbourne")
	g, err := parseGameSchedule("TZ=Australia/Melbourne 0 0,12 * * *")
	if err != nil {
		t.Fatal(err)
	}
	// 11:30 Melbourne: next is noon the same day, then midnight.
	at := time.Date(2026, 11, 23, 11, 30, 0, 0, mel)
	n1 := g.next(at)
	n2 := g.next(n1)
	if n1.In(mel).Hour() != 12 || n2.In(mel).Hour() != 0 || n2.In(mel).Day() != 24 {
		t.Fatalf("next %v then %v", n1.In(mel), n2.In(mel))
	}
	for _, ok := range []string{"nightly", "hourly", "twice-daily", "0 4 * * *", "TZ=UTC weekly"} {
		if _, err := parseGameSchedule(ok); err != nil {
			t.Errorf("%q: %v", ok, err)
		}
	}
	for _, bad := range []string{"sometimes", "0 25 * * *", "TZ=Nowhere/Place 0 0 * * *"} {
		if _, err := parseGameSchedule(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestGameSchedulesRunOnceAtTheirTime(t *testing.T) {
	s, _, id, call, _ := gmodTestServer(t, nil)
	mem := s.Store
	cl, _ := mem.GetCluster(context.Background())
	if code, _ := call("POST", "/api/v1/game-servers/"+id+"/schedules", `{"name":"x","action":"backup","cron":"sometimes"}`); code != http.StatusBadRequest {
		t.Fatalf("invalid schedule %d", code)
	}
	created := time.Date(2026, 11, 23, 0, 30, 0, 0, time.UTC)
	sch := appdb.GameSchedule{ID: "sch1", ServerID: id, Name: "Twice", Action: "backup", Cron: "TZ=UTC 0 0,12 * * *", Enabled: true, CreatedAt: created}
	if err := mem.CreateGameSchedule(context.Background(), sch); err != nil {
		t.Fatal(err)
	}
	backups := func() int {
		s.waitGameJobs()
		b, _ := mem.ListGameBackups(context.Background(), id)
		return len(b)
	}
	clock := created.Add(time.Hour) // 01:30, not due
	s.Now = func() time.Time { return clock }
	s.TickGameSchedules(context.Background())
	if n := backups(); n != 0 {
		t.Fatalf("ran early: %d", n)
	}
	clock = time.Date(2026, 11, 23, 12, 0, 10, 0, time.UTC) // just past noon
	s.TickGameSchedules(context.Background())
	if n := backups(); n != 1 {
		t.Fatalf("noon run: %d backups", n)
	}
	s.TickGameSchedules(context.Background())
	if n := backups(); n != 1 {
		t.Fatalf("ran twice for one time: %d", n)
	}
	// Down over midnight and noon: one catch-up run, not two.
	clock = time.Date(2026, 11, 24, 13, 0, 0, 0, time.UTC)
	s.TickGameSchedules(context.Background())
	s.TickGameSchedules(context.Background())
	if n := backups(); n != 2 {
		t.Fatalf("catch-up: %d backups", n)
	}
	_ = cl
	s.Now = nil // the session was signed with the real clock
	_, out := call("GET", "/api/v1/game-servers/"+id+"/schedules", "")
	items, _ := out["items"].([]any)
	var found map[string]any
	for _, it := range items {
		if m := it.(map[string]any); m["id"] == "sch1" {
			found = m
		}
	}
	if found == nil || found["next_run"] == nil || found["last_run"] == nil {
		t.Fatalf("schedule list %v", out)
	}
}
