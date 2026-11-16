package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"sort"
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

	if err := fn(&txOps{tx: tx}); err != nil {
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

	if err := fn(timerTx{tx: tx}); err != nil {
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
	if err := s.db.Close(); err != nil {
		return fmt.Errorf("postgres close: %w", err)
	}
	return nil
}

type txOps struct {
	tx *sql.Tx
}

func (t *txOps) Get(ctx context.Context, key string) ([]byte, error) { return get(ctx, t.tx, key) }
func (t *txOps) Put(ctx context.Context, key string, data []byte) error {
	return put(ctx, t.tx, key, data)
}
func (t *txOps) Delete(ctx context.Context, key string) error { return del(ctx, t.tx, key) }
func (t *txOps) CompareAndSwap(ctx context.Context, key string, old, new []byte) error {
	return compareAndSwap(ctx, t.tx, key, old, new)
}
func (t *txOps) List(ctx context.Context, prefix string) ([]string, error) {
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
func (t timerTx) ListTimers(ctx context.Context, prefix string) ([]string, error) {
	return listTimers(ctx, t.tx, prefix)
}
func (t timerTx) ClaimTimer(ctx context.Context, timerKey string, owner string, lease time.Duration) (bool, error) {
	return claimTimer(ctx, t.tx, timerKey, owner, lease, time.Now())
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
	var data []byte
	err := q.QueryRowContext(ctx, "SELECT value FROM kv_store WHERE key = $1", key).Scan(&data)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("%w: %s", storage.ErrNotFound, key)
	}
	if err != nil {
		return nil, fmt.Errorf("postgres get %q: %w", key, err)
	}
	return data, nil
}

func put(ctx context.Context, q querier, key string, data []byte) error {
	_, err := q.ExecContext(ctx, "INSERT INTO kv_store (key, value, updated_at) VALUES ($1, $2, NOW()) ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = NOW()", key, data)
	if err != nil {
		return fmt.Errorf("postgres put %q: %w", key, err)
	}
	return nil
}

func del(ctx context.Context, q querier, key string) error {
	_, err := q.ExecContext(ctx, "DELETE FROM kv_store WHERE key = $1", key)
	if err != nil {
		return fmt.Errorf("postgres delete %q: %w", key, err)
	}
	return nil
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

func list(ctx context.Context, q querier, prefix string) ([]string, error) {
	pattern := escapeLike(prefix) + "%"
	rows, err := q.QueryContext(ctx, "SELECT key FROM kv_store WHERE key LIKE $1 ESCAPE '\\' ORDER BY key", pattern)
	if err != nil {
		return nil, fmt.Errorf("postgres list %q: %w", prefix, err)
	}
	defer rows.Close()
	keys := make([]string, 0)
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

func escapeLike(s string) string {
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "%", "\\%")
	s = strings.ReplaceAll(s, "_", "\\_")
	return s
}

func putTimer(ctx context.Context, q querier, key string, dueAt time.Time, data []byte) error {
	_, err := q.ExecContext(ctx,
		`INSERT INTO timer_store (key, value, due_at, owner, lease_expires_at, created_at, updated_at)
		 VALUES ($1, $2, $3, NULL, NULL, NOW(), NOW())
		 ON CONFLICT (key) DO UPDATE SET
		   value = EXCLUDED.value,
		   due_at = EXCLUDED.due_at,
		   owner = NULL,
		   lease_expires_at = NULL,
		   updated_at = NOW()`,
		key, data, dueAt.UTC(),
	)
	if err != nil {
		return fmt.Errorf("postgres put timer %q: %w", key, err)
	}
	return nil
}

func getTimer(ctx context.Context, q querier, key string) ([]byte, error) {
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

func delTimer(ctx context.Context, q querier, key string) error {
	_, err := q.ExecContext(ctx, `DELETE FROM timer_store WHERE key = $1`, key)
	if err != nil {
		return fmt.Errorf("postgres delete timer %q: %w", key, err)
	}
	return nil
}

func listTimers(ctx context.Context, q querier, prefix string) ([]string, error) {
	pattern := escapeLike(prefix) + "%"
	rows, err := q.QueryContext(ctx, `SELECT key FROM timer_store WHERE key LIKE $1 ESCAPE '\\' ORDER BY key`, pattern)
	if err != nil {
		return nil, fmt.Errorf("postgres list timer %q: %w", prefix, err)
	}
	defer rows.Close()
	keys := make([]string, 0)
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

func claimTimer(ctx context.Context, q querier, key string, owner string, lease time.Duration, now time.Time) (bool, error) {
	leaseExpires := now.Add(lease).UTC()
	res, err := q.ExecContext(ctx,
		`UPDATE timer_store
		    SET owner = $1, lease_expires_at = $2, updated_at = NOW()
		  WHERE key = $3
		    AND due_at IS NOT NULL
		    AND due_at <= $4
		    AND (owner IS NULL OR owner = '' OR lease_expires_at IS NULL OR lease_expires_at <= $4)`,
		owner, leaseExpires, key, now.UTC(),
	)
	if err != nil {
		return false, fmt.Errorf("postgres claim timer %q: %w", key, err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("postgres claim timer %q rows affected: %w", key, err)
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
		    AND due_at <= $1
		    AND (owner IS NULL OR owner = '' OR lease_expires_at IS NULL OR lease_expires_at <= $1)
		  ORDER BY due_at ASC, key ASC
		  LIMIT $2`,
		now.UTC(), limit,
	)
	if err != nil {
		return nil, fmt.Errorf("postgres due timers: %w", err)
	}
	defer rows.Close()

	out := make([]storage.Timer, 0)
	for rows.Next() {
		var t storage.Timer
		var due sql.NullTime
		var owner sql.NullString
		var leaseExpires sql.NullTime
		if err := rows.Scan(&t.Key, &due, &owner, &leaseExpires, &t.Data); err != nil {
			return nil, fmt.Errorf("postgres scan due timer: %w", err)
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
		return nil, fmt.Errorf("postgres due timers rows: %w", err)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].DueAt.Equal(out[j].DueAt) {
			return out[i].Key < out[j].Key
		}
		return out[i].DueAt.Before(out[j].DueAt)
	})
	return out, nil
}
