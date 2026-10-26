package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"sync"
	"sync/atomic"

	"github.com/neuron-runtime/neuron/nore/internal/storage"

	_ "modernc.org/sqlite"
)

func init() {
	storage.RegisterDriver("sqlite", func(ctx context.Context, cfg storage.Config) (storage.Store, error) {
		return New(ctx, cfg)
	})
}

// Store is the SQLite storage backend. It serializes access with one connection
// so that a transaction can safely own the connection for its whole callback.
// txActive is checked atomically before taking mu, which lets an operation
// attempted during a transaction report ErrTransactionActive instead of blocking
// until the transaction commits.
type Store struct {
	mu       sync.Mutex
	db       *sql.DB
	txActive atomic.Bool
	closed   atomic.Bool
}

// New opens the SQLite database, initializes its schema, and verifies that it
// responds. A failure closes the database before returning so a partially
// initialized store is never observed.
func New(ctx context.Context, cfg storage.Config) (*Store, error) {
	if cfg.DataDir == "" {
		return nil, fmt.Errorf("data directory is required")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	dbPath := filepath.Join(cfg.DataDir, "nore.db")

	db, err := sql.Open("sqlite", dbPath+"?_journal_mode=WAL&_busy_timeout=5000&_synchronous=NORMAL")
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}

	db.SetMaxOpenConns(1)

	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping sqlite: %w", err)
	}

	s := &Store{db: db}
	if err := s.migrate(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}

	return s, nil
}

func (s *Store) migrate(ctx context.Context) error {
	query := `CREATE TABLE IF NOT EXISTS kv_store (
		key TEXT PRIMARY KEY,
		value BLOB NOT NULL,
		created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
		updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
	)`
	_, err := s.db.ExecContext(ctx, query)
	return err
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
	return get(ctx, s.db, key)
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
	return put(ctx, s.db, key, data)
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
	return del(ctx, s.db, key)
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
	return list(ctx, s.db, prefix)
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
	return s.db.PingContext(ctx)
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
	defer s.mu.Unlock()
	if s.txActive.Load() {
		return storage.ErrNestedTransaction
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("sqlite begin: %w", err)
	}
	s.txActive.Store(true)
	defer s.txActive.Store(false)

	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	if err := fn(txOps{tx: tx}); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("sqlite commit: %w", err)
	}
	committed = true
	return nil
}

func (s *Store) Close() error {
	if s.txActive.Load() {
		return storage.ErrTransactionActive
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.txActive.Load() {
		return storage.ErrTransactionActive
	}
	if s.closed.Swap(true) {
		return nil
	}
	return s.db.Close()
}

func (s *Store) guard() error {
	if s.closed.Load() {
		return storage.ErrClosed
	}
	return nil
}

type txOps struct {
	tx *sql.Tx
}

func (t txOps) Get(ctx context.Context, key string) ([]byte, error) {
	return get(ctx, t.tx, key)
}

func (t txOps) Put(ctx context.Context, key string, data []byte) error {
	return put(ctx, t.tx, key, data)
}

func (t txOps) Delete(ctx context.Context, key string) error {
	return del(ctx, t.tx, key)
}

func (t txOps) List(ctx context.Context, prefix string) ([]string, error) {
	return list(ctx, t.tx, prefix)
}

type querier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

func get(ctx context.Context, q querier, key string) ([]byte, error) {
	var value []byte
	err := q.QueryRowContext(ctx, `SELECT value FROM kv_store WHERE key = ?`, key).Scan(&value)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("%w: %s", storage.ErrNotFound, key)
	}
	if err != nil {
		return nil, fmt.Errorf("sqlite get %q: %w", key, err)
	}
	return value, nil
}

func put(ctx context.Context, q querier, key string, data []byte) error {
	_, err := q.ExecContext(ctx,
		`INSERT INTO kv_store (key, value, created_at, updated_at) VALUES (?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
		 ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = CURRENT_TIMESTAMP`,
		key, data,
	)
	if err != nil {
		return fmt.Errorf("sqlite put %q: %w", key, err)
	}
	return nil
}

func del(ctx context.Context, q querier, key string) error {
	_, err := q.ExecContext(ctx, `DELETE FROM kv_store WHERE key = ?`, key)
	if err != nil {
		return fmt.Errorf("sqlite delete %q: %w", key, err)
	}
	return nil
}

func list(ctx context.Context, q querier, prefix string) ([]string, error) {
	rows, err := q.QueryContext(ctx,
		`SELECT key FROM kv_store WHERE key LIKE ? ESCAPE '\' ORDER BY key`,
		escapeLike(prefix)+"%",
	)
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

func escapeLike(value string) string {
	var b []byte
	for i := 0; i < len(value); i++ {
		switch value[i] {
		case '\\', '%', '_':
			b = append(b, '\\')
		}
		b = append(b, value[i])
	}
	return string(b)
}
