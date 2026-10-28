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
		}
	default:
		return nil
	}
}
