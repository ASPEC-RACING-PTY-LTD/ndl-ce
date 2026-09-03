package appdb

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

func TestUpdateRemoteNodeSessionFailsWhenMissing(t *testing.T) {
	m := NewMemory()
	err := m.UpdateRemoteNodeSession(context.Background(), RemoteNode{
		ID: uuid.NewString(), ClusterID: uuid.NewString(), Status: "Ready",
	})
	if err == nil || err.Error() != "remote node not found" {
		t.Fatalf("missing remote update: %v", err)
	}
}
