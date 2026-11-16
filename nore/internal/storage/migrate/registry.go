package migrate

import (
	"context"
	"database/sql"
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
					stmts := []string{
						`ALTER TABLE timer_store ADD COLUMN due_at TIMESTAMP`,
						`ALTER TABLE timer_store ADD COLUMN owner TEXT`,
						`ALTER TABLE timer_store ADD COLUMN lease_expires_at TIMESTAMP`,
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
					stmts := []string{
						`ALTER TABLE timer_store ADD COLUMN due_at TIMESTAMPTZ`,
						`ALTER TABLE timer_store ADD COLUMN owner TEXT`,
						`ALTER TABLE timer_store ADD COLUMN lease_expires_at TIMESTAMPTZ`,
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
		}
	default:
		return nil
	}
}
