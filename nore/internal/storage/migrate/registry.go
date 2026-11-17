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
				Description: "add timer due time and lease index; backfill/quarantine legacy timer rows",
				Up: func(ctx context.Context, tx *sql.Tx) error {
					// Idempotent/duplicate-tolerant column additions for concurrent or
					// partially-applied startup retries. Legacy v2 rows (no scheduling
					// metadata) are explicitly handled: due_at is backfilled from a
					// safely parseable legacy JSON wait-until field when present; rows
					// that cannot be parsed remain with due_at NULL and are therefore
					// explicitly quarantined as non-schedulable (not silently
					// discoverable by DueTimers/ClaimTimer). Inert-by-decision per ADR.
					for _, stmt := range []string{
						`ALTER TABLE timer_store ADD COLUMN due_at TIMESTAMP`,
						`ALTER TABLE timer_store ADD COLUMN owner TEXT`,
						`ALTER TABLE timer_store ADD COLUMN lease_expires_at TIMESTAMP`,
					} {
						if err := execAddColumnSQLite(ctx, tx, stmt); err != nil {
							return err
						}
					}
					if _, err := tx.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_timer_store_due_key ON timer_store (due_at, key)`); err != nil {
						return err
					}
					return backfillLegacyTimerDueAt(ctx, tx)
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
				Description: "add timer due time and lease index; backfill/quarantine legacy timer rows",
				Up: func(ctx context.Context, tx *sql.Tx) error {
					// Idempotent/duplicate-tolerant column additions for concurrent or
					// partially-applied startup retries. Legacy v2 rows (no scheduling
					// metadata) are explicitly handled: due_at is backfilled from a
					// safely parseable legacy JSON wait-until field when present; rows
					// that cannot be parsed remain with due_at NULL and are therefore
					// explicitly quarantined as non-schedulable (not silently
					// discoverable by DueTimers/ClaimTimer). Inert-by-decision per ADR.
					for _, stmt := range []string{
						`ALTER TABLE timer_store ADD COLUMN IF NOT EXISTS due_at TIMESTAMPTZ`,
						`ALTER TABLE timer_store ADD COLUMN IF NOT EXISTS owner TEXT`,
						`ALTER TABLE timer_store ADD COLUMN IF NOT EXISTS lease_expires_at TIMESTAMPTZ`,
					} {
						if _, err := tx.ExecContext(ctx, stmt); err != nil {
							return err
						}
					}
					if _, err := tx.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_timer_store_due_key ON timer_store (due_at, key)`); err != nil {
						return err
					}
					return backfillLegacyTimerDueAt(ctx, tx)
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
	msg := strings.ToLower(err.Error())
	if strings.Contains(msg, "duplicate column name") || strings.Contains(msg, "already exists") {
		return nil
	}
	return err
}

// backfillLegacyTimerDueAt explicitly handles pre-v3 timer rows that have no
// scheduling metadata. It attempts a safe parse of legacy JSON wait-until fields
// (waitUntil/wait_until/dueAt/due_at) from the stored value and sets due_at when
// parseable. Rows with no parseable due time are left with due_at NULL, which
// keeps them explicitly quarantined/non-schedulable (excluded from
// DueTimers/ClaimTimer) rather than silently scheduled. This is idempotent.
func backfillLegacyTimerDueAt(ctx context.Context, tx *sql.Tx) error {
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

	for _, u := range updates {
		// Try both placeholder styles: migrations/backfill run inside a driver tx and
		// this helper is shared by SQLite (?) and PostgreSQL ($n).
		if _, err := tx.ExecContext(ctx, `UPDATE timer_store SET due_at = ? WHERE key = ? AND due_at IS NULL`, u.due.UTC(), u.key); err != nil {
			if _, err2 := tx.ExecContext(ctx, `UPDATE timer_store SET due_at = $1 WHERE key = $2 AND due_at IS NULL`, u.due.UTC(), u.key); err2 != nil {
				return fmt.Errorf("migrate: backfill legacy timer %q: %w", u.key, err2)
			}
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
