package appdb

import (
	"context"
)

// Datastore is the locator row for an NFS/SMB/iSCSI pool. Pool UUID remains identity.
type Datastore struct {
	PoolID  string
	Kind    string
	Locator string
	Portal  string
	IQN     string
}

func (m *Memory) UpsertDatastore(_ context.Context, d Datastore) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.datastores == nil {
		m.datastores = map[string]Datastore{}
	}
	m.datastores[d.PoolID] = d
	return nil
}

func (m *Memory) GetDatastore(_ context.Context, poolID string) (*Datastore, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	d, ok := m.datastores[poolID]
	if !ok {
		return nil, nil
	}
	cp := d
	return &cp, nil
}

func (m *Memory) UpsertDatastoreSecret(_ context.Context, poolID, username, password string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.datastoreSecrets == nil {
		m.datastoreSecrets = map[string][2]string{}
	}
	m.datastoreSecrets[poolID] = [2]string{username, password}
	return nil
}

func (m *Memory) DatastoreSecret(_ context.Context, poolID string) (username, password string, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	sec, ok := m.datastoreSecrets[poolID]
	if !ok {
		return "", "", nil
	}
	return sec[0], sec[1], nil
}
