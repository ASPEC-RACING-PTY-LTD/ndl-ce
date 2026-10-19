package engine

import (
	"sync"
	"time"
)

// Event is something that happened in No-dal that a flow can react to.
// CausedByRun is set when an automation's own action caused it.
type Event struct {
	ClusterID   string         `json:"cluster_id"`
	Kind        string         `json:"kind"`
	Target      string         `json:"target,omitempty"`
	NodeID      string         `json:"node_id,omitempty"`
	ActorID     string         `json:"actor_id,omitempty"`
	CausedByRun string         `json:"caused_by_run,omitempty"`
	Payload     map[string]any `json:"payload"`
	At          time.Time      `json:"at"`
}

// Bus is a small in-process event bus. Subscribers run on their own
// goroutine so publishers (request handlers) never wait on flows.
type Bus struct {
	mu     sync.Mutex
	subs   []func(Event)
	recent map[string][]Event
	// Sync delivers in the publisher's goroutine (tests).
	Sync bool
}

// recentPerKind is how many past events per kind are kept for test runs.
const recentPerKind = 20

// Subscribe adds a handler.
func (b *Bus) Subscribe(fn func(Event)) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.subs = append(b.subs, fn)
}

// Publish delivers ev to every subscriber.
func (b *Bus) Publish(ev Event) {
	if ev.At.IsZero() {
		ev.At = time.Now().UTC()
	}
	if ev.Payload == nil {
		ev.Payload = map[string]any{}
	}
	b.mu.Lock()
	if b.recent == nil {
		b.recent = map[string][]Event{}
	}
	key := ev.ClusterID + "|" + ev.Kind
	list := append(b.recent[key], Event{ClusterID: ev.ClusterID, Kind: ev.Kind, Target: ev.Target, NodeID: ev.NodeID, At: ev.At, Payload: Redact(ev.Payload).(map[string]any)})
	if len(list) > recentPerKind {
		list = list[len(list)-recentPerKind:]
	}
	b.recent[key] = list
	subs := append([]func(Event){}, b.subs...)
	b.mu.Unlock()
	for _, fn := range subs {
		if b.Sync {
			fn(ev)
			continue
		}
		go fn(ev)
	}
}

// Recent lists the newest events of a kind, newest first, for test runs
// with a real payload.
func (b *Bus) Recent(clusterID, kind string) []Event {
	b.mu.Lock()
	defer b.mu.Unlock()
	list := b.recent[clusterID+"|"+kind]
	out := make([]Event, 0, len(list))
	for i := len(list) - 1; i >= 0; i-- {
		out = append(out, list[i])
	}
	return out
}
