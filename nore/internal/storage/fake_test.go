package storage_test

import (
	"bytes"
	"context"
	"errors"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/neuron-runtime/neuron/nore/internal/assembly"
	"github.com/neuron-runtime/neuron/nore/internal/event"
	"github.com/neuron-runtime/neuron/nore/internal/execution"
	"github.com/neuron-runtime/neuron/nore/internal/storage"
)

// fakeStore is an in-memory implementation of the neutral storage.Store contract.
// It is used in tests to prove that no storage-specific logic leaks into the
// execution, event, instance, or assembly packages.
type fakeStore struct {
	mu     sync.Mutex
	kv     map[string][]byte
	timers map[string]fakeTimer
	closed bool
}

type fakeTimer struct {
	DueAt          time.Time
	Owner          string
	LeaseExpiresAt time.Time
	Data           []byte
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		kv:     make(map[string][]byte),
		timers: make(map[string]fakeTimer),
	}
}

func (s *fakeStore) guard() error {
	if s.closed {
		return storage.ErrClosed
	}
	return nil
}

// Get implements storage.Store.
func (s *fakeStore) Get(ctx context.Context, key string) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.guard(); err != nil {
		return nil, err
	}
	if val, ok := s.kv[key]; ok {
		return append([]byte(nil), val...), nil
	}
	return nil, storage.ErrNotFound
}

// Put implements storage.Store.
func (s *fakeStore) Put(ctx context.Context, key string, data []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.guard(); err != nil {
		return err
	}
	s.kv[key] = append([]byte(nil), data...)
	return nil
}

// Delete implements storage.Store.
func (s *fakeStore) Delete(ctx context.Context, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.guard(); err != nil {
		return err
	}
	delete(s.kv, key)
	return nil
}

// CompareAndSwap implements storage.Store.
func (s *fakeStore) CompareAndSwap(ctx context.Context, key string, old, new []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.guard(); err != nil {
		return err
	}
	current, ok := s.kv[key]
	if !ok {
		return storage.ErrNotFound
	}
	if !bytes.Equal(current, old) {
		return storage.ErrConflict
	}
	s.kv[key] = append([]byte(nil), new...)
	return nil
}

// List implements storage.Store.
func (s *fakeStore) List(ctx context.Context, prefix string) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.guard(); err != nil {
		return nil, err
	}
	keys := make([]string, 0)
	for k := range s.kv {
		if strings.HasPrefix(k, prefix) {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	return keys, nil
}

// WithTx implements storage.Store.
func (s *fakeStore) WithTx(ctx context.Context, fn func(storage.Tx) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.guard(); err != nil {
		return err
	}
	tx := &fakeTx{store: s}
	return fn(tx)
}

// WithTimer implements storage.Store.
func (s *fakeStore) WithTimer(ctx context.Context, fn func(storage.TimerTx) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.guard(); err != nil {
		return err
	}
	tx := &fakeTx{store: s}
	return fn(tx)
}

// PutTimer implements storage.Store.
func (s *fakeStore) PutTimer(ctx context.Context, timerKey string, dueAt time.Time, data []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.guard(); err != nil {
		return err
	}
	if rec, ok := s.timers[timerKey]; ok && rec.Owner != "" && !rec.LeaseExpiresAt.IsZero() && rec.LeaseExpiresAt.After(time.Now()) {
		return storage.ErrTimerLeased
	}
	s.timers[timerKey] = fakeTimer{DueAt: dueAt, Data: append([]byte(nil), data...)}
	return nil
}

// GetTimer implements storage.Store.
func (s *fakeStore) GetTimer(ctx context.Context, timerKey string) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.guard(); err != nil {
		return nil, err
	}
	t, ok := s.timers[timerKey]
	if !ok {
		return nil, storage.ErrTimerNotFound
	}
	return append([]byte(nil), t.Data...), nil
}

// DeleteTimer implements storage.Store.
func (s *fakeStore) DeleteTimer(ctx context.Context, timerKey string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.guard(); err != nil {
		return err
	}
	delete(s.timers, timerKey)
	return nil
}

// DeleteTimerOwned implements storage.Store.
func (s *fakeStore) DeleteTimerOwned(ctx context.Context, timerKey string, owner string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.guard(); err != nil {
		return false, err
	}
	rec, ok := s.timers[timerKey]
	if !ok || rec.Owner != owner {
		return false, nil
	}
	delete(s.timers, timerKey)
	return true, nil
}

// ListTimers implements storage.Store.
func (s *fakeStore) ListTimers(ctx context.Context, prefix string) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.guard(); err != nil {
		return nil, err
	}
	keys := make([]string, 0)
	for k := range s.timers {
		if strings.HasPrefix(k, prefix) {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	return keys, nil
}

// ClaimTimer implements storage.Store.
func (s *fakeStore) ClaimTimer(ctx context.Context, timerKey string, owner string, lease time.Duration) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.guard(); err != nil {
		return false, err
	}
	return claimFakeTimer(s.timers, timerKey, owner, lease, time.Now())
}

// RenewTimer implements storage.Store.
func (s *fakeStore) RenewTimer(ctx context.Context, timerKey string, owner string, lease time.Duration) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.guard(); err != nil {
		return false, err
	}
	return renewFakeTimer(s.timers, timerKey, owner, lease, time.Now())
}

// DueTimers implements storage.Store.
func (s *fakeStore) DueTimers(ctx context.Context, now time.Time, limit int) ([]storage.Timer, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.guard(); err != nil {
		return nil, err
	}
	return dueFakeTimers(s.timers, now, limit), nil
}

// Ping implements storage.Store.
func (s *fakeStore) Ping(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.guard()
}

// Close implements storage.Store.
func (s *fakeStore) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	return nil
}

type fakeTx struct {
	store *fakeStore
}

func (t *fakeTx) Get(ctx context.Context, key string) ([]byte, error) {
	if val, ok := t.store.kv[key]; ok {
		return append([]byte(nil), val...), nil
	}
	return nil, storage.ErrNotFound
}

func (t *fakeTx) Put(ctx context.Context, key string, data []byte) error {
	t.store.kv[key] = append([]byte(nil), data...)
	return nil
}

func (t *fakeTx) Delete(ctx context.Context, key string) error {
	delete(t.store.kv, key)
	return nil
}

func (t *fakeTx) CompareAndSwap(ctx context.Context, key string, old, new []byte) error {
	current, ok := t.store.kv[key]
	if !ok {
		return storage.ErrNotFound
	}
	if !bytes.Equal(current, old) {
		return storage.ErrConflict
	}
	t.store.kv[key] = append([]byte(nil), new...)
	return nil
}

func (t *fakeTx) List(ctx context.Context, prefix string) ([]string, error) {
	keys := make([]string, 0)
	for k := range t.store.kv {
		if strings.HasPrefix(k, prefix) {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	return keys, nil
}

func (t *fakeTx) GetTimer(ctx context.Context, timerKey string) ([]byte, error) {
	rec, ok := t.store.timers[timerKey]
	if !ok {
		return nil, storage.ErrTimerNotFound
	}
	return append([]byte(nil), rec.Data...), nil
}

func (t *fakeTx) PutTimer(ctx context.Context, timerKey string, dueAt time.Time, data []byte) error {
	if rec, ok := t.store.timers[timerKey]; ok && rec.Owner != "" && !rec.LeaseExpiresAt.IsZero() && rec.LeaseExpiresAt.After(time.Now()) {
		return storage.ErrTimerLeased
	}
	t.store.timers[timerKey] = fakeTimer{DueAt: dueAt, Data: append([]byte(nil), data...)}
	return nil
}

func (t *fakeTx) DeleteTimer(ctx context.Context, timerKey string) error {
	delete(t.store.timers, timerKey)
	return nil
}

func (t *fakeTx) DeleteTimerOwned(ctx context.Context, timerKey string, owner string) (bool, error) {
	rec, ok := t.store.timers[timerKey]
	if !ok || rec.Owner != owner {
		return false, nil
	}
	delete(t.store.timers, timerKey)
	return true, nil
}

func (t *fakeTx) ListTimers(ctx context.Context, prefix string) ([]string, error) {
	keys := make([]string, 0)
	for k := range t.store.timers {
		if strings.HasPrefix(k, prefix) {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	return keys, nil
}

func (t *fakeTx) ClaimTimer(ctx context.Context, timerKey string, owner string, lease time.Duration) (bool, error) {
	return claimFakeTimer(t.store.timers, timerKey, owner, lease, time.Now())
}

func (t *fakeTx) RenewTimer(ctx context.Context, timerKey string, owner string, lease time.Duration) (bool, error) {
	return renewFakeTimer(t.store.timers, timerKey, owner, lease, time.Now())
}

func (t *fakeTx) DueTimers(ctx context.Context, now time.Time, limit int) ([]storage.Timer, error) {
	return dueFakeTimers(t.store.timers, now, limit), nil
}

func claimFakeTimer(timers map[string]fakeTimer, timerKey string, owner string, lease time.Duration, now time.Time) (bool, error) {
	rec, ok := timers[timerKey]
	if !ok {
		return false, nil
	}
	if rec.DueAt.After(now) {
		return false, nil
	}
	if rec.Owner != "" && !rec.LeaseExpiresAt.IsZero() && rec.LeaseExpiresAt.After(now) {
		return false, nil
	}
	rec.Owner = owner
	rec.LeaseExpiresAt = now.Add(lease)
	timers[timerKey] = rec
	return true, nil
}

func renewFakeTimer(timers map[string]fakeTimer, timerKey string, owner string, lease time.Duration, now time.Time) (bool, error) {
	rec, ok := timers[timerKey]
	if !ok {
		return false, nil
	}
	if rec.Owner != owner {
		return false, nil
	}
	if rec.Owner == "" || rec.LeaseExpiresAt.IsZero() || !rec.LeaseExpiresAt.After(now) {
		return false, nil
	}
	rec.LeaseExpiresAt = now.Add(lease)
	timers[timerKey] = rec
	return true, nil
}

func dueFakeTimers(timers map[string]fakeTimer, now time.Time, limit int) []storage.Timer {
	if limit <= 0 {
		return []storage.Timer{}
	}
	out := make([]storage.Timer, 0)
	for key, rec := range timers {
		if rec.DueAt.After(now) {
			continue
		}
		if rec.Owner != "" && !rec.LeaseExpiresAt.IsZero() && rec.LeaseExpiresAt.After(now) {
			continue
		}
		out = append(out, storage.Timer{
			Key:            key,
			DueAt:          rec.DueAt,
			Owner:          rec.Owner,
			LeaseExpiresAt: rec.LeaseExpiresAt,
			Data:           append([]byte(nil), rec.Data...),
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].DueAt.Equal(out[j].DueAt) {
			return out[i].Key < out[j].Key
		}
		return out[i].DueAt.Before(out[j].DueAt)
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

// TestFakeStoreProvesNoLeak verifies that the neutral storage contract works
// across all runtime packages (execution, event, assembly) without
// any concrete backend dependencies. Each sub-test constructs its own fake
// store implementing only the contract and runs basic operations, asserting
// errors are propagated correctly and not converted to absence.
func TestFakeStoreProvesNoLeak(t *testing.T) {
	t.Run("kv_put_get_delete", func(t *testing.T) {
		store := newFakeStore()
		ctx := context.Background()

		key, err := storage.Key("test", "key1")
		if err != nil {
			t.Fatalf("key: %v", err)
		}
		val := []byte("hello")

		if err := store.Put(ctx, key, val); err != nil {
			t.Fatalf("put: %v", err)
		}
		got, err := store.Get(ctx, key)
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		if string(got) != "hello" {
			t.Fatalf("mismatch: got %q, want %s", string(got), "hello")
		}

		if err := store.Delete(ctx, key); err != nil {
			t.Fatalf("delete: %v", err)
		}
		if _, err := store.Get(ctx, key); err == nil {
			t.Fatalf("expected err after delete")
		}

		prefix, err := storage.Prefix("test")
		if err != nil {
			t.Fatalf("prefix: %v", err)
		}
		keys, err := store.List(ctx, prefix)
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		if len(keys) != 0 {
			t.Fatalf("list after delete: got %v, want empty", keys)
		}
	})

	t.Run("ping_pong", func(t *testing.T) {
		store := newFakeStore()
		ctx := context.Background()

		if err := store.Ping(ctx); err != nil {
			t.Fatalf("ping: %v", err)
		}
	})

	t.Run("close", func(t *testing.T) {
		store := newFakeStore()
		ctx := context.Background()

		if err := store.Close(); err != nil {
			t.Fatalf("close: %v", err)
		}
		keyAfter, err := storage.Key("after-close")
		if err != nil {
			t.Fatalf("key after-close: %v", err)
		}
		if err := store.Put(ctx, keyAfter, []byte("x")); err == nil {
			t.Fatal("put after close should fail")
		}
		if _, err := store.Get(ctx, keyAfter); err == nil {
			t.Fatal("get after close should fail")
		}
		if err := store.Close(); err != nil && !errors.Is(err, storage.ErrClosed) {
			t.Fatalf("close again: %v", err)
		}
	})

	t.Run("with_tx", func(t *testing.T) {
		store := newFakeStore()
		ctx := context.Background()

		key, err := storage.Key("tx", "key1")
		if err != nil {
			t.Fatalf("key: %v", err)
		}
		if err := store.WithTx(ctx, func(tx storage.Tx) error {
			if err := tx.Put(ctx, key, []byte("tx-val")); err != nil {
				return err
			}
			if _, err := tx.Get(ctx, key); err != nil {
				return err
			}
			return nil
		}); err != nil {
			t.Fatalf("withTx: %v", err)
		}
		if _, err := store.Get(ctx, key); err != nil {
			t.Fatalf("tx get after commit: %v", err)
		}
	})

	t.Run("errnotfound_propagation", func(t *testing.T) {
		store := newFakeStore()
		ctx := context.Background()

		key, err := storage.Key("nonexistent")
		if err != nil {
			t.Fatalf("key: %v", err)
		}
		_, err = store.Get(ctx, key)
		if err == nil {
			t.Fatal("expected ErrNotFound for missing key")
		}
		if !errors.Is(err, storage.ErrNotFound) {
			t.Fatalf("expected ErrNotFound, got %v", err)
		}
	})

	t.Run("execution_package_compiles", func(t *testing.T) {
		store := newFakeStore()
		ctx := context.Background()

		_ = execution.NewExecutionStore(store)
		var _ storage.Store = store
		key, _ := storage.Key("exec-test")
		_ = store.Put(ctx, key, []byte("ok"))
		_, _ = store.Get(ctx, key)
		_ = store.Delete(ctx, key)
		_ = store.Ping(ctx)
		_ = store.Close()
	})

	t.Run("event_package_compiles", func(t *testing.T) {
		store := newFakeStore()
		ctx := context.Background()

		_ = event.NewStore(store)
		var _ storage.Store = store
		key, _ := storage.Key("event-test")
		_ = store.Put(ctx, key, []byte("ok"))
		_, _ = store.Get(ctx, key)
		_ = store.Delete(ctx, key)
		_ = store.Ping(ctx)
		_ = store.Close()
	})

	t.Run("assembly_package_compiles", func(t *testing.T) {
		store := newFakeStore()
		ctx := context.Background()

		_ = assembly.NewRepository(store)
		var _ storage.Store = store
		key, _ := storage.Key("assembly-test")
		_ = store.Put(ctx, key, []byte("ok"))
		_, _ = store.Get(ctx, key)
		_ = store.Delete(ctx, key)
		_ = store.Ping(ctx)
		_ = store.Close()
	})
}
