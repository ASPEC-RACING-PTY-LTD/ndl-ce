package docker

import (
	"fmt"
	"strings"
	"time"
)

const restartLoopWindow = 10 * time.Minute

func classifyContainer(c *Container, now time.Time) {
	if c == nil {
		return
	}
	c.RestartLoop = restartLoop(c, now)
	c.Problems = problemsFor(c)
	c.Health, c.HealthReason = containerHealth(*c)
	c.StatusLabel = statusLabel(*c)
}

func restartLoop(c *Container, now time.Time) bool {
	if c.Restarting && c.RestartCount >= 3 {
		return true
	}
	if c.RestartCount >= 8 {
		return true
	}
	var deaths int
	for _, ev := range c.RecentEvents {
		if now.Sub(ev.Time) > restartLoopWindow {
			continue
		}
		a := strings.ToLower(ev.Action)
		if a == "die" || a == "oom" || strings.Contains(strings.ToLower(ev.Message), "oom") {
			deaths++
		}
	}
	return deaths >= 3
}

func problemsFor(c *Container) []Problem {
	var out []Problem
	add := func(kind, level, msg string) {
		if strings.TrimSpace(msg) == "" {
			return
		}
		out = append(out, Problem{Kind: kind, Level: level, Message: msg})
	}
	if c.Dead {
		add("dead", HealthCritical, "Container is dead")
	}
	if c.OOMKilled {
		add("oom", HealthCritical, "Killed by the OOM killer")
	}
	if c.RestartLoop {
		add("restart_loop", HealthCritical, fmt.Sprintf("Restart loop: %d recorded restarts", c.RestartCount))
	}
	if strings.EqualFold(c.HealthStatus, "unhealthy") {
		add("healthcheck", HealthCritical, "Docker healthcheck is unhealthy")
	}
	if c.Restarting {
		add("restarting", HealthDegraded, "Container is restarting")
	}
	if c.State == "exited" && c.ExitCode != 0 {
		add("exit", HealthDegraded, fmt.Sprintf("Exited with code %d", c.ExitCode))
	}
	if strings.TrimSpace(c.Error) != "" {
		add("engine", HealthDegraded, c.Error)
	}
	if c.UpdateFailed {
		msg := c.UpdateError
		if msg == "" {
			msg = "Image pull or recreate failed"
		}
		add("update", HealthDegraded, msg)
	}
	for _, ev := range c.RecentEvents {
		if !ev.Severe {
			continue
		}
		kind := eventKind(ev)
		add(kind, HealthDegraded, ev.Message)
	}
	return out
}

func containerHealth(c Container) (string, string) {
	if c.Dead || c.OOMKilled || c.RestartLoop || strings.EqualFold(c.HealthStatus, "unhealthy") {
		if c.OOMKilled {
			return HealthCritical, "OOM killed"
		}
		if c.RestartLoop {
			return HealthCritical, "Restart loop"
		}
		if strings.EqualFold(c.HealthStatus, "unhealthy") {
			return HealthCritical, "Healthcheck failed"
		}
		return HealthCritical, "Container is dead"
	}
	if c.Restarting || strings.EqualFold(c.HealthStatus, "starting") {
		return HealthDegraded, "Starting or restarting"
	}
	if c.State == "exited" || c.State == "stopped" {
		if c.ExitCode != 0 {
			return HealthDegraded, fmt.Sprintf("Stopped, exit %d", c.ExitCode)
		}
		return HealthHealthy, "Stopped"
	}
	if c.State == "paused" {
		return HealthDegraded, "Paused"
	}
	if c.State == "created" || c.State == "removing" {
		return HealthDegraded, titleState(c.State)
	}
	if c.State == "running" {
		if c.UpdateFailed {
			return HealthDegraded, "Running, update failed"
		}
		for _, p := range c.Problems {
			if p.Kind == "mount" || p.Kind == "network" || p.Kind == "engine" {
				return HealthDegraded, p.Message
			}
		}
		if strings.EqualFold(c.HealthStatus, "healthy") {
			return HealthHealthy, "Healthy"
		}
		return HealthHealthy, "Running"
	}
	return HealthUnknown, titleState(c.State)
}

func statusLabel(c Container) string {
	base := titleState(c.State)
	if c.State == "running" && strings.EqualFold(c.HealthStatus, "unhealthy") {
		base = "Running · Unhealthy"
	} else if c.State == "running" && strings.EqualFold(c.HealthStatus, "healthy") {
		base = "Running"
	} else if c.OOMKilled {
		base = "OOM Killed"
	}
	if c.UpdateFailed {
		return base + " · Update Failed"
	}
	if c.RestartLoop && c.State != "running" {
		return base + " · Restart Loop"
	}
	return base
}

func titleState(state string) string {
	s := strings.TrimSpace(strings.ToLower(state))
	switch s {
	case "running":
		return "Running"
	case "exited", "stopped":
		return "Stopped"
	case "restarting":
		return "Restarting"
	case "dead":
		return "Dead"
	case "paused":
		return "Paused"
	case "created":
		return "Created"
	case "removing":
		return "Removing"
	case "":
		return "Unknown"
	default:
		return strings.ToUpper(s[:1]) + s[1:]
	}
}

func rollupHealth(items []string, reasons []string) (string, string) {
	hasCritical, hasDegraded, hasHealthy := false, false, false
	var critReason, degReason string
	for i, h := range items {
		switch h {
		case HealthCritical:
			hasCritical = true
			if critReason == "" && i < len(reasons) {
				critReason = reasons[i]
			}
		case HealthDegraded:
			hasDegraded = true
			if degReason == "" && i < len(reasons) {
				degReason = reasons[i]
			}
		case HealthHealthy:
			hasHealthy = true
		}
	}
	if hasCritical {
		if critReason == "" {
			critReason = "Critical containers"
		}
		return HealthCritical, critReason
	}
	if hasDegraded {
		if degReason == "" {
			degReason = "Degraded containers"
		}
		return HealthDegraded, degReason
	}
	if hasHealthy {
		return HealthHealthy, "Healthy"
	}
	return HealthUnknown, "No containers"
}

func eventKind(ev Event) string {
	a := strings.ToLower(ev.Action + " " + ev.Type + " " + ev.Message)
	switch {
	case strings.Contains(a, "oom"):
		return "oom"
	case strings.Contains(a, "pull") || strings.Contains(a, "image"):
		return "update"
	case strings.Contains(a, "mount") || strings.Contains(a, "volume"):
		return "mount"
	case strings.Contains(a, "network"):
		return "network"
	case strings.Contains(a, "health"):
		return "healthcheck"
	default:
		return "event"
	}
}

func classifyEvent(typ, action, status, actor, extra string, ts time.Time) Event {
	msg := strings.TrimSpace(strings.Join([]string{action, extra}, " "))
	if msg == "" {
		msg = status
	}
	ev := Event{Time: ts, Type: typ, Action: action, Status: status, Actor: actor, Message: msg}
	low := strings.ToLower(typ + " " + action + " " + status + " " + extra)
	ev.Severe = strings.Contains(low, "error") ||
		strings.Contains(low, "fail") ||
		strings.Contains(low, "oom") ||
		strings.Contains(low, "unhealthy") ||
		strings.Contains(low, "unmount") ||
		strings.Contains(low, "not found")
	if strings.EqualFold(action, "die") {
		code := strings.TrimSpace(strings.TrimPrefix(strings.ToLower(extra), "exit "))
		if code != "" && code != "0" {
			ev.Severe = true
		}
	}
	return ev
}

func summarize(inv *Inventory) {
	if inv == nil {
		return
	}
	inv.Summary = Summary{}
	seenProj := map[string]struct{}{}
	for i := range inv.Machines {
		m := &inv.Machines[i]
		inv.Summary.Machines++
		if !m.DaemonOK {
			inv.Summary.DaemonsDown++
		}
		for _, p := range m.Projects {
			if _, ok := seenProj[p.ID]; !ok {
				seenProj[p.ID] = struct{}{}
				inv.Summary.Projects++
			}
		}
		switch m.Health {
		case HealthCritical:
			inv.Summary.Critical++
		case HealthDegraded:
			inv.Summary.Degraded++
		case HealthHealthy:
			inv.Summary.Healthy++
		}
	}
	for _, c := range inv.Containers {
		inv.Summary.Containers++
		if c.State == "running" {
			inv.Summary.Running++
		} else {
			inv.Summary.Stopped++
		}
		if c.UpdateFailed {
			inv.Summary.UpdateFailed++
		}
	}
}
