package migrate

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"
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
type Config struct {
	DB         *sql.DB
	Driver     Driver
	Migrations []Migration
	MaxVersion int64
	TableName  string
}

// Migrate ensures the schema is up to date.
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

	if err := ensureMigrationsTable(ctx, cfg.DB, cfg.Driver, table); err != nil {
		return err
	}

	current, dirty, err := getCurrentVersion(ctx, cfg.DB, cfg.Driver, table)
	if err != nil {
		return err
	}
	if dirty {
		return ErrDirty
	}
	if cfg.MaxVersion >= 0 && current > cfg.MaxVersion {
		return fmt.Errorf("%w: db version %d > supported max %d", ErrNewerSchema, current, cfg.MaxVersion)
	}

	migs := make([]Migration, len(cfg.Migrations))
	copy(migs, cfg.Migrations)
	sort.Slice(migs, func(i, j int) bool { return migs[i].Version < migs[j].Version })

	for _, m := range migs {
		if m.Version <= current {
			continue
		}
		if err := applyMigration(ctx, cfg.DB, cfg.Driver, table, m); err != nil {
			return err
		}
	}
	return nil
}

func ensureMigrationsTable(ctx context.Context, db *sql.DB, drv Driver, table string) error {
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

func getCurrentVersion(ctx context.Context, db *sql.DB, drv Driver, table string) (int64, bool, error) {
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

func applyMigration(ctx context.Context, db *sql.DB, drv Driver, table string, m Migration) error {
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

func recordMigration(ctx context.Context, tx *sql.Tx, drv Driver, table string, m Migration) error {
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
