package migrate

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	_ "github.com/lib/pq"
)

// postgresMigrationTest isolates one DSN-gated migration test in its own
// PostgreSQL schema. Migrations run with search_path pinned to that schema via
// the connection string, so a new physical connection can never fall back to a
// shared public schema. The schema is dropped on cleanup.
type postgresMigrationTest struct {
	dsn    string
	schema string
}

// setupPostgresMigrationTest skips only when NEURON_TEST_POSTGRES_DSN is empty.
// When a DSN is supplied, connection or setup failures are fatal: a provided
// DSN must result in the test actually running, never a silent skip.
func setupPostgresMigrationTest(t *testing.T) (context.Context, *sql.DB, *postgresMigrationTest) {
	t.Helper()
	base := os.Getenv("NEURON_TEST_POSTGRES_DSN")
	if base == "" {
		t.Skip("not run — NEURON_TEST_POSTGRES_DSN unavailable (Postgres migration tests need a dedicated test database)")
	}
	ctx := context.Background()

	schema := fmt.Sprintf("neuron_migrate_test_%d_%d", os.Getpid(), time.Now().UnixNano())
	if !isSafeSQLIdentifier(schema) {
		t.Fatalf("generated unsafe schema identifier %q", schema)
	}

	admin, err := sql.Open("postgres", base)
	if err != nil {
		t.Fatalf("open postgres admin: %v", err)
	}
	defer admin.Close()
	if _, err := admin.ExecContext(ctx, `CREATE SCHEMA `+schema); err != nil {
		t.Fatalf("create test schema %s: %v", schema, err)
	}
	t.Cleanup(func() {
		cleanup, err := sql.Open("postgres", base)
		if err != nil {
			return
		}
		defer cleanup.Close()
		if _, err := cleanup.ExecContext(context.Background(), `DROP SCHEMA `+schema+` CASCADE`); err != nil {
			t.Logf("cleanup schema %s: %v", schema, err)
		}
	})

	env := &postgresMigrationTest{dsn: withSearchPath(base, schema), schema: schema}
	db, err := sql.Open("postgres", env.dsn)
	if err != nil {
		t.Fatalf("open scoped postgres: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.PingContext(ctx); err != nil {
		t.Fatalf("ping scoped postgres: %v", err)
	}
	return ctx, db, env
}

// withSearchPath pins search_path in the connection string so every pooled
// connection inherits the isolated schema, including connections opened during
// migration and advisory-lock handling.
func withSearchPath(dsn, schema string) string {
	if strings.HasPrefix(dsn, "postgres://") || strings.HasPrefix(dsn, "postgresql://") {
		if u, err := url.Parse(dsn); err == nil {
			q := u.Query()
			q.Set("search_path", schema)
			u.RawQuery = q.Encode()
			return u.String()
		}
	}
	var parts []string
	for _, part := range strings.Fields(dsn) {
		if strings.HasPrefix(strings.ToLower(part), "search_path=") {
			continue
		}
		parts = append(parts, part)
	}
	return strings.Join(append(parts, "search_path="+schema), " ")
}

func isSafeSQLIdentifier(s string) bool {
	if s == "" || (s[0] >= '0' && s[0] <= '9') {
		return false
	}
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' {
			continue
		}
		return false
	}
	return true
}

// TestMigratePostgresPopulatedV2ToV4 covers the full upgrade path on a
// populated v2 database: parseable legacy rows become schedulable, unparseable
// rows stay quarantined, and a second run is a no-op.
func TestMigratePostgresPopulatedV2ToV4(t *testing.T) {
	ctx, db, _ := setupPostgresMigrationTest(t)
	due := legacyDueTime()
	migs := KVStoreMigrations(DriverPostgres)

	if err := Migrate(ctx, Config{DB: db, Driver: DriverPostgres, Migrations: migs, MaxVersion: 2}); err != nil {
		t.Fatalf("migrate to v2: %v", err)
	}
	insertLegacyTimerFixtures(t, ctx, db, DriverPostgres, due)

	if err := Migrate(ctx, Config{DB: db, Driver: DriverPostgres, Migrations: migs, MaxVersion: 4}); err != nil {
		t.Fatalf("migrate to v4: %v", err)
	}
	assertSchemaVersion(t, ctx, db, 4)
	assertLegacyTimerBackfill(t, ctx, db, DriverPostgres, due)

	if err := Migrate(ctx, Config{DB: db, Driver: DriverPostgres, Migrations: migs, MaxVersion: 4}); err != nil {
		t.Fatalf("migrate to v4 again: %v", err)
	}
	assertSchemaVersion(t, ctx, db, 4)
	assertLegacyTimerBackfill(t, ctx, db, DriverPostgres, due)
}

// TestMigratePostgresV3ToV4Repair proves databases already recorded at v3 are
// repaired: legacy rows remain NULL at v3 (DDL-only) and v4 backfills/quarantines
// them without rerunning v3.
func TestMigratePostgresV3ToV4Repair(t *testing.T) {
	ctx, db, _ := setupPostgresMigrationTest(t)
	due := legacyDueTime()
	migs := KVStoreMigrations(DriverPostgres)

	if err := Migrate(ctx, Config{DB: db, Driver: DriverPostgres, Migrations: migs, MaxVersion: 3}); err != nil {
		t.Fatalf("migrate to v3: %v", err)
	}
	insertTimerWithNullDueAt(t, ctx, db, DriverPostgres, "timers/legacy/backfillable",
		mustJSON(t, map[string]any{"waitUntil": due.Format(time.RFC3339)}))
	insertTimerWithNullDueAt(t, ctx, db, DriverPostgres, "timers/legacy/quarantined",
		mustJSON(t, map[string]any{"foo": "bar"}))
	assertSchemaVersion(t, ctx, db, 3)
	assertDueAt(t, ctx, db, DriverPostgres, "timers/legacy/backfillable", false, time.Time{})
	assertDueAt(t, ctx, db, DriverPostgres, "timers/legacy/quarantined", false, time.Time{})

	if err := Migrate(ctx, Config{DB: db, Driver: DriverPostgres, Migrations: migs, MaxVersion: 4}); err != nil {
		t.Fatalf("migrate to v4: %v", err)
	}
	assertSchemaVersion(t, ctx, db, 4)
	assertDueAt(t, ctx, db, DriverPostgres, "timers/legacy/backfillable", true, due)
	assertDueAt(t, ctx, db, DriverPostgres, "timers/legacy/quarantined", false, time.Time{})
}

// TestMigratePostgresConcurrentStartup exercises the session advisory lock:
// several Migrate starters race on one schema and must all converge at v4
// without dirty/newer-schema errors.
func TestMigratePostgresConcurrentStartup(t *testing.T) {
	ctx, _, env := setupPostgresMigrationTest(t)
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	migs := KVStoreMigrations(DriverPostgres)
	maxv := maxMigrationVersion(migs)

	const n = 6
	start := make(chan struct{})
	errs := make(chan error, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			h, err := sql.Open("postgres", env.dsn)
			if err != nil {
				errs <- err
				return
			}
			defer h.Close()
			h.SetMaxOpenConns(1)
			<-start
			errs <- Migrate(ctx, Config{DB: h, Driver: DriverPostgres, Migrations: migs, MaxVersion: maxv})
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Errorf("concurrent postgres migrate: %v", err)
		}
	}

	verify, err := sql.Open("postgres", env.dsn)
	if err != nil {
		t.Fatalf("open verify: %v", err)
	}
	defer verify.Close()
	assertSchemaVersion(t, ctx, verify, maxv)
	if _, err := verify.ExecContext(ctx, `INSERT INTO kv_store (key, value) VALUES ('k', 'v')`); err != nil {
		t.Fatalf("kv_store usable: %v", err)
	}
	if _, err := verify.ExecContext(ctx, `INSERT INTO timer_store (key, value, due_at) VALUES ('t', '\x00', NOW())`); err != nil {
		t.Fatalf("timer_store usable: %v", err)
	}
}

// TestMigratePostgresIdempotent proves rerunning at the latest version is a
// no-op and leaves an already-scheduled timer untouched.
func TestMigratePostgresIdempotent(t *testing.T) {
	ctx, db, _ := setupPostgresMigrationTest(t)
	migs := KVStoreMigrations(DriverPostgres)
	maxv := maxMigrationVersion(migs)

	for i := 0; i < 2; i++ {
		if err := Migrate(ctx, Config{DB: db, Driver: DriverPostgres, Migrations: migs, MaxVersion: maxv}); err != nil {
			t.Fatalf("migrate run %d: %v", i+1, err)
		}
		assertSchemaVersion(t, ctx, db, maxv)
	}

	due := legacyDueTime()
	insertTimerWithDueAt(t, ctx, db, DriverPostgres, "timers/scheduled",
		mustJSON(t, map[string]any{"waitUntil": due.Format(time.RFC3339)}), due)
	if err := Migrate(ctx, Config{DB: db, Driver: DriverPostgres, Migrations: migs, MaxVersion: maxv}); err != nil {
		t.Fatalf("rerun after data: %v", err)
	}
	assertDueAt(t, ctx, db, DriverPostgres, "timers/scheduled", true, due)
}
