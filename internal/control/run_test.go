package control

import (
	"context"
	"fmt"
	"testing"

	"github.com/no-dal/ndl-ce/internal/appdb"
)

type failCertStore struct {
	appdb.Store
}

func (failCertStore) GetCertificate(context.Context, string) (*appdb.Certificate, error) {
	return nil, fmt.Errorf("db down")
}

func TestCertificateEnabledFailsClosed(t *testing.T) {
	ctx := context.Background()
	mem := appdb.NewMemory()
	if err := mem.CreateCluster(ctx, appdb.Cluster{ID: "c1", Name: "local"}); err != nil {
		t.Fatal(err)
	}
	ok, err := certificateEnabled(ctx, mem)
	if err != nil || ok {
		t.Fatalf("empty cert row: enabled=%v err=%v", ok, err)
	}
	_, err = certificateEnabled(ctx, failCertStore{mem})
	if err == nil {
		t.Fatal("unreadable certificate state must fail closed")
	}
}
