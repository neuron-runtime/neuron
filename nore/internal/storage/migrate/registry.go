package migrate

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// KVStoreMigrations returns the initial migrations for the kv_store schema.
func KVStoreMigrations(drv Driver) []Migration {
	switch drv {
	case DriverSQLite:
		return []Migration{
			{
				Version:     1,
				Description: "create kv_store table",
				Up: func(ctx context.Context, tx *sql.Tx) error {
					_, err := tx.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS kv_store (
						key TEXT PRIMARY KEY,
						value BLOB NOT NULL,
						created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
						updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
					)`)
					return err
				},
			},
			{
				Version:     2,
				Description: "create timer table",
				Up: func(ctx context.Context, tx *sql.Tx) error {
					_, err := tx.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS timer_store (
						key TEXT PRIMARY KEY,
						value BLOB NOT NULL,
						created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
						updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
					)`)
					return err
				},
			},
			{
				Version:     3,
				Description: "add timer due time and lease index",
				Up: func(ctx context.Context, tx *sql.Tx) error {
					// v3 is immutable DDL-only (23aca93). Legacy row handling is
					// performed by v4 so databases already recorded at v3 are repaired.
					for _, stmt := range []string{
						`ALTER TABLE timer_store ADD COLUMN due_at TIMESTAMP`,
						`ALTER TABLE timer_store ADD COLUMN owner TEXT`,
						`ALTER TABLE timer_store ADD COLUMN lease_expires_at TIMESTAMP`,
					} {
						if err := execAddColumnSQLite(ctx, tx, stmt); err != nil {
							return err
						}
					}
					_, err := tx.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_timer_store_due_key ON timer_store (due_at, key)`)
					return err
				},
			},
			{
				Version:     4,
				Description: "backfill/quarantine legacy timer rows with null due_at",
				Up: func(ctx context.Context, tx *sql.Tx) error {
					return backfillLegacyTimerDueAt(ctx, tx, DriverSQLite)
				},
			},
		}
	case DriverPostgres:
		return []Migration{
			{
				Version:     1,
				Description: "create kv_store table",
				Up: func(ctx context.Context, tx *sql.Tx) error {
					_, err := tx.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS kv_store (
						key TEXT PRIMARY KEY,
						value BYTEA NOT NULL,
						created_at TIMESTAMP DEFAULT NOW(),
						updated_at TIMESTAMP DEFAULT NOW()
					)`)
					if err != nil {
						return err
					}
					_, err = tx.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_kv_store_key ON kv_store (key)`)
					return err
				},
			},
			{
				Version:     2,
				Description: "create timer table",
				Up: func(ctx context.Context, tx *sql.Tx) error {
					_, err := tx.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS timer_store (
						key TEXT PRIMARY KEY,
						value BYTEA NOT NULL,
						created_at TIMESTAMP DEFAULT NOW(),
						updated_at TIMESTAMP DEFAULT NOW()
					)`)
					if err != nil {
						return err
					}
					_, err = tx.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_timer_store_key ON timer_store (key)`)
					return err
				},
			},
			{
				Version:     3,
				Description: "add timer due time and lease index",
				Up: func(ctx context.Context, tx *sql.Tx) error {
					// v3 is immutable DDL-only (23aca93). Legacy row handling is
					// performed by v4 so databases already recorded at v3 are repaired.
					stmts := []string{
						`ALTER TABLE timer_store ADD COLUMN IF NOT EXISTS due_at TIMESTAMPTZ`,
						`ALTER TABLE timer_store ADD COLUMN IF NOT EXISTS owner TEXT`,
						`ALTER TABLE timer_store ADD COLUMN IF NOT EXISTS lease_expires_at TIMESTAMPTZ`,
						`CREATE INDEX IF NOT EXISTS idx_timer_store_due_key ON timer_store (due_at, key)`,
					}
					for _, stmt := range stmts {
						if _, err := tx.ExecContext(ctx, stmt); err != nil {
							return err
						}
					}
					return nil
				},
			},
			{
				Version:     4,
				Description: "backfill/quarantine legacy timer rows with null due_at",
				Up: func(ctx context.Context, tx *sql.Tx) error {
					return backfillLegacyTimerDueAt(ctx, tx, DriverPostgres)
				},
			},
		}
	default:
		return nil
	}
}

func execAddColumnSQLite(ctx context.Context, tx *sql.Tx, stmt string) error {
	_, err := tx.ExecContext(ctx, stmt)
	if err == nil {
		return nil
	}
	// Narrow to SQLite's verified duplicate-column condition. Do not suppress
	// unrelated "already exists" failures (e.g. other objects) so real DDL
	// problems fail the migration.
	if strings.Contains(strings.ToLower(err.Error()), "duplicate column name") {
		return nil
	}
	return err
}

// backfillLegacyTimerDueAt explicitly handles pre-v3 timer rows that have no
// scheduling metadata. It attempts a safe parse of legacy JSON wait-until fields
// (waitUntil/wait_until/dueAt/due_at) from the stored value and sets due_at when
// parseable. Rows with no parseable due time are left with due_at NULL, which
// keeps them explicitly quarantined/non-schedulable (excluded from
// DueTimers/ClaimTimer) rather than silently scheduled. This is idempotent and
// only mutates rows where due_at IS NULL.
//
// SQL is driver-dispatched: PostgreSQL uses $1/$2 placeholders and SQLite uses ?.
// A failing placeholder probe must never be executed inside a PostgreSQL
// transaction because it aborts the transaction.
func backfillLegacyTimerDueAt(ctx context.Context, tx *sql.Tx, drv Driver) error {
	rows, err := tx.QueryContext(ctx, `SELECT key, value FROM timer_store WHERE due_at IS NULL`)
	if err != nil {
		return fmt.Errorf("migrate: list legacy timer rows: %w", err)
	}
	defer rows.Close()

	type legacy struct {
		key string
		due time.Time
	}
	var updates []legacy
	for rows.Next() {
		var key string
		var value []byte
		if err := rows.Scan(&key, &value); err != nil {
			return fmt.Errorf("migrate: scan legacy timer row: %w", err)
		}
		if due, ok := parseLegacyTimerDueAt(value); ok {
			updates = append(updates, legacy{key: key, due: due})
		}
		// else: explicitly quarantined as non-schedulable (due_at remains NULL).
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("migrate: iterate legacy timer rows: %w", err)
	}

	updateSQL := `UPDATE timer_store SET due_at = ? WHERE key = ? AND due_at IS NULL`
	if drv == DriverPostgres {
		updateSQL = `UPDATE timer_store SET due_at = $1 WHERE key = $2 AND due_at IS NULL`
	}
	for _, u := range updates {
		if _, err := tx.ExecContext(ctx, updateSQL, u.due.UTC(), u.key); err != nil {
			return fmt.Errorf("migrate: backfill legacy timer %q: %w", u.key, err)
		}
	}
	return nil
}

func parseLegacyTimerDueAt(value []byte) (time.Time, bool) {
	var payload map[string]any
	if err := json.Unmarshal(value, &payload); err != nil {
		return time.Time{}, false
	}
	for _, field := range []string{"waitUntil", "wait_until", "dueAt", "due_at"} {
		raw, ok := payload[field]
		if !ok || raw == nil {
			continue
		}
		switch v := raw.(type) {
		case string:
			if v == "" {
				continue
			}
			for _, layout := range []string{time.RFC3339Nano, time.RFC3339} {
				if ts, err := time.Parse(layout, v); err == nil {
					return ts, true
				}
			}
		case float64:
			// Treat as unix milliseconds if large, else seconds.
			if v > 1e12 {
				return time.UnixMilli(int64(v)), true
			}
			if v > 0 {
				return time.Unix(int64(v), int64((v-float64(int64(v)))*1e9)), true
			}
		}
	}
	return time.Time{}, false
}
