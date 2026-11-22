package httpapi

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/no-dal/ndl-ce/internal/appdb"
	"github.com/no-dal/ndl-ce/internal/automation/flowdef"
	"github.com/no-dal/ndl-ce/internal/gameserver"
)

// Game server schedules (Backups & Schedules): back up, restart, start,
// stop or send a console command when a cron line says so. A line is a
// standard five-field cron (minute hour day month weekday), or one of the
// names below, optionally starting with TZ=Area/City; without it the
// host's time zone applies. A run missed while the control plane was down
// happens once when it is back, not once per missed time.

var gameScheduleAliases = map[string]string{
	"hourly":      "0 * * * *",
	"nightly":     "0 3 * * *",
	"daily":       "0 0 * * *",
	"midnight":    "0 0 * * *",
	"twice-daily": "0 0,12 * * *",
	"weekly":      "0 3 * * 0",
}

// gameSchedule is a parsed schedule line.
type gameSchedule struct {
	cron *flowdef.Cron
	loc  *time.Location
}

func parseGameSchedule(line string) (gameSchedule, error) {
	line = strings.TrimSpace(line)
	loc := time.Local
	if rest, ok := strings.CutPrefix(line, "TZ="); ok {
		zone, expr, _ := strings.Cut(rest, " ")
		l, err := time.LoadLocation(zone)
		if err != nil {
			return gameSchedule{}, fmt.Errorf("unknown time zone %q", zone)
		}
		loc, line = l, strings.TrimSpace(expr)
	}
	if alias, ok := gameScheduleAliases[strings.ToLower(line)]; ok {
		line = alias
	}
	c, err := flowdef.ParseCron(line)
	if err != nil {
		return gameSchedule{}, fmt.Errorf("the schedule is not valid: %v (try nightly, hourly, twice-daily or a cron line such as 0 4 * * *)", err)
	}
	return gameSchedule{cron: c, loc: loc}, nil
}

// next is the first run after t, in the schedule's time zone.
func (g gameSchedule) next(t time.Time) time.Time {
	return g.cron.Next(t.In(g.loc))
}

// gameScheduleTick is how often schedules are checked.
const gameScheduleTick = 30 * time.Second

// RunGameSchedules runs schedules until ctx ends.
func (s *Server) RunGameSchedules(ctx context.Context) {
	started := s.now()
	t := time.NewTicker(gameScheduleTick)
	defer t.Stop()
	for {
		s.tickGameSchedules(ctx, started, false)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// TickGameSchedules checks every schedule once and waits for what it
// starts (tests).
func (s *Server) TickGameSchedules(ctx context.Context) {
	s.tickGameSchedules(ctx, time.Time{}, true)
}

// tickGameSchedules starts every schedule that is due. A schedule that
// never ran counts from when it was made, or when this process started,
// whichever is later, so turning the runner on does not fire old times.
func (s *Server) tickGameSchedules(ctx context.Context, started time.Time, wait bool) {
	if s.Store == nil {
		return
	}
	cl, err := s.Store.GetCluster(ctx)
	if err != nil || cl == nil {
		return
	}
	rows, err := s.Store.ListGameServers(ctx, cl.ID)
	if err != nil {
		return
	}
	now := s.now()
	for i := range rows {
		row := rows[i]
		schedules, err := s.Store.ListGameSchedules(ctx, row.ID)
		if err != nil {
			continue
		}
		for _, sch := range schedules {
			if !sch.Enabled {
				continue
			}
			g, err := parseGameSchedule(sch.Cron)
			if err != nil {
				continue
			}
			ref := sch.LastRun
			if ref.IsZero() {
				ref = sch.CreatedAt
				if started.After(ref) {
					ref = started
				}
			}
			if due := g.next(ref); due.IsZero() || due.After(now) {
				continue
			}
			sch.LastRun = now
			if err := s.Store.UpdateGameSchedule(ctx, sch); err != nil {
				log.Printf("game schedule %s: %v", sch.ID, err)
				continue
			}
			if wait {
				s.runGameSchedule(row, sch)
			} else {
				go s.runGameSchedule(row, sch)
			}
		}
	}
}

// runGameSchedule does one schedule's action and records how it went.
func (s *Server) runGameSchedule(row appdb.GameServer, sch appdb.GameSchedule) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Hour)
	defer cancel()
	note := func(summary, detail string) {
		_ = s.Store.InsertGameEvent(ctx, appdb.GameEvent{ID: uuid.NewString(), ServerID: row.ID, ClusterID: row.ClusterID, Kind: "schedule", Summary: summary, Detail: clipGame(detail, 800), CreatedAt: s.now()})
	}
	cur, err := s.Store.GetGameServer(ctx, row.ClusterID, row.ID)
	if err != nil || cur == nil {
		return
	}
	if gameInstalling(cur) {
		note("Schedule "+sch.Name+" skipped: the server is installing", "")
		return
	}
	running := cur.Status == gameserver.StatusRunning || cur.Status == gameserver.StatusStarting
	host := s.gameHost()
	switch sch.Action {
	case "backup":
		name := "Scheduled: " + sch.Name
		srv := *cur
		_, err := s.startGameJob(cur.ID, "backup", name, func(ctx context.Context) error {
			b, err := s.snapshotBackup(ctx, srv, name, "scheduled")
			if err != nil {
				note("Schedule "+sch.Name+": backup failed", gameserver.HumanError(err.Error()))
				return err
			}
			note("Schedule "+sch.Name+": backup created", b.Name)
			return nil
		})
		if err != nil {
			note("Schedule "+sch.Name+" skipped: "+err.Error(), "")
		}
		return
	case "command":
		if !running {
			note("Schedule "+sch.Name+" skipped: the server is not running", "")
			return
		}
		if err := host.Send(ctx, cur.ID, sch.Payload); err != nil {
			note("Schedule "+sch.Name+": command failed", gameserver.HumanError(err.Error()))
			return
		}
		note("Schedule "+sch.Name+": command sent", sch.Payload)
		return
	case "stop":
		if !running {
			return
		}
		_ = host.StopGraceful(ctx, cur.ID, s.gameStopCommand(ctx, cur), s.gameStopTimeout(ctx, cur))
		cur.Status, cur.DesiredPower, cur.ContainerID = gameserver.StatusStopped, "stopped", ""
		_ = s.Store.UpdateGameServer(ctx, *cur)
		note("Schedule "+sch.Name+": stopped", "")
		return
	case "start", "restart":
		if sch.Action == "start" && running {
			return
		}
		if sch.Action == "restart" {
			if !running {
				note("Schedule "+sch.Name+" skipped: the server is not running", "")
				return
			}
			_ = host.StopGraceful(ctx, cur.ID, s.gameStopCommand(ctx, cur), s.gameStopTimeout(ctx, cur))
		}
		if rerr := s.gameRuntimeReady(ctx); rerr != nil {
			note("Schedule "+sch.Name+" failed", gameserver.HumanError(rerr.Error()))
			return
		}
		preparing, err := s.startOrPrepare(ctx, host, cur)
		if err != nil {
			cur.Status = gameserver.StatusFailed
			cur.ErrorRaw, cur.ErrorHuman = err.Error(), gameserver.HumanError(err.Error())
			_ = s.Store.UpdateGameServer(ctx, *cur)
			note("Schedule "+sch.Name+": "+sch.Action+" failed", cur.ErrorHuman)
			return
		}
		if !preparing {
			cur.ErrorHuman, cur.ErrorRaw = "", ""
			_ = s.Store.UpdateGameServer(ctx, *cur)
		}
		note("Schedule "+sch.Name+": "+sch.Action+" done", "")
	}
}

// gameScheduleNext is when a schedule runs next, for the list.
func gameScheduleNext(sch appdb.GameSchedule, now time.Time) *time.Time {
	if !sch.Enabled {
		return nil
	}
	g, err := parseGameSchedule(sch.Cron)
	if err != nil {
		return nil
	}
	n := g.next(now)
	if n.IsZero() {
		return nil
	}
	return &n
}
