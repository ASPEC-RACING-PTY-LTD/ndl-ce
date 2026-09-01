package httpapi

import (
	"sync"

	"github.com/no-dal/ndl-ce/internal/appdb"
)

// EventHub fans platform events to stream subscribers.
type EventHub struct {
	mu   sync.Mutex
	subs map[chan appdb.Event]struct{}
}

// Publish delivers a copy to every subscriber without blocking the observe loop.
func (h *EventHub) Publish(e appdb.Event) {
	if h == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.subs {
		select {
		case ch <- e:
		default:
		}
	}
}

func (h *EventHub) subscribe() chan appdb.Event {
	if h == nil {
		return nil
	}
	ch := make(chan appdb.Event, 16)
	h.mu.Lock()
	if h.subs == nil {
		h.subs = map[chan appdb.Event]struct{}{}
	}
	h.subs[ch] = struct{}{}
	h.mu.Unlock()
	return ch
}

func (h *EventHub) unsubscribe(ch chan appdb.Event) {
	if h == nil || ch == nil {
		return
	}
	h.mu.Lock()
	delete(h.subs, ch)
	h.mu.Unlock()
	close(ch)
}
