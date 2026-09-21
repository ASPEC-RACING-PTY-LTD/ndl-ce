package httpapi

import (
	"context"
	"sync"

	"github.com/no-dal/ndl-ce/internal/appdb"
)

func (s *Server) acquireBackupSlot(ctx context.Context, clusterID, workloadID string) error {
	settings, _ := s.Store.GetBackupWorkspaceSettings(ctx, clusterID)
	slots := 1
	if settings != nil && settings.CaptureConcurrency > 0 {
		slots = settings.CaptureConcurrency
	}
	s.backupMu.Lock()
	if s.backupInflight == nil {
		s.backupInflight = map[string]struct{}{}
	}
	if s.backupWait == nil {
		s.backupWait = sync.NewCond(&s.backupMu)
	}
	stop := context.AfterFunc(ctx, func() {
		s.backupMu.Lock()
		if s.backupWait != nil {
			s.backupWait.Broadcast()
		}
		s.backupMu.Unlock()
	})
	defer stop()
	for {
		if _, busy := s.backupInflight[workloadID]; busy {
			s.backupMu.Unlock()
			return errConflict("a backup is already running for this workload")
		}
		if len(s.backupInflight) < slots {
			s.backupInflight[workloadID] = struct{}{}
			s.backupMu.Unlock()
			return nil
		}
		if err := ctx.Err(); err != nil {
			s.backupMu.Unlock()
			return err
		}
		s.backupWait.Wait()
	}
}

func (s *Server) releaseBackupSlot(workloadID string) {
	s.backupMu.Lock()
	delete(s.backupInflight, workloadID)
	if s.backupWait != nil {
		s.backupWait.Broadcast()
	}
	s.backupMu.Unlock()
}

func (s *Server) captureSlots(ctx context.Context, clusterID string) int {
	settings, _ := s.Store.GetBackupWorkspaceSettings(ctx, clusterID)
	if settings == nil {
		return appdb.DefaultBackupWorkspaceSettings(clusterID).CaptureConcurrency
	}
	if settings.CaptureConcurrency < 1 {
		return 1
	}
	return settings.CaptureConcurrency
}
