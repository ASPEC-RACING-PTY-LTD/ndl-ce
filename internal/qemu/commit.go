package qemu

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// commitTimeout bounds a live commit. The overlay only holds what the guest
// wrote while a backup ran, so this is generous.
const commitTimeout = 2 * time.Hour

// overlayCommit merges req.OverlayPath into req.BackingPath and points the
// disk back at the backing file. A running VM is committed live with QMP
// block-commit, so the guest keeps running; a stopped VM uses qemu-img commit.
// The overlay file is removed afterwards.
func (e *Engine) overlayCommit(ctx context.Context, req OverlayRequest) (OverlayResult, error) {
	if req.BackingPath == "" || req.OverlayPath == "" || req.OverlayPath == req.BackingPath {
		return OverlayResult{}, fmt.Errorf("commit needs an overlay and its backing file")
	}
	res := OverlayResult{WorkloadID: req.WorkloadID, OverlayPath: req.BackingPath, BackingPath: req.BackingPath, Mechanism: "qcow2-commit"}
	running := e.unitProvenStopped(ctx, req.WorkloadID) != nil && e.diskAttached(req.WorkloadID, req.OverlayPath)
	if running {
		if err := e.qmpActiveCommit(ctx, req.WorkloadID, req.OverlayPath, req.BackingPath); err != nil {
			return OverlayResult{}, fmt.Errorf("live commit: %w", err)
		}
		res.LiveQMP = true
	} else {
		if err := e.AssertDiskOffline(ctx, req.OverlayPath); err != nil {
			return OverlayResult{}, err
		}
		if e.SkipHostCmds {
			return OverlayResult{}, fmt.Errorf("qemu-img commit is unavailable")
		}
		out, err := exec.CommandContext(ctx, BinQEMUImg, "commit", "-q", req.OverlayPath).CombinedOutput()
		if err != nil {
			return OverlayResult{}, fmt.Errorf("qemu-img commit: %w: %s", err, strings.TrimSpace(string(out)))
		}
	}
	if err := e.retargetBootDisk(req.WorkloadID, req.BackingPath); err != nil {
		return OverlayResult{}, err
	}
	if err := os.Remove(req.OverlayPath); err != nil && !os.IsNotExist(err) {
		return res, fmt.Errorf("the overlay was merged but could not be removed: %w", err)
	}
	return res, nil
}

type qmpBlockNode struct {
	NodeName string `json:"node-name"`
	File     string `json:"file"`
	Drv      string `json:"drv"`
}

type qmpBlockJob struct {
	Device string `json:"device"`
	Ready  bool   `json:"ready"`
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
}

func (e *Engine) qmpActiveCommit(ctx context.Context, id, overlay, backing string) error {
	if e.SkipHostCmds {
		return fmt.Errorf("qmp is unavailable")
	}
	q, err := e.dialQMP(id, 3*time.Second)
	if err != nil {
		return err
	}
	defer q.Close()
	raw, err := q.exec("query-named-block-nodes", nil)
	if err != nil {
		return err
	}
	var nodes []qmpBlockNode
	if err := json.Unmarshal(raw, &nodes); err != nil {
		return err
	}
	top, base := commitNodes(nodes, overlay, backing)
	if top == "" || base == "" {
		return fmt.Errorf("the running VM does not have %s on top of %s", overlay, backing)
	}
	jobID := "ndl-commit-" + id
	if len(jobID) > 60 {
		jobID = jobID[:60]
	}
	if _, err := q.exec("block-commit", map[string]any{
		"job-id": jobID, "device": top, "base-node": base, "auto-dismiss": true,
	}); err != nil {
		return err
	}
	deadline := time.Now().Add(commitTimeout)
	completed := false
	for time.Now().Before(deadline) {
		if err := ctx.Err(); err != nil {
			_, _ = q.exec("block-job-cancel", map[string]any{"device": jobID})
			return err
		}
		raw, err := q.exec("query-block-jobs", nil)
		if err != nil {
			return err
		}
		var jobs []qmpBlockJob
		if err := json.Unmarshal(raw, &jobs); err != nil {
			return err
		}
		job := findJob(jobs, jobID)
		switch {
		case job == nil && completed:
			return nil
		case job == nil:
			return fmt.Errorf("commit job ended before it was completed")
		case job.Error != "":
			return fmt.Errorf("commit job: %s", job.Error)
		case job.Ready && !completed:
			if _, err := q.exec("block-job-complete", map[string]any{"device": jobID}); err != nil {
				return err
			}
			completed = true
		}
		time.Sleep(250 * time.Millisecond)
	}
	_, _ = q.exec("block-job-cancel", map[string]any{"device": jobID})
	return fmt.Errorf("commit did not finish within %s", commitTimeout)
}

// commitNodes finds the format nodes (not the protocol nodes, which report
// the same file) of the active overlay and its backing file.
func commitNodes(nodes []qmpBlockNode, overlay, backing string) (top, base string) {
	for _, n := range nodes {
		if n.NodeName == "" || n.Drv == "file" || n.Drv == "host_device" {
			continue
		}
		switch n.File {
		case overlay:
			top = n.NodeName
		case backing:
			base = n.NodeName
		}
	}
	return top, base
}

func findJob(jobs []qmpBlockJob, id string) *qmpBlockJob {
	for i := range jobs {
		if jobs[i].Device == id {
			return &jobs[i]
		}
	}
	return nil
}
