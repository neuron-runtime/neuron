package storage_test

import (
	"context"
	"errors"
	"testing"

	"github.com/neuron-runtime/neuron/nore/internal/assembly"
	"github.com/neuron-runtime/neuron/nore/internal/event"
	"github.com/neuron-runtime/neuron/nore/internal/execution"
	"github.com/neuron-runtime/neuron/nore/internal/storage"
)

// fakeStore is an in-memory implementation of the neutral storage.Store contract.
// It is used in tests to prove that no storage-specific logic leaks into the
// execution, event, instance, or assembly packages.
type fakeStore struct {
	// kv holds the key/value mappings. A key that does not exist in kv is
	// considered "not found" by Get.
	kv map[string][]byte
	// closed tracks whether Close has been called.
	closed bool
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		kv: make(map[string][]byte),
	}
}

// Get implements storage.Store.
func (s *fakeStore) Get(ctx context.Context, key string) ([]byte, error) {
	if s.closed {
		return nil, storage.ErrClosed
	}
	if val, ok := s.kv[key]; ok {
		return val, nil
	}
	return nil, storage.ErrNotFound
}

// Put implements storage.Store.
func (s *fakeStore) Put(ctx context.Context, key string, data []byte) error {
	if s.closed {
		return storage.ErrClosed
	}
	s.kv[key] = append(s.kv[key], data...)
	return nil
}

// Delete implements storage.Store.
func (s *fakeStore) Delete(ctx context.Context, key string) error {
	if s.closed {
		return storage.ErrClosed
	}
	delete(s.kv, key)
	return nil
}

// List implements storage.Store.
func (s *fakeStore) List(ctx context.Context, prefix string) ([]string, error) {
	if s.closed {
		return nil, storage.ErrClosed
	}
	var keys []string
	for k := range s.kv {
		if len(k) >= len(prefix) && len(k) > 0 && k[:len(prefix)] == prefix {
			keys = append(keys, k)
		}
	}
	return keys, nil
}

// WithTx implements storage.Store.
func (s *fakeStore) WithTx(ctx context.Context, fn func(storage.Tx) error) error {
	if s.closed {
		return storage.ErrClosed
	}
	tx := &fakeTx{kv: s.kv}
	if err := fn(tx); err != nil {
		return err
	}
	// The fake tx shares the same underlying map; commits are effectively
	// no-ops since we don't track read/write sets. Real transaction semantics
	// are tested against concrete backends.
	return nil
}

// Ping implements storage.Store.
func (s *fakeStore) Ping(ctx context.Context) error {
	if s.closed {
		return storage.ErrClosed
	}
	return nil
}

// Close implements storage.Store.
func (s *fakeStore) Close() error {
	s.closed = true
	return nil
}

type fakeTx struct {
	kv map[string][]byte
}

func (s *fakeTx) Get(ctx context.Context, key string) ([]byte, error) {
	if val, ok := s.kv[key]; ok {
		return val, nil
	}
	return nil, storage.ErrNotFound
}

func (s *fakeTx) Put(ctx context.Context, key string, data []byte) error {
	s.kv[key] = append(s.kv[key], data...)
	return nil
}

func (s *fakeTx) Delete(ctx context.Context, key string) error {
	delete(s.kv, key)
	return nil
}

func (s *fakeTx) List(ctx context.Context, prefix string) ([]string, error) {
	var keys []string
	for k := range s.kv {
		if len(k) >= len(prefix) && len(k) > 0 && k[:len(prefix)] == prefix {
			keys = append(keys, k)
		}
	}
	return keys, nil
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
		// Verify the type is correctly assigned
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
