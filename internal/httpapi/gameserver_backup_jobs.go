package httpapi

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/no-dal/ndl-ce/internal/appdb"
	"github.com/no-dal/ndl-ce/internal/gameserver"
)

// A game server backup or restore copies the whole server folder, which
// takes many minutes for a large server (JvS is about 40 GB). The panel
// is usually reached through a tunnel that drops requests after 100
// seconds, so these run in the background: the request starts the job
// and returns, and the backups list reports how it is going.

// gameBackupJob is the latest backup or restore for one server.
type gameBackupJob struct {
	Op         string     `json:"op"` // backup | restore
	Name       string     `json:"name"`
	Running    bool       `json:"running"`
	StartedAt  time.Time  `json:"started_at"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
	Error      string     `json:"error,omitempty"`
}

type gameBackupJobs struct {
	mu   sync.Mutex
	jobs map[string]*gameBackupJob
	wg   sync.WaitGroup
}

var errGameJobBusy = errors.New("a backup or restore is already running for this server")

// job returns a copy of the server's latest job, or nil.
func (j *gameBackupJobs) job(serverID string) *gameBackupJob {
	j.mu.Lock()
	defer j.mu.Unlock()
	if cur := j.jobs[serverID]; cur != nil {
		cp := *cur
		return &cp
	}
	return nil
}

// start runs fn in the background unless a job is already running for
// the server. fn gets a context no request can cancel.
func (s *Server) startGameJob(serverID, op, name string, fn func(ctx context.Context) error) (*gameBackupJob, error) {
	j := &s.gameJobs
	j.mu.Lock()
	if cur := j.jobs[serverID]; cur != nil && cur.Running {
		j.mu.Unlock()
		return nil, errGameJobBusy
	}
	if j.jobs == nil {
		j.jobs = map[string]*gameBackupJob{}
	}
	job := &gameBackupJob{Op: op, Name: name, Running: true, StartedAt: s.now()}
	j.jobs[serverID] = job
	cp := *job
	j.mu.Unlock()

	j.wg.Add(1)
	go func() {
		defer j.wg.Done()
		err := fn(context.Background())
		j.mu.Lock()
		defer j.mu.Unlock()
		done := s.now()
		job.Running = false
		job.FinishedAt = &done
		if err != nil {
			job.Error = gameserver.HumanError(err.Error())
		}
	}()
	return &cp, nil
}

// waitGameJobs blocks until every background job has finished (tests).
func (s *Server) waitGameJobs() { s.gameJobs.wg.Wait() }

// noteGameCtx records a server event without a live request.
func (s *Server) noteGameCtx(ctx context.Context, p *principal, serverID, kind, summary, detail string) {
	_ = s.Store.InsertGameEvent(ctx, appdb.GameEvent{
		ID: uuid.NewString(), ServerID: serverID, ClusterID: p.User.ClusterID, UserID: p.User.ID,
		Kind: kind, Summary: summary, Detail: detail, CreatedAt: s.now(),
	})
}
