package migrate

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

// TestMigrateEmptySQLite applies the full migration set to an empty SQLite database
// and asserts the resulting schema is usable by the contract suite. After migration,
// a basic KV write confirms the schema is functional.
func TestMigrateEmptySQLite(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "nore.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()
	migs := KVStoreMigrations(DriverSQLite)
	maxv := int64(0)
	for _, m := range migs {
		if m.Version > maxv {
			maxv = m.Version
		}
	}
	if err := Migrate(ctx, Config{DB: db, Driver: DriverSQLite, Migrations: migs, MaxVersion: maxv}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO kv_store (key, value) VALUES ('k','v')`); err != nil {
		t.Fatalf("insert: %v", err)
	}
}

func TestMigrateIdempotent(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "nore.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()
	migs := KVStoreMigrations(DriverSQLite)
	maxv := int64(0)
	for _, m := range migs {
		if m.Version > maxv {
			maxv = m.Version
		}
	}
	if err := Migrate(ctx, Config{DB: db, Driver: DriverSQLite, Migrations: migs, MaxVersion: maxv}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if err := Migrate(ctx, Config{DB: db, Driver: DriverSQLite, Migrations: migs, MaxVersion: maxv}); err != nil {
		t.Fatalf("migrate again: %v", err)
	}
}

func TestMigrateRefuseNewerSchema(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "nore.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()
	migs := KVStoreMigrations(DriverSQLite)
	if err := Migrate(ctx, Config{DB: db, Driver: DriverSQLite, Migrations: migs, MaxVersion: 5}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if err := Migrate(ctx, Config{DB: db, Driver: DriverSQLite, Migrations: migs, MaxVersion: 0}); err == nil {
		t.Fatal("expected error for newer schema")
	} else if !errors.Is(err, ErrNewerSchema) {
		t.Fatalf("expected ErrNewerSchema, got %v", err)
	}
}
