package migrate

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

// TestMigrateEmptySQLite applies the full migration set to an empty SQLite database
// and asserts the resulting schema is usable. After migration, a basic KV and
// timer write confirms the schema is functional.
func TestMigrateEmptySQLite(t *testing.T) {
	ctx := context.Background()
	db := openTestSQLite(t, filepath.Join(t.TempDir(), "nore.db"))
	defer db.Close()

	migs := KVStoreMigrations(DriverSQLite)
	maxv := maxMigrationVersion(migs)
	if err := Migrate(ctx, Config{DB: db, Driver: DriverSQLite, Migrations: migs, MaxVersion: maxv}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	assertSchemaVersion(t, ctx, db, maxv)
	if _, err := db.ExecContext(ctx, `INSERT INTO kv_store (key, value) VALUES ('k','v')`); err != nil {
		t.Fatalf("insert kv: %v", err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO timer_store (key, value, due_at) VALUES ('t', X'00', CURRENT_TIMESTAMP)`); err != nil {
		t.Fatalf("insert timer: %v", err)
	}
}

func TestMigrateIdempotent(t *testing.T) {
	ctx := context.Background()
	db := openTestSQLite(t, filepath.Join(t.TempDir(), "nore.db"))
	defer db.Close()

	migs := KVStoreMigrations(DriverSQLite)
	maxv := maxMigrationVersion(migs)
	if err := Migrate(ctx, Config{DB: db, Driver: DriverSQLite, Migrations: migs, MaxVersion: maxv}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if err := Migrate(ctx, Config{DB: db, Driver: DriverSQLite, Migrations: migs, MaxVersion: maxv}); err != nil {
		t.Fatalf("migrate again: %v", err)
	}
	assertSchemaVersion(t, ctx, db, maxv)
}

func TestMigrateRefuseNewerSchema(t *testing.T) {
	ctx := context.Background()
	db := openTestSQLite(t, filepath.Join(t.TempDir(), "nore.db"))
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

// TestMigratePopulatedV2BackfillsOrQuarantinesLegacyTimers proves v4 migration
// repairs a populated v2 database: parseable wait-until JSON is backfilled to
// due_at (discoverable), unparseable/absent is quarantined with due_at NULL
// (non-schedulable). v3 remains DDL-only.
func TestMigratePopulatedV2BackfillsOrQuarantinesLegacyTimers(t *testing.T) {
	ctx := context.Background()
	db := openTestSQLite(t, filepath.Join(t.TempDir(), "nore.db"))
	defer db.Close()

	migs := KVStoreMigrations(DriverSQLite)
	if err := Migrate(ctx, Config{DB: db, Driver: DriverSQLite, Migrations: migs, MaxVersion: 2}); err != nil {
		t.Fatalf("migrate to v2: %v", err)
	}
	insertLegacyTimerFixtures(t, ctx, db)

	if err := Migrate(ctx, Config{DB: db, Driver: DriverSQLite, Migrations: migs, MaxVersion: 4}); err != nil {
		t.Fatalf("migrate to v4: %v", err)
	}
	assertSchemaVersion(t, ctx, db, 4)
	assertLegacyTimerBackfill(t, ctx, db)

	if err := Migrate(ctx, Config{DB: db, Driver: DriverSQLite, Migrations: migs, MaxVersion: 4}); err != nil {
		t.Fatalf("migrate to v4 again: %v", err)
	}
	assertLegacyTimerBackfill(t, ctx, db)
}

// TestMigrateV3ToV4RepairsLegacyNullDueAt proves databases already recorded at
// v3 (DDL-only) are repaired by v4 without rerunning v3 semantics.
func TestMigrateV3ToV4RepairsLegacyNullDueAt(t *testing.T) {
	ctx := context.Background()
	db := openTestSQLite(t, filepath.Join(t.TempDir(), "nore.db"))
	defer db.Close()

	migs := KVStoreMigrations(DriverSQLite)
	if err := Migrate(ctx, Config{DB: db, Driver: DriverSQLite, Migrations: migs, MaxVersion: 3}); err != nil {
		t.Fatalf("migrate to v3: %v", err)
	}
	// Simulate legacy rows present while schema is already at v3 (DDL-only).
	if _, err := db.ExecContext(ctx, `INSERT INTO timer_store (key, value, due_at) VALUES (?, ?, NULL)`, "timers/legacy/backfillable", mustJSON(t, map[string]any{"waitUntil": legacyDueTime().Format(time.RFC3339)})); err != nil {
		t.Fatalf("insert backfillable: %v", err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO timer_store (key, value, due_at) VALUES (?, ?, NULL)`, "timers/legacy/quarantined", mustJSON(t, map[string]any{"foo": "bar"})); err != nil {
		t.Fatalf("insert quarantined: %v", err)
	}
	assertSchemaVersion(t, ctx, db, 3)

	if err := Migrate(ctx, Config{DB: db, Driver: DriverSQLite, Migrations: migs, MaxVersion: 4}); err != nil {
		t.Fatalf("migrate to v4: %v", err)
	}
	assertSchemaVersion(t, ctx, db, 4)
	assertDueAt(t, ctx, db, "timers/legacy/backfillable", true, legacyDueTime())
	assertDueAt(t, ctx, db, "timers/legacy/quarantined", false, time.Time{})
}

func TestMigrateConcurrentStartupSQLite(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "nore.db")
	migs := KVStoreMigrations(DriverSQLite)
	maxv := int64(4)

	const n = 8
	start := make(chan struct{})
	errs := make(chan error, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
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

	db := openTestSQLite(t, dbPath)
	defer db.Close()
	assertSchemaVersion(t, ctx, db, maxv)
	if _, err := db.ExecContext(ctx, `INSERT INTO kv_store (key, value) VALUES ('k','v') ON CONFLICT(key) DO UPDATE SET value='v'`); err != nil {
		t.Fatalf("kv usable: %v", err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO timer_store (key, value, due_at) VALUES ('t', X'00', CURRENT_TIMESTAMP)`); err != nil {
		t.Fatalf("timer_store usable: %v", err)
	}
}

func TestExecAddColumnSQLite_DoesNotSuppressUnrelatedErrors(t *testing.T) {
	ctx := context.Background()
	db := openTestSQLite(t, filepath.Join(t.TempDir(), "nore.db"))
	defer db.Close()

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer tx.Rollback()
	// Table does not exist: must not be treated as duplicate-column/idempotent.
	err = execAddColumnSQLite(ctx, tx, `ALTER TABLE missing_table ADD COLUMN due_at TIMESTAMP`)
	if err == nil {
		t.Fatal("expected error for unrelated DDL failure")
	}
	if strings.Contains(strings.ToLower(err.Error()), "duplicate column") {
		t.Fatalf("unexpected duplicate-column classification: %v", err)
	}
}

func TestMigratePostgresDSNGated(t *testing.T) {
	dsn := os.Getenv("NEURON_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("not run — NEURON_TEST_POSTGRES_DSN unavailable")
	}
	t.Skip("NEURON_TEST_POSTGRES_DSN set but DSN-gated migration cases require a dedicated test database; not implemented in this lease")
}

func openTestSQLite(t *testing.T, path string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	return db
}

func maxMigrationVersion(migs []Migration) int64 {
	maxv := int64(0)
	for _, m := range migs {
		if m.Version > maxv {
			maxv = m.Version
		}
	}
	return maxv
}

func assertSchemaVersion(t *testing.T, ctx context.Context, db *sql.DB, want int64) {
	t.Helper()
	var version int64
	if err := db.QueryRowContext(ctx, `SELECT MAX(version) FROM schema_migrations`).Scan(&version); err != nil {
		t.Fatalf("read version: %v", err)
	}
	if version != want {
		t.Fatalf("schema version = %d, want %d", version, want)
	}
}

func legacyDueTime() time.Time {
	return time.Date(2026, 11, 17, 0, 0, 0, 0, time.UTC)
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}

func insertLegacyTimerFixtures(t *testing.T, ctx context.Context, db *sql.DB) {
	t.Helper()
	withDue := mustJSON(t, map[string]any{"waitUntil": legacyDueTime().Format(time.RFC3339)})
	withoutDue := mustJSON(t, map[string]any{"foo": "bar"})
	if _, err := db.ExecContext(ctx, `INSERT INTO timer_store (key, value) VALUES (?, ?)`, "timers/legacy/backfillable", withDue); err != nil {
		t.Fatalf("insert backfillable: %v", err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO timer_store (key, value) VALUES (?, ?)`, "timers/legacy/quarantined", withoutDue); err != nil {
		t.Fatalf("insert quarantined: %v", err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO timer_store (key, value) VALUES (?, ?)`, "timers/legacy/plain", []byte("not-json")); err != nil {
		t.Fatalf("insert plain: %v", err)
	}
}

func assertLegacyTimerBackfill(t *testing.T, ctx context.Context, db *sql.DB) {
	t.Helper()
	assertDueAt(t, ctx, db, "timers/legacy/backfillable", true, legacyDueTime())
	assertDueAt(t, ctx, db, "timers/legacy/quarantined", false, time.Time{})
	assertDueAt(t, ctx, db, "timers/legacy/plain", false, time.Time{})

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
	if err := rows.Err(); err != nil {
		t.Fatalf("rows err: %v", err)
	}
	if len(keys) != 1 || keys[0] != "timers/legacy/backfillable" {
		t.Fatalf("schedulable legacy keys = %v, want [timers/legacy/backfillable]", keys)
	}
}

func assertDueAt(t *testing.T, ctx context.Context, db *sql.DB, key string, wantValid bool, want time.Time) {
	t.Helper()
	var d sql.NullTime
	if err := db.QueryRowContext(ctx, `SELECT due_at FROM timer_store WHERE key = ?`, key).Scan(&d); err != nil {
		t.Fatalf("read %s: %v", key, err)
	}
	if d.Valid != wantValid {
		t.Fatalf("key %s due_at valid=%v want %v (time=%v)", key, d.Valid, wantValid, d.Time)
	}
	if wantValid && !d.Time.UTC().Equal(want.UTC()) {
		t.Fatalf("key %s due_at=%v want %v", key, d.Time.UTC(), want.UTC())
	}
}
