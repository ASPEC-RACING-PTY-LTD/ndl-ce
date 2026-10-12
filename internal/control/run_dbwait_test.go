package control

import (
	"context"
	"errors"
	"testing"
	"time"
)

type flakyDB struct{ fails, calls int }

func (f *flakyDB) PingContext(context.Context) error {
	f.calls++
	if f.calls <= f.fails {
		return errors.New("connection refused")
	}
	return nil
}

func TestWaitForDBRetriesUntilReady(t *testing.T) {
	db := &flakyDB{fails: 2}
	if err := waitForDB(db, 10*time.Second); err != nil {
		t.Fatal(err)
	}
	if db.calls != 3 {
		t.Fatalf("calls = %d", db.calls)
	}
}

func TestWaitForDBGivesUp(t *testing.T) {
	db := &flakyDB{fails: 1 << 30}
	if err := waitForDB(db, 300*time.Millisecond); err == nil {
		t.Fatal("expected error")
	}
}
