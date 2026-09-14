package migrate

import (
	"context"
	"fmt"
	"sync"
)

// Fake is an in-process migrate runtime for tests. Dest incoming is not a
// second running copy.
type Fake struct {
	mu            sync.Mutex
	FailLive      bool
	FailPrepare   bool
	FailCopy      bool
	FailStart     bool
	sourceRunning map[string]bool
	destIncoming  map[string]bool
	destRunning   map[string]bool
	Copies        []VolumeCopy
}

// NewFake returns an empty runtime. Call SetSourceRunning for a live guest.
func NewFake() *Fake {
	return &Fake{
		sourceRunning: map[string]bool{},
		destIncoming:  map[string]bool{},
		destRunning:   map[string]bool{},
	}
}

func (f *Fake) SetSourceRunning(id string, running bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.sourceRunning == nil {
		f.sourceRunning = map[string]bool{}
	}
	f.sourceRunning[id] = running
}

func (f *Fake) PrepareDest(_ context.Context, req Request) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.FailPrepare {
		return fmt.Errorf("dest prepare failed")
	}
	if f.destIncoming == nil {
		f.destIncoming = map[string]bool{}
	}
	f.destIncoming[req.WorkloadID] = true
	if f.destRunning == nil {
		f.destRunning = map[string]bool{}
	}
	f.destRunning[req.WorkloadID] = false
	return nil
}

func (f *Fake) CopyVolume(_ context.Context, vol VolumeCopy) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.FailCopy {
		return fmt.Errorf("volume copy failed")
	}
	f.Copies = append(f.Copies, vol)
	return nil
}

func (f *Fake) StopSource(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.sourceRunning == nil {
		f.sourceRunning = map[string]bool{}
	}
	f.sourceRunning[id] = false
	return nil
}

func (f *Fake) StartDest(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.FailStart {
		return fmt.Errorf("dest start failed")
	}
	if f.destIncoming != nil {
		delete(f.destIncoming, id)
	}
	if f.destRunning == nil {
		f.destRunning = map[string]bool{}
	}
	f.destRunning[id] = true
	if f.sourceRunning == nil {
		f.sourceRunning = map[string]bool{}
	}
	f.sourceRunning[id] = false
	return nil
}

func (f *Fake) LiveMigrate(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.FailLive {
		return fmt.Errorf("qmp migrate failed")
	}
	if f.destIncoming != nil {
		delete(f.destIncoming, id)
	}
	if f.destRunning == nil {
		f.destRunning = map[string]bool{}
	}
	f.destRunning[id] = true
	if f.sourceRunning == nil {
		f.sourceRunning = map[string]bool{}
	}
	f.sourceRunning[id] = false
	return nil
}

func (f *Fake) AbortDest(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.destIncoming != nil {
		delete(f.destIncoming, id)
	}
	if f.destRunning == nil {
		f.destRunning = map[string]bool{}
	}
	f.destRunning[id] = false
	return nil
}

func (f *Fake) SourceRunning(_ context.Context, id string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.sourceRunning == nil {
		return false
	}
	return f.sourceRunning[id]
}

func (f *Fake) DestRunning(id string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.destRunning[id]
}

func (f *Fake) DestIncoming(id string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.destIncoming[id]
}
