package migrate

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

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

// TestMigratePopulatedV2BackfillsOrQuarantinesLegacyTimers proves v3 migration
// on a populated v2 database explicitly handles legacy timer rows: parseable
// wait-until JSON is backfilled to due_at (discoverable), unparseable/absent is
// quarantined with due_at NULL (non-schedulable, never silently scheduled).
func TestMigratePopulatedV2BackfillsOrQuarantinesLegacyTimers(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "nore.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()

	migs := KVStoreMigrations(DriverSQLite)
	// Apply only v1 and v2 to simulate a populated pre-v3 database.
	if err := Migrate(ctx, Config{DB: db, Driver: DriverSQLite, Migrations: migs, MaxVersion: 2}); err != nil {
		t.Fatalf("migrate to v2: %v", err)
	}

	due := time.Now().Add(-time.Minute).UTC().Truncate(time.Second)
	withDue, _ := json.Marshal(map[string]any{"waitUntil": due.Format(time.RFC3339)})
	withoutDue, _ := json.Marshal(map[string]any{"foo": "bar"})
	if _, err := db.ExecContext(ctx, `INSERT INTO timer_store (key, value) VALUES (?, ?)`, "timers/legacy/backfillable", withDue); err != nil {
		t.Fatalf("insert backfillable: %v", err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO timer_store (key, value) VALUES (?, ?)`, "timers/legacy/quarantined", withoutDue); err != nil {
		t.Fatalf("insert quarantined: %v", err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO timer_store (key, value) VALUES (?, ?)`, "timers/legacy/plain", []byte("not-json")); err != nil {
		t.Fatalf("insert plain: %v", err)
	}

	// Upgrade to v3 (current max).
	if err := Migrate(ctx, Config{DB: db, Driver: DriverSQLite, Migrations: migs, MaxVersion: 3}); err != nil {
		t.Fatalf("migrate to v3: %v", err)
	}

	var backfilledDue sql.NullTime
	if err := db.QueryRowContext(ctx, `SELECT due_at FROM timer_store WHERE key = ?`, "timers/legacy/backfillable").Scan(&backfilledDue); err != nil {
		t.Fatalf("read backfilled: %v", err)
	}
	if !backfilledDue.Valid || !backfilledDue.Time.Equal(due) {
		t.Fatalf("backfilled due_at = %v (valid=%v), want %v", backfilledDue.Time, backfilledDue.Valid, due)
	}

	for _, key := range []string{"timers/legacy/quarantined", "timers/legacy/plain"} {
		var d sql.NullTime
		if err := db.QueryRowContext(ctx, `SELECT due_at FROM timer_store WHERE key = ?`, key).Scan(&d); err != nil {
			t.Fatalf("read %s: %v", key, err)
		}
		if d.Valid {
			t.Fatalf("legacy row %s should be quarantined with NULL due_at, got %v", key, d.Time)
		}
	}

	// Discoverability: only the explicitly backfilled row is schedulable; quarantined
	// rows remain non-schedulable (not silently invisible by accident but by decision).
	rows, err := db.QueryContext(ctx, `SELECT key FROM timer_store WHERE due_at IS NOT NULL AND due_at <= ? ORDER BY key`, time.Now().UTC())
	if err != nil {
		t.Fatalf("query schedulable: %v", err)
	}
	defer rows.Close()
	var keys []string
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			t.Fatalf("scan: %v", err)
		}
		keys = append(keys, k)
	}
	if len(keys) != 1 || keys[0] != "timers/legacy/backfillable" {
		t.Fatalf("schedulable legacy keys = %v, want [timers/legacy/backfillable]", keys)
	}

	// Idempotent re-run.
	if err := Migrate(ctx, Config{DB: db, Driver: DriverSQLite, Migrations: migs, MaxVersion: 3}); err != nil {
		t.Fatalf("migrate to v3 again: %v", err)
	}
}

func TestMigrateConcurrentStartupSQLite(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "nore.db")
	migs := KVStoreMigrations(DriverSQLite)
	maxv := int64(3)

	const n = 8
	start := make(chan struct{})
	errs := make(chan error, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// Each goroutine uses its own connection to simulate concurrent starters.
			db, err := sql.Open("sqlite", dbPath+"?_journal_mode=WAL&_busy_timeout=5000&_synchronous=NORMAL&_txlock=immediate")
			if err != nil {
				errs <- err
				return
			}
			db.SetMaxOpenConns(1)
			defer db.Close()
			<-start
			errs <- Migrate(ctx, Config{DB: db, Driver: DriverSQLite, Migrations: migs, MaxVersion: maxv})
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent migrate: %v", err)
		}
	}

	// Verify schema is intact and at expected version.
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open verify: %v", err)
	}
	defer db.Close()
	var version int64
	if err := db.QueryRowContext(ctx, `SELECT MAX(version) FROM schema_migrations`).Scan(&version); err != nil {
		t.Fatalf("read version: %v", err)
	}
	if version != maxv {
		t.Fatalf("schema version = %d, want %d", version, maxv)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO kv_store (key, value) VALUES ('k','v') ON CONFLICT(key) DO UPDATE SET value='v'`); err != nil {
		t.Fatalf("kv usable: %v", err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO timer_store (key, value, due_at) VALUES ('t', X'00', CURRENT_TIMESTAMP)`); err != nil {
		t.Fatalf("timer_store usable: %v", err)
	}
}
