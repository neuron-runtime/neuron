package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync/atomic"
	"time"

	_ "github.com/lib/pq"

	"github.com/neuron-runtime/neuron/nore/internal/storage"
	"github.com/neuron-runtime/neuron/nore/internal/storage/migrate"
)

func init() {
	storage.RegisterDriver("postgres", New)
	storage.RegisterDriver("postgresql", New)
}

type Store struct {
	gate   storage.TransactionGate
	db     *sql.DB
	closed atomic.Bool
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
	migs := migrate.KVStoreMigrations(migrate.DriverPostgres)
	maxv := int64(0)
	for _, m := range migs {
		if m.Version > maxv {
			maxv = m.Version
		}
	}
	if err := migrate.Migrate(ctx, migrate.Config{DB: db, Driver: migrate.DriverPostgres, Migrations: migs, MaxVersion: maxv}); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
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
	if err := s.gate.BeginRead(); err != nil {
		return nil, err
	}
	defer s.gate.EndRead()
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
	if err := s.gate.BeginRead(); err != nil {
		return err
	}
	defer s.gate.EndRead()
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
	if err := s.gate.BeginRead(); err != nil {
		return err
	}
	defer s.gate.EndRead()
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

func (s *Store) CompareAndSwap(ctx context.Context, key string, old, new []byte) error {
	if err := s.guard(); err != nil {
		return err
	}
	if err := s.gate.BeginRead(); err != nil {
		return err
	}
	defer s.gate.EndRead()
	return compareAndSwap(ctx, s.db, key, old, new)
}

func (s *Store) List(ctx context.Context, prefix string) ([]string, error) {
	if err := s.guard(); err != nil {
		return nil, err
	}
	if err := s.gate.BeginRead(); err != nil {
		return nil, err
	}
	defer s.gate.EndRead()
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
	if err := s.gate.BeginRead(); err != nil {
		return err
	}
	defer s.gate.EndRead()
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
	if err := s.gate.Begin(); err != nil {
		return err
	}
	defer s.gate.End()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("postgres begin: %w", err)
	}

	committed := false
	defer func() {
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

func (t *txOps) CompareAndSwap(ctx context.Context, key string, old, new []byte) error {
	return compareAndSwap(ctx, t.tx, key, old, new)
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

type querier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

func compareAndSwap(ctx context.Context, q querier, key string, old, new []byte) error {
	result, err := q.ExecContext(ctx,
		"UPDATE kv_store SET value = $1, updated_at = NOW() WHERE key = $2 AND value = $3",
		new, key, old,
	)
	if err != nil {
		return fmt.Errorf("postgres compare and swap %q: %w", key, err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("postgres compare and swap %q rows affected: %w", key, err)
	}
	if affected == 1 {
		return nil
	}
	if affected > 1 {
		return fmt.Errorf("postgres compare and swap %q: updated %d rows", key, affected)
	}

	// No row matched. Reload to distinguish a missing key from a value that
	// changed since the caller read it.
	var value []byte
	err = q.QueryRowContext(ctx, "SELECT value FROM kv_store WHERE key = $1", key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("%w: %s", storage.ErrNotFound, key)
	}
	if err != nil {
		return fmt.Errorf("postgres compare and swap %q: %w", key, err)
	}
	return fmt.Errorf("%w: %s", storage.ErrConflict, key)
}

// timerRecord represents a timer record stored as JSON bytes.
// The JSON fields are: WaitUntil, CreatedAt, Owner, LeaseExpiresAt, WaitType, ExpectedSignal.
type timerRecord struct {
	WaitUntil       int64  `json:"waitUntil"`
	CreatedAt       int64  `json:"createdAt"`
	Owner           string `json:"owner"`
	LeaseExpiresAt  int64  `json:"leaseExpiresAt"`
	WaitType        string `json:"waitType"`
	ExpectedSignal  string `json:"expectedSignal"`
}

// WithTimer runs fn inside one timer transaction. Returning an error from fn
// rolls the transaction back. WithTimer is analogous to WithTx but operates
// on the timer_store table and provides a TimerTx bound to the timer KV surface.
func (s *Store) WithTimer(ctx context.Context, fn func(storage.TimerTx) error) error {
	if err := s.guard(); err != nil {
		return err
	}
	if fn == nil {
		return fmt.Errorf("timer transaction callback is required")
	}
	if err := s.gate.Begin(); err != nil {
		return err
	}
	defer s.gate.End()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("postgres begin: %w", err)
	}

	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	timerTx := timerTx{tx: tx, store: s}
	if err := fn(timerTx); err != nil {
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

// timerTx operates on the store's database connection inside a timer
// transaction. It implements storage.TimerTx so that the callback passed to
// Store.WithTimer can perform timer operations that participate in the same
// transaction.
type timerTx struct {
	tx    *sql.Tx
	store *Store
}

func (t timerTx) Get(ctx context.Context, key string) ([]byte, error) {
	return t.store.getTimer(ctx, t.tx, key)
}

func (t timerTx) Put(ctx context.Context, key string, data []byte) error {
	return t.store.putTimer(ctx, t.tx, key, data)
}

func (t timerTx) Delete(ctx context.Context, key string) error {
	return t.store.delTimer(ctx, t.tx, key)
}

func (t timerTx) List(ctx context.Context, prefix string) ([]string, error) {
	return t.store.listTimer(ctx, t.tx, prefix)
}

func (t timerTx) CompareAndSwap(ctx context.Context, key string, old, new []byte) error {
	return t.store.compareAndSwapTimer(ctx, t.tx, key, old, new)
}

func (t timerTx) GetTimer(ctx context.Context, timerKey string) ([]byte, error) {
	return t.store.getTimer(ctx, t.tx, timerKey)
}

func (t timerTx) PutTimer(ctx context.Context, timerKey string, data []byte) error {
	return t.store.putTimer(ctx, t.tx, timerKey, data)
}

func (t timerTx) DeleteTimer(ctx context.Context, timerKey string) error {
	return t.store.delTimer(ctx, t.tx, timerKey)
}

func (t timerTx) ListTimers(ctx context.Context, prefix string) ([]string, error) {
	return t.store.listTimer(ctx, t.tx, prefix)
}

func (t timerTx) ClaimTimer(ctx context.Context, timerKey string, owner string, lease time.Duration) (bool, error) {
	return t.store.claimTimer(ctx, t.tx, timerKey, owner, lease)
}

// PutTimer stores data at the given timer key. If the key already exists,
// the write is an overwrite (compare-and-swap with existing value).
func (s *Store) PutTimer(ctx context.Context, timerKey string, data []byte) error {
	if err := s.guard(); err != nil {
		return err
	}
	if err := s.gate.BeginRead(); err != nil {
		return err
	}
	defer s.gate.EndRead()
	return s.putTimer(ctx, s.db, timerKey, data)
}

// GetTimer retrieves the data stored at the given timer key.
func (s *Store) GetTimer(ctx context.Context, timerKey string) ([]byte, error) {
	if err := s.guard(); err != nil {
		return nil, err
	}
	if err := s.gate.BeginRead(); err != nil {
		return nil, err
	}
	defer s.gate.EndRead()
	return s.getTimer(ctx, s.db, timerKey)
}

// DeleteTimer deletes the timer record at the given key.
func (s *Store) DeleteTimer(ctx context.Context, timerKey string) error {
	if err := s.guard(); err != nil {
		return err
	}
	if err := s.gate.BeginRead(); err != nil {
		return err
	}
	defer s.gate.EndRead()
	return s.delTimer(ctx, s.db, timerKey)
}

// ListTimers returns all timer keys matching the given prefix.
func (s *Store) ListTimers(ctx context.Context, prefix string) ([]string, error) {
	if err := s.guard(); err != nil {
		return nil, err
	}
	if err := s.gate.BeginRead(); err != nil {
		return nil, err
	}
	defer s.gate.EndRead()
	return s.listTimer(ctx, s.db, prefix)
}

// ClaimTimer atomically claims a timer lease. Returns (claimed, error).
// If the timer key does not exist, it is created with the owner and lease.
// If the timer key already exists, the owner is updated and the lease is extended.
// The function returns true if the claim succeeded, false otherwise.
func (s *Store) ClaimTimer(ctx context.Context, timerKey string, owner string, lease time.Duration) (bool, error) {
	if err := s.guard(); err != nil {
		return false, err
	}
	if err := s.gate.BeginRead(); err != nil {
		return false, err
	}
	defer s.gate.EndRead()
	return s.claimTimer(ctx, s.db, timerKey, owner, lease)
}

// putTimer stores data at the given timer key. If the key already exists,
// the write is an overwrite (compare-and-swap with existing value).
func (s *Store) putTimer(ctx context.Context, q querier, key string, data []byte) error {
	_, err := q.ExecContext(ctx,
		`INSERT INTO timer_store (key, value, created_at, updated_at) VALUES ($1, $2, NOW(), NOW()) ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = NOW()`,
		key, data,
	)
	if err != nil {
		return fmt.Errorf("postgres put timer %q: %w", key, err)
	}
	return nil
}

// getTimer retrieves the data stored at the given timer key.
func (s *Store) getTimer(ctx context.Context, q querier, key string) ([]byte, error) {
	var value []byte
	err := q.QueryRowContext(ctx, `SELECT value FROM timer_store WHERE key = $1`, key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("%w: %s", storage.ErrTimerNotFound, key)
	}
	if err != nil {
		return nil, fmt.Errorf("postgres get timer %q: %w", key, err)
	}
	return value, nil
}

// delTimer deletes the timer record at the given key.
func (s *Store) delTimer(ctx context.Context, q querier, key string) error {
	_, err := q.ExecContext(ctx, `DELETE FROM timer_store WHERE key = $1`, key)
	if err != nil {
		return fmt.Errorf("postgres delete timer %q: %w", key, err)
	}
	return nil
}

// listTimers returns all timer keys matching the given prefix.
func (s *Store) listTimer(ctx context.Context, q querier, prefix string) ([]string, error) {
	pattern := escapeLike(prefix) + "%"
	rows, err := q.QueryContext(ctx, `SELECT key FROM timer_store WHERE key LIKE $1 ESCAPE '\\' ORDER BY key`, pattern)
	if err != nil {
		return nil, fmt.Errorf("postgres list timer %q: %w", prefix, err)
	}
	defer rows.Close()
	var keys []string
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			return nil, fmt.Errorf("postgres list timer scan: %w", err)
		}
		keys = append(keys, k)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres list timer: %w", err)
	}
	return keys, nil
}

// claimTimer atomically claims a timer lease. Returns (claimed, error).
// If the timer key does not exist, it is created with the owner and lease.
// If the timer key already exists, the owner is updated and the lease is extended.
// The function returns true if the claim succeeded, false otherwise.
func (s *Store) claimTimer(ctx context.Context, q querier, key string, owner string, lease time.Duration) (bool, error) {
	// Use CompareAndSwap with an empty-old to atomically claim the timer.
	// If the key doesn't exist, CompareAndSwap returns ErrNotFound and we return false.
	// If the key exists, we update it and return true.
	// We need to set a lease expiry in the value JSON.
	var existing []byte
	err := q.QueryRowContext(ctx, `SELECT value FROM timer_store WHERE key = $1`, key).Scan(&existing)
	if err != nil {
		if err == sql.ErrNoRows {
			return false, nil // key not found, not claimed
		}
		return false, fmt.Errorf("postgres get timer %q: %w", key, err)
	}

	// Parse existing timer record to preserve fields
	var rec timerRecord
	if err := json.Unmarshal(existing, &rec); err != nil {
		// If no existing record or unmarshal fails, create a new one
		rec = timerRecord{
			WaitUntil:      0,
			CreatedAt:      time.Now().Unix(),
			Owner:          owner,
			LeaseExpiresAt: time.Now().Add(lease).Unix(),
			WaitType:       "",
			ExpectedSignal: "",
		}
	} else {
		// Update owner and lease expiry, preserve other fields
		now := time.Now().Unix()
		if rec.CreatedAt == 0 {
			rec.CreatedAt = now
		}
		rec.Owner = owner
		rec.LeaseExpiresAt = now + int64(lease.Seconds())
	}

	newData, err := json.Marshal(rec)
	if err != nil {
		return false, fmt.Errorf("postgres marshal timer %q: %w", key, err)
	}

	// Perform compare-and-swap: only write if value matches old (empty for new key)
	result, err := q.ExecContext(ctx,
		`UPDATE timer_store SET value = $1, updated_at = NOW() WHERE key = $2 AND (value = $3 OR value IS NULL)`,
		newData, key, existing,
	)
	if err != nil {
		return false, fmt.Errorf("postgres claim timer %q: %w", key, err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("postgres claim timer %q rows affected: %w", key, err)
	}
	if affected == 1 {
		return true, nil // claimed
	}

	// Key existed but compare-and-swap failed (value changed); re-read to determine state
	_, err = s.getTimer(ctx, q, key)
	if err != nil {
		if storage.ErrTimerNotFound == err {
			return false, nil // somehow deleted between read and write
		}
		return false, err
	}
	// Value changed; another writer won. Return false to indicate not claimed.
	return false, nil
}

// compareAndSwapTimer atomically replaces the value stored at key when its
// current value equals old. It returns ErrNotFound when key is missing and
// ErrConflict when the stored value differs from old; in both cases the
// stored value is left unchanged. On success new becomes the stored value.
func (s *Store) compareAndSwapTimer(ctx context.Context, q querier, key string, old, new []byte) error {
	result, err := q.ExecContext(ctx,
		`UPDATE timer_store SET value = $1, updated_at = NOW() WHERE key = $2 AND (value = $3 OR value IS NULL)`,
		new, key, old,
	)
	if err != nil {
		return fmt.Errorf("postgres compare and swap timer %q: %w", key, err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("postgres compare and swap timer %q rows affected: %w", key, err)
	}
	if affected == 1 {
		return nil
	}

	// No row matched. Reload to distinguish a missing key from a value that
	// changed since the caller read it.
	var value []byte
	err = q.QueryRowContext(ctx, `SELECT value FROM timer_store WHERE key = $1`, key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("%w: %s", storage.ErrNotFound, key)
	}
	if err != nil {
		return fmt.Errorf("postgres compare and swap timer %q: %w", key, err)
	}
	return fmt.Errorf("%w: %s", storage.ErrConflict, key)
}

func (s *Store) Close() error {
	if err := s.guard(); err != nil {
		return err
	}
	if s.gate.Active() {
		return storage.ErrTransactionActive
	}
	if err := s.gate.Begin(); err != nil {
		return err
	}
	defer s.gate.End()
	if s.closed.Swap(true) {
		return nil
	}
	if err := s.db.Close(); err != nil {
		return fmt.Errorf("postgres close: %w", err)
	}
	return nil
}
