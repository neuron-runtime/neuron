package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
	"sync"

	"github.com/neuron-runtime/neuron/nore/internal/storage"

	_ "modernc.org/sqlite"
)

type Store struct {
	mu   sync.RWMutex
	db   *sql.DB
	root string
}

func New(cfg storage.Config) (*Store, error) {
	if cfg.DataDir == "" {
		return nil, fmt.Errorf("data directory is required")
	}

	dbPath := filepath.Join(cfg.DataDir, "nore.db")

	db, err := sql.Open("sqlite", dbPath+"?_journal_mode=WAL&_busy_timeout=5000&_synchronous=NORMAL")
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}

	db.SetMaxOpenConns(1)

	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping sqlite: %w", err)
	}

	s := &Store{db: db, root: cfg.DataDir}
	if err := s.migrate(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}

	return s, nil
}

func (s *Store) migrate() error {
	query := `CREATE TABLE IF NOT EXISTS kv_store (
		key TEXT PRIMARY KEY,
		value BLOB NOT NULL,
		created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
		updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
	)`
	_, err := s.db.Exec(query)
	return err
}

func (s *Store) Get(ctx context.Context, key string) ([]byte, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var value []byte
	err := s.db.QueryRowContext(ctx, `SELECT value FROM kv_store WHERE key = ?`, key).Scan(&value)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("%w: %s", storage.ErrNotFound, key)
	}
	if err != nil {
		return nil, fmt.Errorf("sqlite get %q: %w", key, err)
	}

	return value, nil
}

func (s *Store) Put(ctx context.Context, key string, data []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	_, err := s.db.ExecContext(ctx,
		`INSERT INTO kv_store (key, value, created_at, updated_at) VALUES (?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
		 ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = CURRENT_TIMESTAMP`,
		key, data,
	)
	if err != nil {
		return fmt.Errorf("sqlite put %q: %w", key, err)
	}

	return nil
}

func (s *Store) Delete(ctx context.Context, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	_, err := s.db.ExecContext(ctx, `DELETE FROM kv_store WHERE key = ?`, key)
	if err != nil {
		return fmt.Errorf("sqlite delete %q: %w", key, err)
	}

	return nil
}

func (s *Store) List(ctx context.Context, prefix string) ([]string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	rows, err := s.db.QueryContext(ctx, `SELECT key FROM kv_store WHERE key LIKE ? ORDER BY key`, prefix+"%")
	if err != nil {
		return nil, fmt.Errorf("sqlite list %q: %w", prefix, err)
	}
	defer rows.Close()

	var keys []string
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			return nil, fmt.Errorf("sqlite scan: %w", err)
		}
		keys = append(keys, key)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite rows: %w", err)
	}

	return keys, nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

func (s *Store) Root() string {
	return s.root
}

func SanitizeKey(parts ...string) string {
	return strings.Join(parts, "/")
}
