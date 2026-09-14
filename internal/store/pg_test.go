package store

import (
	"context"
	"database/sql"
	"os"
	"testing"

	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/no-dal/ndl-ce/migrations"
)

func TestApplyOnPostgres(t *testing.T) {
	dsn := os.Getenv("NODAL_TEST_PG")
	if dsn == "" {
		t.Skip("NODAL_TEST_PG not set")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Ping(); err != nil {
		t.Fatal(err)
	}
	files, err := List(migrations.FS, ".")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatal("no migrations")
	}
	adapter := SQLDB{DB: db}
	if err := Apply(context.Background(), adapter, files); err != nil {
		t.Fatal(err)
	}
	if err := Apply(context.Background(), adapter, files); err != nil {
		t.Fatal(err)
	}
}
