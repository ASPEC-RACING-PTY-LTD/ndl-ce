package appdb

import (
	"context"
	"database/sql"
	"os"
	"testing"

	"github.com/google/uuid"
	_ "github.com/jackc/pgx/v5/stdlib"
)

// NULLIF($n,0) lets Postgres infer int4 from the literal, so memory >= 2 GiB
// failed to encode ("2147483648 is greater than maximum value for int4") on
// every edit of a running LXC, even though the rest of the change applied.
func TestUpdateWorkloadSpecAcceptsMemoryAbove2GiB(t *testing.T) {
	dsn := os.Getenv("NODAL_TEST_PG")
	if dsn == "" {
		t.Skip("NODAL_TEST_PG not set")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	p := &Postgres{DB: db}
	for _, mem := range []int64{2 << 30, 64 << 30} {
		w := Workload{ID: uuid.NewString(), CPUs: 2, MemoryBytes: mem}
		if err := p.UpdateWorkloadSpec(context.Background(), w); err != nil {
			t.Fatalf("UpdateWorkloadSpec(memory=%d): %v", mem, err)
		}
	}
}
