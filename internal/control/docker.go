package control

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/no-dal/ndl-ce/internal/docker"
	"github.com/no-dal/ndl-ce/internal/features"
	"github.com/no-dal/ndl-ce/internal/lxc"
)

func (o observer) reconcileDocker(ctx context.Context, clusterID, nodeID string) {
	row, _ := o.Store.GetFeature(ctx, clusterID, features.IDDocker)
	enabled := row != nil && row.Enabled
	if !enabled {
		if o.lastHealth != nil {
			if _, was := o.lastHealth["docker.enabled"]; was {
				delete(o.lastHealth, "docker.enabled")
				_ = o.Agent.DockerIdle(ctx)
			}
		}
		if o.dockerFP != nil {
			delete(o.dockerFP, "inv")
		}
		return
	}
	if o.lastHealth != nil {
		o.lastHealth["docker.enabled"] = time.Now()
	}
	wls, err := o.Store.ListWorkloads(ctx, clusterID)
	if err != nil {
		return
	}
	var hints []docker.MachineHint
	for _, w := range wls {
		if w.Kind != lxc.KindSystemContainer {
			continue
		}
		hints = append(hints, docker.MachineHint{ID: w.ID, Name: w.Name, Kind: lxc.KindSystemContainer})
	}
	inv, err := o.Agent.DockerSnapshot(ctx, hints)
	if err != nil {
		o.emitThrottled(ctx, clusterID, nodeID, "docker.unavailable", map[string]string{"detail": err.Error()})
		return
	}
	fp := dockerHealthFingerprint(inv)
	if o.dockerFP != nil && o.dockerFP["inv"] == fp {
		return
	}
	if o.dockerFP != nil {
		o.dockerFP["inv"] = fp
	}
	o.emit(ctx, clusterID, nodeID, "docker.inventory", map[string]string{
		"machines":      strconv.Itoa(inv.Summary.Machines),
		"containers":    strconv.Itoa(inv.Summary.Containers),
		"critical":      strconv.Itoa(inv.Summary.Critical),
		"degraded":      strconv.Itoa(inv.Summary.Degraded),
		"daemons_down":  strconv.Itoa(inv.Summary.DaemonsDown),
		"update_failed": strconv.Itoa(inv.Summary.UpdateFailed),
	})
}

func dockerHealthFingerprint(inv docker.Inventory) string {
	var b strings.Builder
	for _, m := range inv.Machines {
		b.WriteString(m.ID)
		b.WriteByte(':')
		b.WriteString(m.Health)
		b.WriteByte(';')
	}
	for _, c := range inv.Containers {
		b.WriteString(c.Ref)
		b.WriteByte(':')
		b.WriteString(c.Health)
		b.WriteByte('/')
		b.WriteString(c.StatusLabel)
		b.WriteByte(';')
	}
	return b.String()
}
