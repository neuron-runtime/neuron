package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"

	_ "github.com/lib/pq"

	"github.com/neuron-runtime/neuron/nore/internal/storage"
)

func init() {
	storage.RegisterDriver("postgres", New)
	storage.RegisterDriver("postgresql", New)
}

type Store struct {
	mu       sync.Mutex
	db       *sql.DB
	txActive atomic.Bool
	closed   atomic.Bool
}

func New(ctx context.Context, cfg storage.Config) (storage.Store, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	dsn := cfg.DSN
	if dsn == "" {
		dsn = os.Getenv("NEURON_DATABASE_URL")
	}
	if dsn == "" {
		return nil, fmt.Errorf("postgres: dsn is required")
	}

	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return nil, fmt.Errorf("open postgres: %w", err)
	}

	db.SetMaxOpenConns(25)
	db.SetMaxIdleConns(25)
	db.SetConnMaxLifetime(0)

	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping postgres: %w", err)
	}

	s := &Store{db: db}
	if err := s.migrate(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) guard() error {
	if s.closed.Load() {
		return storage.ErrClosed
	}
	return nil
}

func (s *Store) Get(ctx context.Context, key string) ([]byte, error) {
	if err := s.guard(); err != nil {
		return nil, err
	}
	if s.txActive.Load() {
		return nil, storage.ErrTransactionActive
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.txActive.Load() {
		return nil, storage.ErrTransactionActive
	}
	var data []byte
	err := s.db.QueryRowContext(ctx, "SELECT value FROM kv_store WHERE key = $1", key).Scan(&data)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("%w: %s", storage.ErrNotFound, key)
	}
	if err != nil {
		return nil, fmt.Errorf("postgres get %q: %w", key, err)
	}
	return data, nil
}

func (s *Store) Put(ctx context.Context, key string, data []byte) error {
	if err := s.guard(); err != nil {
		return err
	}
	if s.txActive.Load() {
		return storage.ErrTransactionActive
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.txActive.Load() {
		return storage.ErrTransactionActive
	}
	_, err := s.db.ExecContext(ctx, "INSERT INTO kv_store (key, value, updated_at) VALUES ($1, $2, NOW()) ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = NOW()", key, data)
	if err != nil {
		return fmt.Errorf("postgres put %q: %w", key, err)
	}
	return nil
}

func (s *Store) Delete(ctx context.Context, key string) error {
	if err := s.guard(); err != nil {
		return err
	}
	if s.txActive.Load() {
		return storage.ErrTransactionActive
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.txActive.Load() {
		return storage.ErrTransactionActive
	}
	_, err := s.db.ExecContext(ctx, "DELETE FROM kv_store WHERE key = $1", key)
	if err != nil {
		return fmt.Errorf("postgres delete %q: %w", key, err)
	}
	return nil
}

func escapeLike(s string) string {
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "%", "\\%")
	s = strings.ReplaceAll(s, "_", "\\_")
	return s
}

func (s *Store) List(ctx context.Context, prefix string) ([]string, error) {
	if err := s.guard(); err != nil {
		return nil, err
	}
	if s.txActive.Load() {
		return nil, storage.ErrTransactionActive
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.txActive.Load() {
		return nil, storage.ErrTransactionActive
	}
	pattern := escapeLike(prefix) + "%"
	rows, err := s.db.QueryContext(ctx, "SELECT key FROM kv_store WHERE key LIKE $1 ESCAPE '\\' ORDER BY key", pattern)
	if err != nil {
		return nil, fmt.Errorf("postgres list %q: %w", prefix, err)
	}
	defer rows.Close()
	var keys []string
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			return nil, fmt.Errorf("postgres list scan: %w", err)
		}
		keys = append(keys, k)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres list: %w", err)
	}
	return keys, nil
}

func (s *Store) Ping(ctx context.Context) error {
	if err := s.guard(); err != nil {
		return err
	}
	if s.txActive.Load() {
		return storage.ErrTransactionActive
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.txActive.Load() {
		return storage.ErrTransactionActive
	}
	if err := s.db.PingContext(ctx); err != nil {
		return fmt.Errorf("postgres ping: %w", err)
	}
	return nil
}

func (s *Store) WithTx(ctx context.Context, fn func(storage.Tx) error) error {
	if err := s.guard(); err != nil {
		return err
	}
	if fn == nil {
		return fmt.Errorf("transaction callback is required")
	}
	if s.txActive.Load() {
		return storage.ErrNestedTransaction
	}
	s.mu.Lock()
	if s.txActive.Load() {
		s.mu.Unlock()
		return storage.ErrNestedTransaction
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		s.mu.Unlock()
		return fmt.Errorf("postgres begin: %w", err)
	}
	s.txActive.Store(true)
	s.mu.Unlock()

	committed := false
	defer func() {
		s.txActive.Store(false)
		if !committed {
			_ = tx.Rollback()
		}
	}()

	t := &txOps{tx: tx}
	if err := fn(t); err != nil {
		return err
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("postgres commit: %w", err)
	}
	committed = true
	return nil
}

type txOps struct {
	tx *sql.Tx
}

func (t *txOps) Get(ctx context.Context, key string) ([]byte, error) {
	var data []byte
	err := t.tx.QueryRowContext(ctx, "SELECT value FROM kv_store WHERE key = $1", key).Scan(&data)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("%w: %s", storage.ErrNotFound, key)
	}
	if err != nil {
		return nil, fmt.Errorf("postgres tx get %q: %w", key, err)
	}
	return data, nil
}

func (t *txOps) Put(ctx context.Context, key string, data []byte) error {
	_, err := t.tx.ExecContext(ctx, "INSERT INTO kv_store (key, value, updated_at) VALUES ($1, $2, NOW()) ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = NOW()", key, data)
	if err != nil {
		return fmt.Errorf("postgres tx put %q: %w", key, err)
	}
	return nil
}

func (t *txOps) Delete(ctx context.Context, key string) error {
	_, err := t.tx.ExecContext(ctx, "DELETE FROM kv_store WHERE key = $1", key)
	if err != nil {
		return fmt.Errorf("postgres tx delete %q: %w", key, err)
	}
	return nil
}

func (t *txOps) List(ctx context.Context, prefix string) ([]string, error) {
	pattern := escapeLike(prefix) + "%"
	rows, err := t.tx.QueryContext(ctx, "SELECT key FROM kv_store WHERE key LIKE $1 ESCAPE '\\' ORDER BY key", pattern)
	if err != nil {
		return nil, fmt.Errorf("postgres tx list %q: %w", prefix, err)
	}
	defer rows.Close()
	var keys []string
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			return nil, fmt.Errorf("postgres tx list scan: %w", err)
		}
		keys = append(keys, k)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres tx list: %w", err)
	}
	return keys, nil
}

func (s *Store) Close() error {
	if !s.closed.CompareAndSwap(false, true) {
		return nil
	}
	if s.txActive.Load() {
		return storage.ErrTransactionActive
	}
	if err := s.db.Close(); err != nil {
		return fmt.Errorf("postgres close: %w", err)
	}
	return nil
}

func (s *Store) migrate(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS kv_store (
		key TEXT PRIMARY KEY,
		value BYTEA NOT NULL,
		created_at TIMESTAMP DEFAULT NOW(),
		updated_at TIMESTAMP DEFAULT NOW()
	)`)
	if err != nil {
		return fmt.Errorf("postgres migrate: %w", err)
	}
	_, err = s.db.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_kv_store_key ON kv_store (key)`)
	if err != nil {
		return fmt.Errorf("postgres migrate index: %w", err)
	}
	return nil
}
