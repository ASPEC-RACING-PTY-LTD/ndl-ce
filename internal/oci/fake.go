package oci

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// FakeRuntime is the Cloud and unit-test runtime. It never talks to containerd.
type FakeRuntime struct {
	mu       sync.Mutex
	Images   map[string]string // image -> digest
	Running  map[string]bool
	PulledAs map[string]*RegistryCreds
	FailPull error
	Now      func() time.Time
}

func (f *FakeRuntime) now() time.Time {
	if f.Now != nil {
		return f.Now()
	}
	return time.Now().UTC()
}

func (f *FakeRuntime) ensure() {
	if f.Images == nil {
		f.Images = map[string]string{}
	}
	if f.Running == nil {
		f.Running = map[string]bool{}
	}
	if f.PulledAs == nil {
		f.PulledAs = map[string]*RegistryCreds{}
	}
}

// Pull records credentials usage and stores a digest. Passwords are kept only in memory for assertions.
func (f *FakeRuntime) Pull(_ context.Context, req PullRequest) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ensure()
	if f.FailPull != nil {
		return "", f.FailPull
	}
	if err := ValidateImageRef(req.Image); err != nil {
		return "", err
	}
	if req.Creds != nil {
		cp := *req.Creds
		f.PulledAs[req.Image] = &cp
	} else {
		f.PulledAs[req.Image] = nil
	}
	digest := "sha256:fake-" + req.Image
	f.Images[req.Image] = digest
	return digest, nil
}

func (f *FakeRuntime) Run(_ context.Context, spec Spec) error {
	if err := ValidateSpec(spec); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ensure()
	if _, ok := f.Images[spec.ImagePin]; !ok {
		return fmt.Errorf("image not pulled")
	}
	f.Running[spec.WorkloadID] = true
	return nil
}

func (f *FakeRuntime) Stop(_ context.Context, workloadID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ensure()
	f.Running[workloadID] = false
	return nil
}

func (f *FakeRuntime) Observe(_ context.Context, workloadID string) (Observed, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ensure()
	obs := Observed{
		WorkloadID: workloadID,
		Kind:       KindOCI,
		ObservedAt: f.now(),
		Health:     Health{Status: StatusNotConfigured, Message: "healthcheck not configured"},
	}
	if f.Running[workloadID] {
		obs.Status = StatusRunning
		obs.UnitActive = true
		obs.Health = Health{Status: StatusRunning, Message: "fake runtime healthy"}
		return obs, nil
	}
	if _, known := f.Running[workloadID]; known {
		obs.Status = StatusStopped
		obs.Health = Health{Status: StatusStopped, Message: "container stopped"}
		return obs, nil
	}
	obs.Status = StatusUnavailable
	obs.Reason = "workload not present in fake runtime"
	obs.Health = Health{Status: StatusUnavailable, Message: "runtime has no observation"}
	return obs, nil
}

func (f *FakeRuntime) Delete(_ context.Context, workloadID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ensure()
	delete(f.Running, workloadID)
	return nil
}

// LastPullCreds returns the credentials used for the last Pull of image, if any.
func (f *FakeRuntime) LastPullCreds(image string) *RegistryCreds {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.PulledAs[image]
}
