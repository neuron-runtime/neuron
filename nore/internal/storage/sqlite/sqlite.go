package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"github.com/neuron-runtime/neuron/nore/internal/storage"
	"github.com/neuron-runtime/neuron/nore/internal/storage/migrate"

	_ "modernc.org/sqlite"
)

func init() {
	storage.RegisterDriver("sqlite", func(ctx context.Context, cfg storage.Config) (storage.Store, error) {
		return New(ctx, cfg)
	})
}

// Store is the SQLite storage backend. A transaction owns the store for its
// whole callback through the gate, so the single database connection cannot be
// used by a plain operation while the transaction is active. Concurrent callers
// wait for the transaction; re-entrant calls from inside the callback receive
// the explicit transaction errors instead of deadlocking.
type Store struct {
	gate   storage.TransactionGate
	db     *sql.DB
	closed atomic.Bool
}

// New opens the SQLite database, initializes its schema, and verifies that it
// responds. A failure closes the database before returning so a partially
// initialized store is never observed.
func New(ctx context.Context, cfg storage.Config) (*Store, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	dataDir := cfg.DataDir
	if dataDir == "" {
		dataDir = cfg.Path
	}
	if dataDir == "" {
		return nil, fmt.Errorf("data directory is required")
	}

	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return nil, fmt.Errorf("create data directory %s: %w", dataDir, err)
	}

	dbPath := filepath.Join(dataDir, "nore.db")

	// _txlock=immediate makes every WithTx acquire the write lock at BEGIN
	// instead of upgrading from a read snapshot mid-transaction. A deferred
	// transaction that reads and then writes can otherwise fail with
	// SQLITE_BUSY_SNAPSHOT when another process commits first; an immediate
	// transaction waits on busy_timeout instead.
	db, err := sql.Open("sqlite", dbPath+"?_journal_mode=WAL&_busy_timeout=5000&_synchronous=NORMAL&_txlock=immediate")
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}

	db.SetMaxOpenConns(1)

	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping sqlite: %w", err)
	}

	s := &Store{db: db}
	migs := migrate.KVStoreMigrations(migrate.DriverSQLite)
	maxv := int64(0)
	for _, m := range migs {
		if m.Version > maxv {
			maxv = m.Version
		}
	}
	if err := migrate.Migrate(ctx, migrate.Config{DB: db, Driver: migrate.DriverSQLite, Migrations: migs, MaxVersion: maxv}); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}

	return s, nil
}

func (s *Store) Get(ctx context.Context, key string) ([]byte, error) {
	if err := s.guard(); err != nil {
		return nil, err
	}
	if err := s.gate.BeginRead(); err != nil {
		return nil, err
	}
	defer s.gate.EndRead()
	return get(ctx, s.db, key)
}

func (s *Store) Put(ctx context.Context, key string, data []byte) error {
	if err := s.guard(); err != nil {
		return err
	}
	if err := s.gate.BeginRead(); err != nil {
		return err
	}
	defer s.gate.EndRead()
	return put(ctx, s.db, key, data)
}

func (s *Store) Delete(ctx context.Context, key string) error {
	if err := s.guard(); err != nil {
		return err
	}
	if err := s.gate.BeginRead(); err != nil {
		return err
	}
	defer s.gate.EndRead()
	return del(ctx, s.db, key)
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
	return list(ctx, s.db, prefix)
}

func (s *Store) Ping(ctx context.Context) error {
	if err := s.guard(); err != nil {
		return err
	}
	if err := s.gate.BeginRead(); err != nil {
		return err
	}
	defer s.gate.EndRead()
	return s.db.PingContext(ctx)
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
		return fmt.Errorf("sqlite begin: %w", err)
	}

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

func (t txOps) CompareAndSwap(ctx context.Context, key string, old, new []byte) error {
	return compareAndSwap(ctx, t.tx, key, old, new)
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

func compareAndSwap(ctx context.Context, q querier, key string, old, new []byte) error {
	result, err := q.ExecContext(ctx,
		`UPDATE kv_store SET value = ?, updated_at = CURRENT_TIMESTAMP WHERE key = ? AND value = ?`,
		new, key, old,
	)
	if err != nil {
		return fmt.Errorf("sqlite compare and swap %q: %w", key, err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("sqlite compare and swap %q rows affected: %w", key, err)
	}
	if affected == 1 {
		return nil
	}
	if affected > 1 {
		return fmt.Errorf("sqlite compare and swap %q: updated %d rows", key, affected)
	}

	// No row matched. Reload to distinguish a missing key from a value that
	// changed since the caller read it.
	if _, err := get(ctx, q, key); err != nil {
		return err
	}
	return fmt.Errorf("%w: %s", storage.ErrConflict, key)
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

// timerRecord represents a timer record stored in the KV store as JSON bytes.
// The JSON fields are: WaitUntil, CreatedAt, Owner, LeaseExpiresAt, WaitType, ExpectedSignal.
type timerRecord struct {
	WaitUntil      int64  `json:"waitUntil"`
	CreatedAt      int64  `json:"createdAt"`
	Owner          string `json:"owner"`
	LeaseExpiresAt int64  `json:"leaseExpiresAt"`
	WaitType       string `json:"waitType"`
	ExpectedSignal string `json:"expectedSignal"`
}

// escapeLike escapes special characters in a SQL LIKE pattern so that the
// pattern is treated literally except for the trailing % wildcard. This mirrors
// the behaviour used by the PostgreSQL backend.
func escapeLike(s string) string {
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "%", "\\%")
	s = strings.ReplaceAll(s, "_", "\\_")
	return s
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
		return fmt.Errorf("sqlite begin: %w", err)
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
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("sqlite commit: %w", err)
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
	return get(ctx, t.tx, key)
}

func (t timerTx) Put(ctx context.Context, key string, data []byte) error {
	return put(ctx, t.tx, key, data)
}

func (t timerTx) Delete(ctx context.Context, key string) error {
	return del(ctx, t.tx, key)
}

func (t timerTx) CompareAndSwap(ctx context.Context, key string, old, new []byte) error {
	return compareAndSwap(ctx, t.tx, key, old, new)
}

func (t timerTx) List(ctx context.Context, prefix string) ([]string, error) {
	return list(ctx, t.tx, prefix)
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

// TimerTx is a KV bound to one timer transaction. Every operation on a TimerTx
// participates in the timer transaction opened by Store.WithTimer.
type TimerTx interface {
	storage.KV
	GetTimer(ctx context.Context, timerKey string) ([]byte, error)
	PutTimer(ctx context.Context, timerKey string, data []byte) error
	DeleteTimer(ctx context.Context, timerKey string) error
	ListTimers(ctx context.Context, prefix string) ([]string, error)
	ClaimTimer(ctx context.Context, timerKey string, owner string, lease time.Duration) (bool, error)
}

// timerStore methods implement the KV surface for timer operations.

func (s *Store) putTimer(ctx context.Context, q querier, key string, data []byte) error {
	_, err := q.ExecContext(ctx,
		`INSERT INTO timer_store (key, value, created_at, updated_at) VALUES (?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
		 ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = CURRENT_TIMESTAMP`,
		key, data,
	)
	if err != nil {
		return fmt.Errorf("sqlite put timer %q: %w", key, err)
	}
	return nil
}

func (s *Store) getTimer(ctx context.Context, q querier, key string) ([]byte, error) {
	var value []byte
	err := q.QueryRowContext(ctx, `SELECT value FROM timer_store WHERE key = ?`, key).Scan(&value)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("%w: %s", storage.ErrTimerNotFound, key)
	}
	if err != nil {
		return nil, fmt.Errorf("sqlite get timer %q: %w", key, err)
	}
	return value, nil
}

func (s *Store) delTimer(ctx context.Context, q querier, key string) error {
	_, err := q.ExecContext(ctx, `DELETE FROM timer_store WHERE key = ?`, key)
	if err != nil {
		return fmt.Errorf("sqlite delete timer %q: %w", key, err)
	}
	return nil
}

func (s *Store) listTimer(ctx context.Context, q querier, prefix string) ([]string, error) {
	rows, err := q.QueryContext(ctx,
		`SELECT key FROM timer_store WHERE key LIKE ? ESCAPE '\' ORDER BY key`,
		escapeLike(prefix)+"%",
	)
	if err != nil {
		return nil, fmt.Errorf("sqlite list timer %q: %w", prefix, err)
	}
	defer rows.Close()

	var keys []string
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			return nil, fmt.Errorf("sqlite scan timer: %w", err)
		}
		keys = append(keys, key)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite rows timer: %w", err)
	}

	return keys, nil
}

func (s *Store) compareAndSwapTimer(ctx context.Context, q querier, key string, old, new []byte) (int64, error) {
	result, err := q.ExecContext(ctx,
		`UPDATE timer_store SET value = ?, updated_at = CURRENT_TIMESTAMP WHERE key = ? AND value = ?`,
		new, key, old,
	)
	if err != nil {
		return 0, fmt.Errorf("sqlite compare and swap timer %q: %w", key, err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("sqlite compare and swap timer %q rows affected: %w", key, err)
	}
	return affected, nil
}

func (s *Store) claimTimer(ctx context.Context, q querier, key string, owner string, lease time.Duration) (bool, error) {
	// Use CompareAndSwap with an empty-old to atomically claim the timer.
	// If the key doesn't exist, CompareAndSwap returns ErrNotFound and we return false.
	// If the key exists, we update it and return true.
	// We need to set a lease expiry in the value JSON.
	// First, let's read the existing value to preserve existing fields, or set new ones.
	var existing []byte
	err := q.QueryRowContext(ctx, `SELECT value FROM timer_store WHERE key = ?`, key).Scan(&existing)
	if err != nil {
		if err == sql.ErrNoRows {
			return false, nil // key not found, not claimed
		}
		return false, fmt.Errorf("sqlite get timer %q: %w", key, err)
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
		return false, fmt.Errorf("sqlite marshal timer %q: %w", key, err)
	}

	// Perform compare-and-swap: only write if value matches old (empty for new key)
	result, err := q.ExecContext(ctx,
		`UPDATE timer_store SET value = ?, updated_at = CURRENT_TIMESTAMP WHERE key = ? AND (value = ? OR value IS NULL)`,
		newData, key, existing,
	)
	if err != nil {
		return false, fmt.Errorf("sqlite claim timer %q: %w", key, err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("sqlite claim timer %q rows affected: %w", key, err)
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
