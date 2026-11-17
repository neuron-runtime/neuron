package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	_ "modernc.org/sqlite"

	"github.com/neuron-runtime/neuron/nore/internal/storage"
	"github.com/neuron-runtime/neuron/nore/internal/storage/migrate"
)

func init() {
	storage.RegisterDriver("sqlite", func(ctx context.Context, cfg storage.Config) (storage.Store, error) {
		return New(ctx, cfg)
	})
}

// Store is the SQLite storage backend.
type Store struct {
	gate   storage.TransactionGate
	db     *sql.DB
	closed atomic.Bool
}

// New opens the SQLite database, initializes its schema, and verifies that it responds.
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

	if err := fn(timerTx{tx: tx}); err != nil {
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

func (s *Store) PutTimer(ctx context.Context, timerKey string, dueAt time.Time, data []byte) error {
	if err := s.guard(); err != nil {
		return err
	}
	if err := s.gate.BeginRead(); err != nil {
		return err
	}
	defer s.gate.EndRead()
	return putTimer(ctx, s.db, timerKey, dueAt, data)
}

func (s *Store) GetTimer(ctx context.Context, timerKey string) ([]byte, error) {
	if err := s.guard(); err != nil {
		return nil, err
	}
	if err := s.gate.BeginRead(); err != nil {
		return nil, err
	}
	defer s.gate.EndRead()
	return getTimer(ctx, s.db, timerKey)
}

func (s *Store) DeleteTimer(ctx context.Context, timerKey string) error {
	if err := s.guard(); err != nil {
		return err
	}
	if err := s.gate.BeginRead(); err != nil {
		return err
	}
	defer s.gate.EndRead()
	return delTimer(ctx, s.db, timerKey)
}

func (s *Store) DeleteTimerOwned(ctx context.Context, timerKey string, owner string) (bool, error) {
	if err := s.guard(); err != nil {
		return false, err
	}
	if err := s.gate.BeginRead(); err != nil {
		return false, err
	}
	defer s.gate.EndRead()
	return deleteTimerOwned(ctx, s.db, timerKey, owner)
}

func (s *Store) ListTimers(ctx context.Context, prefix string) ([]string, error) {
	if err := s.guard(); err != nil {
		return nil, err
	}
	if err := s.gate.BeginRead(); err != nil {
		return nil, err
	}
	defer s.gate.EndRead()
	return listTimers(ctx, s.db, prefix)
}

func (s *Store) ClaimTimer(ctx context.Context, timerKey string, owner string, lease time.Duration) (bool, error) {
	if err := s.guard(); err != nil {
		return false, err
	}
	if err := s.gate.BeginRead(); err != nil {
		return false, err
	}
	defer s.gate.EndRead()
	return claimTimer(ctx, s.db, timerKey, owner, lease, time.Now())
}

func (s *Store) RenewTimer(ctx context.Context, timerKey string, owner string, lease time.Duration) (bool, error) {
	if err := s.guard(); err != nil {
		return false, err
	}
	if err := s.gate.BeginRead(); err != nil {
		return false, err
	}
	defer s.gate.EndRead()
	return renewTimer(ctx, s.db, timerKey, owner, lease, time.Now())
}

func (s *Store) DueTimers(ctx context.Context, now time.Time, limit int) ([]storage.Timer, error) {
	if err := s.guard(); err != nil {
		return nil, err
	}
	if err := s.gate.BeginRead(); err != nil {
		return nil, err
	}
	defer s.gate.EndRead()
	return dueTimers(ctx, s.db, now, limit)
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

func (t txOps) Get(ctx context.Context, key string) ([]byte, error) { return get(ctx, t.tx, key) }
func (t txOps) Put(ctx context.Context, key string, data []byte) error {
	return put(ctx, t.tx, key, data)
}
func (t txOps) Delete(ctx context.Context, key string) error { return del(ctx, t.tx, key) }
func (t txOps) CompareAndSwap(ctx context.Context, key string, old, new []byte) error {
	return compareAndSwap(ctx, t.tx, key, old, new)
}
func (t txOps) List(ctx context.Context, prefix string) ([]string, error) {
	return list(ctx, t.tx, prefix)
}

type timerTx struct {
	tx *sql.Tx
}

func (t timerTx) Get(ctx context.Context, key string) ([]byte, error) { return get(ctx, t.tx, key) }
func (t timerTx) Put(ctx context.Context, key string, data []byte) error {
	return put(ctx, t.tx, key, data)
}
func (t timerTx) Delete(ctx context.Context, key string) error { return del(ctx, t.tx, key) }
func (t timerTx) CompareAndSwap(ctx context.Context, key string, old, new []byte) error {
	return compareAndSwap(ctx, t.tx, key, old, new)
}
func (t timerTx) List(ctx context.Context, prefix string) ([]string, error) {
	return list(ctx, t.tx, prefix)
}
func (t timerTx) GetTimer(ctx context.Context, timerKey string) ([]byte, error) {
	return getTimer(ctx, t.tx, timerKey)
}
func (t timerTx) PutTimer(ctx context.Context, timerKey string, dueAt time.Time, data []byte) error {
	return putTimer(ctx, t.tx, timerKey, dueAt, data)
}
func (t timerTx) DeleteTimer(ctx context.Context, timerKey string) error {
	return delTimer(ctx, t.tx, timerKey)
}
func (t timerTx) DeleteTimerOwned(ctx context.Context, timerKey string, owner string) (bool, error) {
	return deleteTimerOwned(ctx, t.tx, timerKey, owner)
}
func (t timerTx) ListTimers(ctx context.Context, prefix string) ([]string, error) {
	return listTimers(ctx, t.tx, prefix)
}
func (t timerTx) ClaimTimer(ctx context.Context, timerKey string, owner string, lease time.Duration) (bool, error) {
	return claimTimer(ctx, t.tx, timerKey, owner, lease, time.Now())
}
func (t timerTx) RenewTimer(ctx context.Context, timerKey string, owner string, lease time.Duration) (bool, error) {
	return renewTimer(ctx, t.tx, timerKey, owner, lease, time.Now())
}
func (t timerTx) DueTimers(ctx context.Context, now time.Time, limit int) ([]storage.Timer, error) {
	return dueTimers(ctx, t.tx, now, limit)
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

func escapeLike(s string) string {
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "%", "\\%")
	s = strings.ReplaceAll(s, "_", "\\_")
	return s
}

func putTimer(ctx context.Context, q querier, key string, dueAt time.Time, data []byte) error {
	now := time.Now().UTC()
	res, err := q.ExecContext(ctx,
		`INSERT INTO timer_store (key, value, due_at, owner, lease_expires_at, created_at, updated_at)
		 VALUES (?, ?, ?, NULL, NULL, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
		 ON CONFLICT(key) DO UPDATE SET
		   value = excluded.value,
		   due_at = excluded.due_at,
		   owner = NULL,
		   lease_expires_at = NULL,
		   updated_at = CURRENT_TIMESTAMP
		 WHERE timer_store.owner IS NULL OR timer_store.owner = '' OR timer_store.lease_expires_at IS NULL OR timer_store.lease_expires_at <= ?`,
		key, data, dueAt.UTC(), now,
	)
	if err != nil {
		return fmt.Errorf("sqlite put timer %q: %w", key, err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("sqlite put timer %q rows affected: %w", key, err)
	}
	if affected == 0 {
		var owner sql.NullString
		var leaseExpires sql.NullTime
		err := q.QueryRowContext(ctx, `SELECT owner, lease_expires_at FROM timer_store WHERE key = ?`, key).Scan(&owner, &leaseExpires)
		if err == sql.ErrNoRows {
			return fmt.Errorf("sqlite put timer %q: conflict without row", key)
		}
		if err != nil {
			return fmt.Errorf("sqlite put timer %q: %w", key, err)
		}
		if owner.Valid && owner.String != "" && leaseExpires.Valid && leaseExpires.Time.After(now) {
			return fmt.Errorf("%w: %s", storage.ErrTimerLeased, key)
		}
	}
	return nil
}

func getTimer(ctx context.Context, q querier, key string) ([]byte, error) {
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

func delTimer(ctx context.Context, q querier, key string) error {
	_, err := q.ExecContext(ctx, `DELETE FROM timer_store WHERE key = ?`, key)
	if err != nil {
		return fmt.Errorf("sqlite delete timer %q: %w", key, err)
	}
	return nil
}

func deleteTimerOwned(ctx context.Context, q querier, key string, owner string) (bool, error) {
	res, err := q.ExecContext(ctx, `DELETE FROM timer_store WHERE key = ? AND owner = ?`, key, owner)
	if err != nil {
		return false, fmt.Errorf("sqlite delete timer owned %q: %w", key, err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("sqlite delete timer owned %q rows affected: %w", key, err)
	}
	return affected == 1, nil
}

func renewTimer(ctx context.Context, q querier, key string, owner string, lease time.Duration, now time.Time) (bool, error) {
	leaseExpires := now.Add(lease).UTC()
	res, err := q.ExecContext(ctx,
		`UPDATE timer_store
		    SET lease_expires_at = ?, updated_at = CURRENT_TIMESTAMP
		  WHERE key = ?
		    AND owner = ?
		    AND lease_expires_at IS NOT NULL
		    AND lease_expires_at > ?`,
		leaseExpires, key, owner, now.UTC(),
	)
	if err != nil {
		return false, fmt.Errorf("sqlite renew timer %q: %w", key, err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("sqlite renew timer %q rows affected: %w", key, err)
	}
	return affected == 1, nil
}

func listTimers(ctx context.Context, q querier, prefix string) ([]string, error) {
	rows, err := q.QueryContext(ctx,
		`SELECT key FROM timer_store WHERE key LIKE ? ESCAPE '\' ORDER BY key`,
		escapeLike(prefix)+"%",
	)
	if err != nil {
		return nil, fmt.Errorf("sqlite list timer %q: %w", prefix, err)
	}
	defer rows.Close()

	keys := make([]string, 0)
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

func claimTimer(ctx context.Context, q querier, key string, owner string, lease time.Duration, now time.Time) (bool, error) {
	leaseExpires := now.Add(lease).UTC()
	res, err := q.ExecContext(ctx,
		`UPDATE timer_store
		    SET owner = ?, lease_expires_at = ?, updated_at = CURRENT_TIMESTAMP
		  WHERE key = ?
		    AND due_at IS NOT NULL
		    AND due_at <= ?
		    AND (owner IS NULL OR owner = '' OR lease_expires_at IS NULL OR lease_expires_at <= ?)`,
		owner, leaseExpires, key, now.UTC(), now.UTC(),
	)
	if err != nil {
		return false, fmt.Errorf("sqlite claim timer %q: %w", key, err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("sqlite claim timer %q rows affected: %w", key, err)
	}
	return affected == 1, nil
}

func dueTimers(ctx context.Context, q querier, now time.Time, limit int) ([]storage.Timer, error) {
	if limit <= 0 {
		return []storage.Timer{}, nil
	}
	rows, err := q.QueryContext(ctx,
		`SELECT key, due_at, owner, lease_expires_at, value
		   FROM timer_store
		  WHERE due_at IS NOT NULL
		    AND due_at <= ?
		    AND (owner IS NULL OR owner = '' OR lease_expires_at IS NULL OR lease_expires_at <= ?)
		  ORDER BY due_at ASC, key ASC
		  LIMIT ?`,
		now.UTC(), now.UTC(), limit,
	)
	if err != nil {
		return nil, fmt.Errorf("sqlite due timers: %w", err)
	}
	defer rows.Close()

	out := make([]storage.Timer, 0)
	for rows.Next() {
		var t storage.Timer
		var due sql.NullTime
		var owner sql.NullString
		var leaseExpires sql.NullTime
		if err := rows.Scan(&t.Key, &due, &owner, &leaseExpires, &t.Data); err != nil {
			return nil, fmt.Errorf("sqlite scan due timer: %w", err)
		}
		if due.Valid {
			t.DueAt = due.Time
		}
		if owner.Valid {
			t.Owner = owner.String
		}
		if leaseExpires.Valid {
			t.LeaseExpiresAt = leaseExpires.Time
		}
		t.Data = append([]byte(nil), t.Data...)
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite due timers rows: %w", err)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].DueAt.Equal(out[j].DueAt) {
			return out[i].Key < out[j].Key
		}
		return out[i].DueAt.Before(out[j].DueAt)
	})
	return out, nil
}
