package docker

import (
	"testing"
	"time"
)

func TestContainerHealthOOMAndRestartLoop(t *testing.T) {
	now := time.Now().UTC()
	c := Container{
		State:        "exited",
		OOMKilled:    true,
		ExitCode:     137,
		RestartCount: 4,
		RecentEvents: []Event{
			{Time: now.Add(-time.Minute), Action: "oom", Message: "oom", Severe: true},
			{Time: now.Add(-2 * time.Minute), Action: "die", Message: "die", Severe: true},
			{Time: now.Add(-3 * time.Minute), Action: "die", Message: "die", Severe: true},
		},
	}
	classifyContainer(&c, now)
	if c.Health != HealthCritical {
		t.Fatalf("health %s", c.Health)
	}
	if !c.RestartLoop && !c.OOMKilled {
		t.Fatal("expected oom or restart loop")
	}
}

func TestRunningUpdateFailedLabel(t *testing.T) {
	c := Container{State: "running", HealthStatus: "healthy", UpdateFailed: true, UpdateError: "pull failed"}
	classifyContainer(&c, time.Now().UTC())
	if c.StatusLabel != "Running · Update Failed" {
		t.Fatalf("label %q", c.StatusLabel)
	}
	if c.Health != HealthDegraded {
		t.Fatalf("health %s want degraded so the project rolls up", c.Health)
	}
}

func TestStoppedZeroIsHealthy(t *testing.T) {
	c := Container{State: "exited", ExitCode: 0}
	classifyContainer(&c, time.Now().UTC())
	if c.Health != HealthHealthy {
		t.Fatalf("health %s", c.Health)
	}
}

func TestUnhealthyHealthcheckIsCritical(t *testing.T) {
	c := Container{State: "running", HealthStatus: "unhealthy"}
	classifyContainer(&c, time.Now().UTC())
	if c.Health != HealthCritical {
		t.Fatalf("health %s", c.Health)
	}
	if c.StatusLabel != "Running · Unhealthy" {
		t.Fatalf("label %q", c.StatusLabel)
	}
}

func TestApplyUpdateNoteFromEvents(t *testing.T) {
	c := Container{
		Ref: "host/abc123abc123",
		RecentEvents: []Event{
			{Action: "pull", Type: "image", Message: "image pull failed: not found", Severe: true},
		},
	}
	(&Engine{}).applyUpdateNote(&c)
	if !c.UpdateFailed {
		t.Fatal("severe pull event must mark update failed")
	}
}

func TestCleanStopIsNotSevere(t *testing.T) {
	ev := classifyEvent("container", "die", "die", "abc", "exit 0", time.Now().UTC())
	if ev.Severe {
		t.Fatalf("clean stop should not be severe: %+v", ev)
	}
	bad := classifyEvent("container", "die", "die", "abc", "exit 137", time.Now().UTC())
	if !bad.Severe {
		t.Fatal("non-zero die must be severe")
	}
}

func TestRollupCriticalWins(t *testing.T) {
	h, reason := rollupHealth(
		[]string{HealthHealthy, HealthDegraded, HealthCritical},
		[]string{"ok", "degraded", "oom"},
	)
	if h != HealthCritical || reason != "oom" {
		t.Fatalf("%s %s", h, reason)
	}
}
