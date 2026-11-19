package migrate

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

// sqliteProductionDSN mirrors the production DSN suffix in
// nore/internal/storage/sqlite/sqlite.go:52. It is the declared SQLite
// serialization contract for migrations: immediate transactions reserve the
// write lock before a step reads the schema version, and the busy timeout
// bounds driver-level contention. TestMigrateConcurrentStartupSQLite proves
// deterministic convergence under exactly this contract.
const sqliteProductionDSN = "_journal_mode=WAL&_busy_timeout=5000&_synchronous=NORMAL&_txlock=immediate"

// legacyDueAtFixture is relative to process start so schedulability assertions
// never depend on a fixed wall-clock date at authoring time. It is computed
// once to keep fixture insertion and assertion identical across a test.
var legacyDueAtFixture = time.Now().UTC().Add(-time.Hour).Truncate(time.Second)

func legacyDueTime() time.Time { return legacyDueAtFixture }

var legacyFixtureKeys = []string{
	"timers/legacy/backfillable",
	"timers/legacy/quarantined",
	"timers/legacy/plain",
}

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

// TestMigratePopulatedV2BackfillsOrQuarantinesLegacyTimers proves the v3/v4
// split: v3 is DDL-only and leaves parseable legacy rows with due_at NULL, then
// v4 backfills/quarantines them. An existing non-NULL due_at is never replaced.
func TestMigratePopulatedV2BackfillsOrQuarantinesLegacyTimers(t *testing.T) {
	ctx := context.Background()
	db := openTestSQLite(t, filepath.Join(t.TempDir(), "nore.db"))
	defer db.Close()
	due := legacyDueTime()

	migs := KVStoreMigrations(DriverSQLite)
	if err := Migrate(ctx, Config{DB: db, Driver: DriverSQLite, Migrations: migs, MaxVersion: 2}); err != nil {
		t.Fatalf("migrate to v2: %v", err)
	}
	insertLegacyTimerFixtures(t, ctx, db, DriverSQLite, due)

	// v3 immutability guard: only DDL runs; legacy rows stay unbackfilled.
	if err := Migrate(ctx, Config{DB: db, Driver: DriverSQLite, Migrations: migs, MaxVersion: 3}); err != nil {
		t.Fatalf("migrate to v3: %v", err)
	}
	assertSchemaVersion(t, ctx, db, 3)
	for _, key := range legacyFixtureKeys {
		assertDueAt(t, ctx, db, DriverSQLite, key, false, time.Time{})
	}

	// A v3-era row with an explicit due time must survive v4 unchanged.
	preserved := due.Add(48 * time.Hour)
	insertTimerWithDueAt(t, ctx, db, DriverSQLite, "timers/legacy/preserved", []byte(`{"foo":"bar"}`), preserved)

	if err := Migrate(ctx, Config{DB: db, Driver: DriverSQLite, Migrations: migs, MaxVersion: 4}); err != nil {
		t.Fatalf("migrate to v4: %v", err)
	}
	assertSchemaVersion(t, ctx, db, 4)
	assertLegacyTimerBackfill(t, ctx, db, DriverSQLite, due)
	assertDueAt(t, ctx, db, DriverSQLite, "timers/legacy/preserved", true, preserved)

	if err := Migrate(ctx, Config{DB: db, Driver: DriverSQLite, Migrations: migs, MaxVersion: 4}); err != nil {
		t.Fatalf("migrate to v4 again: %v", err)
	}
	assertSchemaVersion(t, ctx, db, 4)
	assertLegacyTimerBackfill(t, ctx, db, DriverSQLite, due)
	assertDueAt(t, ctx, db, DriverSQLite, "timers/legacy/preserved", true, preserved)
}

// TestMigrateV3ToV4RepairsLegacyNullDueAt proves databases already recorded at
// v3 (DDL-only) are repaired by v4 without rerunning v3 semantics.
func TestMigrateV3ToV4RepairsLegacyNullDueAt(t *testing.T) {
	ctx := context.Background()
	db := openTestSQLite(t, filepath.Join(t.TempDir(), "nore.db"))
	defer db.Close()
	due := legacyDueTime()

	migs := KVStoreMigrations(DriverSQLite)
	if err := Migrate(ctx, Config{DB: db, Driver: DriverSQLite, Migrations: migs, MaxVersion: 3}); err != nil {
		t.Fatalf("migrate to v3: %v", err)
	}
	// Simulate legacy rows present while schema is already at v3 (DDL-only).
	insertTimerWithNullDueAt(t, ctx, db, DriverSQLite, "timers/legacy/backfillable",
		mustJSON(t, map[string]any{"waitUntil": due.Format(time.RFC3339)}))
	insertTimerWithNullDueAt(t, ctx, db, DriverSQLite, "timers/legacy/quarantined",
		mustJSON(t, map[string]any{"foo": "bar"}))
	assertSchemaVersion(t, ctx, db, 3)
	assertDueAt(t, ctx, db, DriverSQLite, "timers/legacy/backfillable", false, time.Time{})
	assertDueAt(t, ctx, db, DriverSQLite, "timers/legacy/quarantined", false, time.Time{})

	if err := Migrate(ctx, Config{DB: db, Driver: DriverSQLite, Migrations: migs, MaxVersion: 4}); err != nil {
		t.Fatalf("migrate to v4: %v", err)
	}
	assertSchemaVersion(t, ctx, db, 4)
	assertDueAt(t, ctx, db, DriverSQLite, "timers/legacy/backfillable", true, due)
	assertDueAt(t, ctx, db, DriverSQLite, "timers/legacy/quarantined", false, time.Time{})
}

// TestMigrateConcurrentStartupSQLite is the declared-contract test: it runs
// concurrent starters with exactly the production SQLite DSN (immediate
// transactions + busy timeout) and a single connection per handle. Convergence
// here is deterministic, not contention luck.
func TestMigrateConcurrentStartupSQLite(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "nore.db")
	migs := KVStoreMigrations(DriverSQLite)
	maxv := maxMigrationVersion(migs)

	const n = 8
	start := make(chan struct{})
	errs := make(chan error, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			db, err := sql.Open("sqlite", dbPath+"?"+sqliteProductionDSN)
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

// TestMigrateConcurrentStartupSQLiteDeferredDSN proves the unsupported DSN case
// fails explicitly instead of silently corrupting. With a plain/deferred DSN
// concurrent starters either converge, or return a bounded busy/exhaustion
// error. In every case the database remains integral and no migration is dirty.
func TestMigrateConcurrentStartupSQLiteDeferredDSN(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	dbPath := filepath.Join(t.TempDir(), "nore.db")
	migs := KVStoreMigrations(DriverSQLite)
	maxv := maxMigrationVersion(migs)

	const n = 6
	start := make(chan struct{})
	errs := make(chan error, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// Plain DSN: deferred transactions, default pool, no busy timeout.
			db, err := sql.Open("sqlite", dbPath)
			if err != nil {
				errs <- err
				return
			}
			defer db.Close()
			<-start
			errs <- Migrate(ctx, Config{DB: db, Driver: DriverSQLite, Migrations: migs, MaxVersion: maxv})
		}()
	}
	close(start)
	wg.Wait()
	close(errs)

	allConverged := true
	for err := range errs {
		if err == nil {
			continue
		}
		allConverged = false
		if !errors.Is(err, errSQLiteBusyExhausted) && !isSQLiteBusy(err) && !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("non-busy migration failure under deferred DSN (silent-corruption risk): %v", err)
		}
	}
	assertSQLiteMigrationIntegrity(t, context.Background(), dbPath, maxv)
	if allConverged {
		db := openTestSQLite(t, dbPath)
		defer db.Close()
		assertSchemaVersion(t, ctx, db, maxv)
	}
}

func TestExecAddColumnSQLite_ToleratesDuplicateColumn(t *testing.T) {
	ctx := context.Background()
	db := openTestSQLite(t, filepath.Join(t.TempDir(), "nore.db"))
	defer db.Close()
	if _, err := db.ExecContext(ctx, `CREATE TABLE timer_store (key TEXT PRIMARY KEY, due_at TIMESTAMP)`); err != nil {
		t.Fatalf("create fixture table: %v", err)
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer tx.Rollback()
	if err := execAddColumnSQLite(ctx, tx, `ALTER TABLE timer_store ADD COLUMN due_at TIMESTAMP`); err != nil {
		t.Fatalf("duplicate column must be tolerated: %v", err)
	}
}

func TestExecAddColumnSQLite_DoesNotSuppressUnrelatedErrors(t *testing.T) {
	ctx := context.Background()
	db := openTestSQLite(t, filepath.Join(t.TempDir(), "nore.db"))
	defer db.Close()
	if _, err := db.ExecContext(ctx, `CREATE TABLE timer_store (key TEXT PRIMARY KEY)`); err != nil {
		t.Fatalf("create fixture table: %v", err)
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer tx.Rollback()
	// This produces "table timer_store already exists": the message the old
	// broad matcher swallowed. It must now be surfaced.
	err = execAddColumnSQLite(ctx, tx, `CREATE TABLE timer_store (key TEXT PRIMARY KEY)`)
	if err == nil {
		t.Fatal("expected unrelated already-exists DDL failure to be surfaced")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "already exists") {
		t.Fatalf("expected an already-exists error to prove the narrow matcher, got %v", err)
	}
}

func TestIsSQLiteDuplicateColumnError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"duplicate column", errors.New("SQLITE_ERROR: duplicate column name: due_at (1)"), true},
		{"mixed case", errors.New("Duplicate Column Name: owner"), true},
		{"wrapped duplicate", fmt.Errorf("migrate: %w", errors.New("duplicate column name: owner")), true},
		{"table already exists", errors.New("table timer_store already exists"), false},
		{"index already exists", errors.New("index idx_timer_store_due_key already exists"), false},
		{"no such table", errors.New("no such table: timer_store"), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isSQLiteDuplicateColumnError(tt.err); got != tt.want {
				t.Fatalf("isSQLiteDuplicateColumnError(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

func TestParseLegacyTimerDueAt(t *testing.T) {
	sec := time.Unix(1_700_000_000, 0).UTC()
	secFrac := time.Unix(1_700_000_000, 250_000_000).UTC()
	millis := time.UnixMilli(1_700_000_000_123).UTC()
	nano := time.Date(2024, 3, 5, 6, 7, 8, 123456789, time.UTC)

	tests := []struct {
		name string
		json string
		want time.Time
		ok   bool
	}{
		{"rfc3339", `{"waitUntil":"2023-11-14T22:13:20Z"}`, sec, true},
		{"rfc3339nano", `{"waitUntil":"2024-03-05T06:07:08.123456789Z"}`, nano, true},
		{"numeric seconds", `{"waitUntil":1700000000}`, sec, true},
		{"numeric seconds fraction", `{"waitUntil":1700000000.25}`, secFrac, true},
		{"numeric milliseconds", `{"waitUntil":1700000000123}`, millis, true},
		{"field wait_until", `{"wait_until":"2023-11-14T22:13:20Z"}`, sec, true},
		{"field dueAt", `{"dueAt":1700000000}`, sec, true},
		{"field due_at", `{"due_at":1700000000}`, sec, true},
		{"empty string", `{"waitUntil":""}`, time.Time{}, false},
		{"invalid string", `{"waitUntil":"not-a-time"}`, time.Time{}, false},
		{"null field", `{"waitUntil":null}`, time.Time{}, false},
		{"zero", `{"waitUntil":0}`, time.Time{}, false},
		{"negative", `{"waitUntil":-5}`, time.Time{}, false},
		{"huge overflows int64", `{"waitUntil":1e30}`, time.Time{}, false},
		{"non-object array", `[1,2,3]`, time.Time{}, false},
		{"non-object string", `"2023-11-14T22:13:20Z"`, time.Time{}, false},
		{"non-object number", `1700000000`, time.Time{}, false},
		{"null document", `null`, time.Time{}, false},
		{"malformed json", `not-json`, time.Time{}, false},
		{"empty field falls through", `{"wait_until":"","due_at":"2023-11-14T22:13:20Z"}`, sec, true},
		{"unrelated field", `{"foo":"bar"}`, time.Time{}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := parseLegacyTimerDueAt([]byte(tt.json))
			if ok != tt.ok {
				t.Fatalf("parseLegacyTimerDueAt(%s) ok=%v, want %v (got %v)", tt.json, ok, tt.ok, got)
			}
			if ok && !got.Equal(tt.want) {
				t.Fatalf("parseLegacyTimerDueAt(%s) = %v, want %v", tt.json, got, tt.want)
			}
		})
	}
}

func TestLegacyBackfillUpdateSQLUsesDriverPlaceholders(t *testing.T) {
	pg := legacyBackfillUpdateSQL(DriverPostgres)
	if !strings.Contains(pg, "$1") || !strings.Contains(pg, "$2") || strings.Contains(pg, "?") {
		t.Fatalf("postgres backfill must use $1/$2 only, got %q", pg)
	}
	lite := legacyBackfillUpdateSQL(DriverSQLite)
	if !strings.Contains(lite, "?") || strings.Contains(lite, "$") {
		t.Fatalf("sqlite backfill must use ? only, got %q", lite)
	}
}

func TestIsSQLiteBusy(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"sqlite busy", errors.New("database is locked (5) (SQLITE_BUSY)"), true},
		{"snapshot busy", errors.New("database is locked (517) (SQLITE_BUSY_SNAPSHOT)"), true},
		{"shared cache locked", errors.New("database table is locked (6) (SQLITE_LOCKED)"), true},
		{"wrapped busy", fmt.Errorf("migrate: commit v1: %w", errors.New("database is locked (5) (SQLITE_BUSY)")), true},
		{"duplicate column", errors.New("duplicate column name: due_at"), false},
		{"no such table", errors.New("no such table: timer_store"), false},
		{"context canceled", context.Canceled, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isSQLiteBusy(tt.err); got != tt.want {
				t.Fatalf("isSQLiteBusy(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

func TestMigrateSQLiteWithRetry(t *testing.T) {
	ctx := context.Background()
	busy := errors.New("database is locked (5) (SQLITE_BUSY)")

	t.Run("busy then success", func(t *testing.T) {
		calls := 0
		err := migrateSQLiteWithRetry(ctx, time.Microsecond, func() (bool, error) {
			calls++
			if calls <= 3 {
				return false, busy
			}
			return false, nil
		})
		if err != nil {
			t.Fatalf("retry should converge: %v", err)
		}
		if calls != 4 {
			t.Fatalf("calls = %d, want 4", calls)
		}
	})

	t.Run("non busy passthrough", func(t *testing.T) {
		want := errors.New("boom")
		calls := 0
		err := migrateSQLiteWithRetry(ctx, time.Microsecond, func() (bool, error) {
			calls++
			return false, want
		})
		if !errors.Is(err, want) {
			t.Fatalf("error = %v, want %v", err, want)
		}
		if calls != 1 {
			t.Fatalf("calls = %d, want 1 (no retry on non-busy errors)", calls)
		}
	})

	t.Run("exhaustion is reachable", func(t *testing.T) {
		calls := 0
		err := migrateSQLiteWithRetry(ctx, time.Microsecond, func() (bool, error) {
			calls++
			return false, busy
		})
		if !errors.Is(err, errSQLiteBusyExhausted) {
			t.Fatalf("expected exhaustion, got %v", err)
		}
		if calls != sqliteMaxBusyRetries+1 {
			t.Fatalf("calls = %d, want %d", calls, sqliteMaxBusyRetries+1)
		}
	})

	t.Run("context cancellation", func(t *testing.T) {
		cancelCtx, cancel := context.WithCancel(ctx)
		calls := 0
		err := migrateSQLiteWithRetry(cancelCtx, time.Hour, func() (bool, error) {
			calls++
			cancel()
			return false, busy
		})
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected context cancellation, got %v", err)
		}
		if calls != 1 {
			t.Fatalf("calls = %d, want 1", calls)
		}
	})

	t.Run("budget resets after an applied step", func(t *testing.T) {
		calls := 0
		err := migrateSQLiteWithRetry(ctx, time.Microsecond, func() (bool, error) {
			calls++
			switch calls {
			case 1, 2:
				return false, busy
			case 3:
				return true, nil // applied: budget resets
			case 4:
				return false, busy
			default:
				return false, nil
			}
		})
		if err != nil {
			t.Fatalf("retry with reset should converge: %v", err)
		}
		if calls != 5 {
			t.Fatalf("calls = %d, want 5", calls)
		}
	})
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

func placeholder(drv Driver, n int) string {
	if drv == DriverPostgres {
		return fmt.Sprintf("$%d", n)
	}
	return "?"
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

// assertSQLiteMigrationIntegrity proves a deferred-DSN race never leaves a
// dirty or corrupt schema, even when some starters fail explicitly.
func assertSQLiteMigrationIntegrity(t *testing.T, ctx context.Context, dbPath string, maxv int64) {
	t.Helper()
	db := openTestSQLite(t, dbPath)
	defer db.Close()

	var result string
	if err := db.QueryRowContext(ctx, `PRAGMA integrity_check`).Scan(&result); err != nil {
		t.Fatalf("integrity_check: %v", err)
	}
	if result != "ok" {
		t.Fatalf("integrity_check = %q, want ok", result)
	}
	var hasTable int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='schema_migrations'`).Scan(&hasTable); err != nil {
		t.Fatalf("check schema_migrations: %v", err)
	}
	if hasTable == 0 {
		return // no migration recorded; no partial state to inspect
	}
	var dirty int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations WHERE dirty != 0`).Scan(&dirty); err != nil {
		t.Fatalf("check dirty rows: %v", err)
	}
	if dirty != 0 {
		t.Fatalf("dirty migration rows = %d, want 0", dirty)
	}
	var version int64
	if err := db.QueryRowContext(ctx, `SELECT COALESCE(MAX(version),0) FROM schema_migrations`).Scan(&version); err != nil {
		t.Fatalf("read recorded version: %v", err)
	}
	if version < 0 || version > maxv {
		t.Fatalf("recorded version %d outside [0,%d]", version, maxv)
	}
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}

func insertLegacyTimerFixtures(t *testing.T, ctx context.Context, db *sql.DB, drv Driver, due time.Time) {
	t.Helper()
	withDue := mustJSON(t, map[string]any{"waitUntil": due.Format(time.RFC3339)})
	withoutDue := mustJSON(t, map[string]any{"foo": "bar"})
	q := `INSERT INTO timer_store (key, value) VALUES (` + placeholder(drv, 1) + `, ` + placeholder(drv, 2) + `)`
	for _, fixture := range []struct {
		key   string
		value []byte
	}{
		{"timers/legacy/backfillable", withDue},
		{"timers/legacy/quarantined", withoutDue},
		{"timers/legacy/plain", []byte("not-json")},
	} {
		if _, err := db.ExecContext(ctx, q, fixture.key, fixture.value); err != nil {
			t.Fatalf("insert %s: %v", fixture.key, err)
		}
	}
}

func insertTimerWithNullDueAt(t *testing.T, ctx context.Context, db *sql.DB, drv Driver, key string, value []byte) {
	t.Helper()
	q := `INSERT INTO timer_store (key, value, due_at) VALUES (` + placeholder(drv, 1) + `, ` + placeholder(drv, 2) + `, NULL)`
	if _, err := db.ExecContext(ctx, q, key, value); err != nil {
		t.Fatalf("insert %s: %v", key, err)
	}
}

func insertTimerWithDueAt(t *testing.T, ctx context.Context, db *sql.DB, drv Driver, key string, value []byte, due time.Time) {
	t.Helper()
	q := `INSERT INTO timer_store (key, value, due_at) VALUES (` + placeholder(drv, 1) + `, ` + placeholder(drv, 2) + `, ` + placeholder(drv, 3) + `)`
	if _, err := db.ExecContext(ctx, q, key, value, due.UTC()); err != nil {
		t.Fatalf("insert %s: %v", key, err)
	}
}

func assertLegacyTimerBackfill(t *testing.T, ctx context.Context, db *sql.DB, drv Driver, due time.Time) {
	t.Helper()
	assertDueAt(t, ctx, db, drv, "timers/legacy/backfillable", true, due)
	assertDueAt(t, ctx, db, drv, "timers/legacy/quarantined", false, time.Time{})
	assertDueAt(t, ctx, db, drv, "timers/legacy/plain", false, time.Time{})

	rows, err := db.QueryContext(ctx,
		`SELECT key FROM timer_store WHERE due_at IS NOT NULL AND due_at <= `+placeholder(drv, 1)+` ORDER BY key`,
		time.Now().UTC())
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

func assertDueAt(t *testing.T, ctx context.Context, db *sql.DB, drv Driver, key string, wantValid bool, want time.Time) {
	t.Helper()
	var d sql.NullTime
	if err := db.QueryRowContext(ctx, `SELECT due_at FROM timer_store WHERE key = `+placeholder(drv, 1), key).Scan(&d); err != nil {
		t.Fatalf("read %s: %v", key, err)
	}
	if d.Valid != wantValid {
		t.Fatalf("key %s due_at valid=%v want %v (time=%v)", key, d.Valid, wantValid, d.Time)
	}
	if wantValid && !d.Time.UTC().Equal(want.UTC()) {
		t.Fatalf("key %s due_at=%v want %v", key, d.Time.UTC(), want.UTC())
	}
}
