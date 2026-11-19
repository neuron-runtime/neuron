package migrate

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"hash/fnv"
	"sort"
	"strings"
	"time"
)

var (
	// ErrNewerSchema indicates the database schema version is newer than
	// this binary supports. The version policy requires refusing to start
	// in this case (do not downgrade).
	ErrNewerSchema = errors.New("storage/migrate: schema version newer than supported")

	// ErrDirty indicates a migration was left in a dirty/incomplete state.
	ErrDirty = errors.New("storage/migrate: migration is dirty")

	// ErrNoMigrations indicates no migrations were provided to the runner.
	ErrNoMigrations = errors.New("storage/migrate: no migrations registered")

	// errSQLiteBusyExhausted reports that a migration step kept losing the
	// SQLite write lock until its bounded per-step retry budget was spent. The
	// step transaction was rolled back, so no partial migration was recorded.
	errSQLiteBusyExhausted = errors.New("migrate: sqlite busy: retries exhausted")
)

// Migration represents one schema change.
type Migration struct {
	Version     int64
	Description string
	Up          func(context.Context, *sql.Tx) error
}

// Driver identifies the SQL dialect for driver-specific behavior.
type Driver string

const (
	DriverSQLite   Driver = "sqlite"
	DriverPostgres Driver = "postgres"
)

// NormalizeDriver normalizes driver names to the migrate package's forms.
func NormalizeDriver(d string) Driver {
	switch strings.ToLower(strings.TrimSpace(d)) {
	case "sqlite":
		return DriverSQLite
	case "postgres", "postgresql":
		return DriverPostgres
	default:
		return Driver(strings.ToLower(strings.TrimSpace(d)))
	}
}

// Config configures migration execution.
//
// SQLite callers must open DB with an immediate-transaction DSN: for
// modernc.org/sqlite that is _txlock=immediate together with a bounded
// _busy_timeout, and SetMaxOpenConns(1). This matches the production DSN in
// nore/internal/storage/sqlite. The migration runner cannot force the
// transaction mode because Migration.Up receives a *sql.Tx whose BEGIN is
// issued by database/sql at BeginTx time; only the opening DSN controls it.
// See Migrate for the concurrency contract and failure behavior.
type Config struct {
	DB         *sql.DB
	Driver     Driver
	Migrations []Migration
	MaxVersion int64
	TableName  string
}

// Migrate ensures the schema is up to date.
//
// Concurrent startup is serialized per driver. PostgreSQL holds a session
// advisory lock on a pinned connection for the whole run and applies each
// migration in its own transaction (preserving per-step atomicity).
//
// SQLite applies one migration per transaction and retries bounded SQLITE_BUSY
// contention. Deterministic serialization is a caller DSN contract: DB must be
// opened with immediate transactions (_txlock=immediate for modernc.org/sqlite),
// a bounded _busy_timeout, and SetMaxOpenConns(1), as production does. With a
// deferred/plain DSN the runner cannot reserve the write lock before a step
// reads the version; steps still roll back on write contention and the retry is
// bounded, so concurrent starters either converge or Migrate returns an
// explicit busy/exhaustion error. Deferred DSNs are unsupported for concurrent
// startup safety and must never be treated as serialized.
//
// Migration steps and their ADD COLUMN operations are idempotent and narrowly
// duplicate-tolerant so a raced or partially-applied step can be retried safely.
func Migrate(ctx context.Context, cfg Config) error {
	if cfg.DB == nil {
		return fmt.Errorf("migrate: db is required")
	}
	if len(cfg.Migrations) == 0 {
		return ErrNoMigrations
	}
	table := cfg.TableName
	if table == "" {
		table = "schema_migrations"
	}

	migs := make([]Migration, len(cfg.Migrations))
	copy(migs, cfg.Migrations)
	sort.Slice(migs, func(i, j int) bool { return migs[i].Version < migs[j].Version })

	switch cfg.Driver {
	case DriverPostgres:
		return migratePostgres(ctx, cfg, table, migs)
	case DriverSQLite:
		return migrateSQLite(ctx, cfg, table, migs)
	default:
		// Best-effort fallback: no driver-specific lock, but idempotent steps.
		return migrateSQLite(ctx, cfg, table, migs)
	}
}

const migrationAdvisoryLockKey int64 = 8291047130942213611

func migrationLockID(table string) int64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte("neuron/storage/migrate:" + table))
	v := int64(h.Sum64())
	if v == 0 {
		return migrationAdvisoryLockKey
	}
	return v
}

func migratePostgres(ctx context.Context, cfg Config, table string, migs []Migration) error {
	conn, err := cfg.DB.Conn(ctx)
	if err != nil {
		return fmt.Errorf("migrate: acquire connection: %w", err)
	}
	defer conn.Close()

	lockID := migrationLockID(table)
	if _, err := conn.ExecContext(ctx, `SELECT pg_advisory_lock($1)`, lockID); err != nil {
		return fmt.Errorf("migrate: acquire advisory lock: %w", err)
	}
	defer func() {
		// Always release on the same session/connection; use a background context
		// so shutdown/cancellation cannot leak the session lock.
		_, _ = conn.ExecContext(context.Background(), `SELECT pg_advisory_unlock($1)`, lockID)
	}()

	if err := ensureMigrationsTable(ctx, conn, cfg.Driver, table); err != nil {
		return err
	}

	current, dirty, err := getCurrentVersion(ctx, conn, cfg.Driver, table)
	if err != nil {
		return err
	}
	if dirty {
		return ErrDirty
	}
	if cfg.MaxVersion >= 0 && current > cfg.MaxVersion {
		return fmt.Errorf("%w: db version %d > supported max %d", ErrNewerSchema, current, cfg.MaxVersion)
	}

	target := cfg.MaxVersion
	for _, m := range migs {
		if target >= 0 && m.Version > target {
			continue
		}
		if m.Version <= current {
			continue
		}
		if err := applyMigration(ctx, conn, cfg.Driver, table, m); err != nil {
			return err
		}
	}
	return nil
}

const (
	// sqliteMaxBusyRetries is the per-step SQLite write-lock retry budget. It
	// resets after each successfully applied step, so the overall bound is
	// (number of migrations + 1) * (sqliteMaxBusyRetries + 1) attempts.
	sqliteMaxBusyRetries = 50
	sqliteBusyRetryDelay = 20 * time.Millisecond
)

func migrateSQLite(ctx context.Context, cfg Config, table string, migs []Migration) error {
	return migrateSQLiteWithRetry(ctx, sqliteBusyRetryDelay, func() (bool, error) {
		return migrateSQLiteStep(ctx, cfg, table, migs)
	})
}

// migrateSQLiteWithRetry drives one-at-a-time migration steps. Each step is
// atomic; a non-busy error is returned unchanged and a busy error is retried
// only within a bounded per-step budget. The budget resets after a step applies
// a migration so forward progress is capped by the migration count and ctx.
func migrateSQLiteWithRetry(ctx context.Context, retryDelay time.Duration, step func() (bool, error)) error {
	busyRetries := 0
	for {
		applied, err := step()
		if err == nil {
			if !applied {
				return nil
			}
			busyRetries = 0
			continue
		}
		if !isSQLiteBusy(err) {
			return err
		}
		if busyRetries == sqliteMaxBusyRetries {
			return fmt.Errorf("%w: %d retries", errSQLiteBusyExhausted, sqliteMaxBusyRetries)
		}
		busyRetries++
		select {
		case <-ctx.Done():
			return fmt.Errorf("migrate: sqlite busy retry: %w", ctx.Err())
		case <-time.After(retryDelay):
		}
	}
}

// isSQLiteBusy classifies SQLite write-lock contention. modernc.org/sqlite
// reports SQLITE_BUSY and its extended SQLITE_BUSY_SNAPSHOT variant as
// "database is locked (5|517) ... (SQLITE_BUSY...)"; the shared-cache
// SQLITE_LOCKED variant reports "database table is locked".
func isSQLiteBusy(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	for _, token := range []string{"sqlite_busy", "sqlite_locked", "database is locked", "database table is locked"} {
		if strings.Contains(msg, token) {
			return true
		}
	}
	return false
}

func migrateSQLiteStep(ctx context.Context, cfg Config, table string, migs []Migration) (bool, error) {
	tx, err := cfg.DB.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("migrate: begin step: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	if err := ensureMigrationsTableTx(ctx, tx, cfg.Driver, table); err != nil {
		return false, err
	}

	current, dirty, err := getCurrentVersionTx(ctx, tx, cfg.Driver, table)
	if err != nil {
		return false, err
	}
	if dirty {
		return false, ErrDirty
	}
	if cfg.MaxVersion >= 0 && current > cfg.MaxVersion {
		return false, fmt.Errorf("%w: db version %d > supported max %d", ErrNewerSchema, current, cfg.MaxVersion)
	}

	target := cfg.MaxVersion
	for _, m := range migs {
		if target >= 0 && m.Version > target {
			continue
		}
		if m.Version <= current {
			continue
		}
		if err := m.Up(ctx, tx); err != nil {
			return false, fmt.Errorf("migrate: up v%d (%s): %w", m.Version, m.Description, err)
		}
		if err := recordMigration(ctx, tx, cfg.Driver, table, m); err != nil {
			return false, err
		}
		if err := tx.Commit(); err != nil {
			return false, fmt.Errorf("migrate: commit v%d: %w", m.Version, err)
		}
		committed = true
		return true, nil
	}

	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("migrate: commit step: %w", err)
	}
	committed = true
	return false, nil
}

type dbExecer interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

type txBeginner interface {
	dbExecer
	BeginTx(context.Context, *sql.TxOptions) (*sql.Tx, error)
}

func ensureMigrationsTable(ctx context.Context, db dbExecer, drv Driver, table string) error {
	return ensureMigrationsTableTx(ctx, db, drv, table)
}

func ensureMigrationsTableTx(ctx context.Context, db dbExecer, drv Driver, table string) error {
	var q string
	if drv == DriverPostgres {
		q = fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s (
			version BIGINT PRIMARY KEY,
			description TEXT,
			applied_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			dirty BOOLEAN NOT NULL DEFAULT FALSE
		)`, table)
	} else {
		q = fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s (
			version INTEGER PRIMARY KEY,
			description TEXT,
			applied_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
			dirty INTEGER NOT NULL DEFAULT 0
		)`, table)
	}
	if _, err := db.ExecContext(ctx, q); err != nil {
		return fmt.Errorf("migrate: create migrations table: %w", err)
	}
	return nil
}

func getCurrentVersion(ctx context.Context, db dbExecer, drv Driver, table string) (int64, bool, error) {
	return getCurrentVersionTx(ctx, db, drv, table)
}

func getCurrentVersionTx(ctx context.Context, db dbExecer, drv Driver, table string) (int64, bool, error) {
	q := fmt.Sprintf(`SELECT version, dirty FROM %s ORDER BY version DESC LIMIT 1`, table)
	var v int64
	var dirtyVal interface{}
	err := db.QueryRowContext(ctx, q).Scan(&v, &dirtyVal)
	if err == sql.ErrNoRows {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("migrate: read current version: %w", err)
	}
	switch d := dirtyVal.(type) {
	case bool:
		return v, d, nil
	case int64:
		return v, d != 0, nil
	case int:
		return v, d != 0, nil
	default:
		return v, false, nil
	}
}

func applyMigration(ctx context.Context, db txBeginner, drv Driver, table string, m Migration) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("migrate: begin tx for v%d: %w", m.Version, err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	if err := m.Up(ctx, tx); err != nil {
		return fmt.Errorf("migrate: up v%d (%s): %w", m.Version, m.Description, err)
	}
	if err := recordMigration(ctx, tx, drv, table, m); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("migrate: commit v%d: %w", m.Version, err)
	}
	committed = true
	return nil
}

func recordMigration(ctx context.Context, tx dbExecer, drv Driver, table string, m Migration) error {
	var q string
	if drv == DriverPostgres {
		q = fmt.Sprintf(`INSERT INTO %s (version, description, dirty) VALUES ($1, $2, FALSE)`, table)
	} else {
		q = fmt.Sprintf(`INSERT INTO %s (version, description, dirty) VALUES (?, ?, 0)`, table)
	}
	if _, err := tx.ExecContext(ctx, q, m.Version, m.Description); err != nil {
		return fmt.Errorf("migrate: record v%d: %w", m.Version, err)
	}
	return nil
}
