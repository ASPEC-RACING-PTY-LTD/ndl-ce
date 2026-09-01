package appdb

import (
	"context"
	"encoding/json"
	"sort"
	"time"
)

func (m *Memory) UpsertInventory(_ context.Context, row HardwareInventory) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.inventory == nil {
		m.inventory = map[string]HardwareInventory{}
	}
	m.inventory[row.NodeID] = row
	return nil
}

func (m *Memory) GetInventory(_ context.Context, nodeID string) (*HardwareInventory, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	row, ok := m.inventory[nodeID]
	if !ok {
		return nil, nil
	}
	cp := row
	return &cp, nil
}

func (m *Memory) MarkInventoryStale(_ context.Context, nodeID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	row, ok := m.inventory[nodeID]
	if !ok {
		return nil
	}
	row.Stale = true
	m.inventory[nodeID] = row
	return nil
}

func (m *Memory) InsertObservation(_ context.Context, o NodeObservation) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.observations = append(m.observations, o)
	return nil
}

func (m *Memory) UpsertOperation(_ context.Context, op Operation) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if op.UpdatedAt.IsZero() {
		op.UpdatedAt = time.Now().UTC()
	}
	if op.CreatedAt.IsZero() {
		op.CreatedAt = op.UpdatedAt
	}
	for i, existing := range m.operations {
		if op.IdempotencyKey != "" && existing.ClusterID == op.ClusterID && existing.IdempotencyKey == op.IdempotencyKey {
			op.ID = existing.ID
			op.CreatedAt = existing.CreatedAt
			m.operations[i] = op
			return nil
		}
		if existing.ID == op.ID {
			m.operations[i] = op
			return nil
		}
	}
	m.operations = append(m.operations, op)
	return nil
}

func (m *Memory) ListOperations(_ context.Context, clusterID string, limit int) ([]Operation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Operation
	for _, op := range m.operations {
		if op.ClusterID == clusterID {
			out = append(out, op)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].UpdatedAt.After(out[j].UpdatedAt)
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (m *Memory) InsertEvent(_ context.Context, e Event) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(e.Payload) == 0 {
		e.Payload = json.RawMessage(`{}`)
	}
	if e.CreatedAt.IsZero() {
		e.CreatedAt = time.Now().UTC()
	}
	m.events = append(m.events, e)
	return nil
}

func (m *Memory) ListEvents(_ context.Context, clusterID string, limit int) ([]Event, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Event
	for _, e := range m.events {
		if e.ClusterID == clusterID {
			out = append(out, e)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].CreatedAt.After(out[j].CreatedAt)
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}
