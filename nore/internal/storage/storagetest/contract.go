// Package storagetest defines the backend-neutral storage contract as an
// executable suite. A backend runs Run with its own constructor so every
// backend proves the same transaction and error semantics.
package storagetest

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/neuron-runtime/neuron/nore/internal/storage"
)

// Run exercises the storage contract against one backend. open must return a
// fresh, empty store for each call and must arrange its own cleanup.
func Run(t *testing.T, open func(t *testing.T) storage.Store) {
	t.Helper()

	t.Run("key value basics", func(t *testing.T) {
		store := open(t)
		ctx := context.Background()
		key := mustKey(t, "contract", "kv")
		other := mustKey(t, "contractor")

		if _, err := store.Get(ctx, key); !errors.Is(err, storage.ErrNotFound) {
			t.Fatalf("get missing key = %v, want ErrNotFound", err)
		}
		if err := store.Put(ctx, key, []byte("one")); err != nil {
			t.Fatalf("put key: %v", err)
		}
		if err := store.Put(ctx, other, []byte("outside")); err != nil {
			t.Fatalf("put sibling key: %v", err)
		}
		if got, err := store.Get(ctx, key); err != nil || string(got) != "one" {
			t.Fatalf("get key = %q, %v; want one", got, err)
		}
		if err := store.Put(ctx, key, []byte("two")); err != nil {
			t.Fatalf("overwrite key: %v", err)
		}
		if got, err := store.Get(ctx, key); err != nil || string(got) != "two" {
			t.Fatalf("get overwritten key = %q, %v; want two", got, err)
		}

		keys, err := store.List(ctx, mustPrefix(t, "contract"))
		if err != nil {
			t.Fatalf("list prefix: %v", err)
		}
		if len(keys) != 1 || keys[0] != key {
			t.Fatalf("list prefix = %v, want [%s]", keys, key)
		}

		if err := store.Delete(ctx, key); err != nil {
			t.Fatalf("delete key: %v", err)
		}
		if _, err := store.Get(ctx, key); !errors.Is(err, storage.ErrNotFound) {
			t.Fatalf("get deleted key = %v, want ErrNotFound", err)
		}
	})

	t.Run("transaction commits", func(t *testing.T) {
		store := open(t)
		ctx := context.Background()
		first := mustKey(t, "tx", "first")
		second := mustKey(t, "tx", "second")

		err := store.WithTx(ctx, func(tx storage.Tx) error {
			if err := tx.Put(ctx, first, []byte("a")); err != nil {
				return err
			}
			return tx.Put(ctx, second, []byte("b"))
		})
		if err != nil {
			t.Fatalf("commit transaction: %v", err)
		}
		for key, want := range map[string]string{first: "a", second: "b"} {
			if got, err := store.Get(ctx, key); err != nil || string(got) != want {
				t.Fatalf("get committed %s = %q, %v; want %q", key, got, err, want)
			}
		}
	})

	t.Run("transaction rolls back", func(t *testing.T) {
		store := open(t)
		ctx := context.Background()
		existing := mustKey(t, "tx", "existing")
		created := mustKey(t, "tx", "created")
		abort := errors.New("abort transaction")

		if err := store.Put(ctx, existing, []byte("before")); err != nil {
			t.Fatalf("seed key: %v", err)
		}
		err := store.WithTx(ctx, func(tx storage.Tx) error {
			if err := tx.Put(ctx, existing, []byte("after")); err != nil {
				return err
			}
			if err := tx.Put(ctx, created, []byte("value")); err != nil {
				return err
			}
			return abort
		})
		if !errors.Is(err, abort) {
			t.Fatalf("transaction error = %v, want %v", err, abort)
		}
		if got, err := store.Get(ctx, existing); err != nil || string(got) != "before" {
			t.Fatalf("rolled-back existing = %q, %v; want before", got, err)
		}
		if _, err := store.Get(ctx, created); !errors.Is(err, storage.ErrNotFound) {
			t.Fatalf("rolled-back created key = %v, want ErrNotFound", err)
		}
	})

	t.Run("transaction callback panic rolls back", func(t *testing.T) {
		store := open(t)
		ctx := context.Background()
		key := mustKey(t, "tx", "panic")

		func() {
			defer func() {
				if recover() == nil {
					t.Fatal("panic in transaction callback was swallowed")
				}
			}()
			_ = store.WithTx(ctx, func(tx storage.Tx) error {
				if err := tx.Put(ctx, key, []byte("value")); err != nil {
					t.Fatalf("put in panic transaction: %v", err)
				}
				panic("boom")
			})
		}()

		if _, err := store.Get(ctx, key); !errors.Is(err, storage.ErrNotFound) {
			t.Fatalf("key written by panicking transaction = %v, want ErrNotFound", err)
		}
	})

	t.Run("nested and re-entrant operations are explicit", func(t *testing.T) {
		store := open(t)
		ctx := context.Background()
		key := mustKey(t, "tx", "nested")

		err := store.WithTx(ctx, func(tx storage.Tx) error {
			if _, err := store.Get(ctx, key); !errors.Is(err, storage.ErrTransactionActive) {
				t.Fatalf("plain Get during transaction = %v, want ErrTransactionActive", err)
			}
			if err := store.Put(ctx, key, []byte("blocked")); !errors.Is(err, storage.ErrTransactionActive) {
				t.Fatalf("plain Put during transaction = %v, want ErrTransactionActive", err)
			}
			if err := store.WithTx(ctx, func(storage.Tx) error { return nil }); !errors.Is(err, storage.ErrNestedTransaction) {
				t.Fatalf("nested WithTx = %v, want ErrNestedTransaction", err)
			}
			return tx.Put(ctx, key, []byte("committed"))
		})
		if err != nil {
			t.Fatalf("outer transaction: %v", err)
		}
		if got, err := store.Get(ctx, key); err != nil || string(got) != "committed" {
			t.Fatalf("outer transaction write = %q, %v; want committed", got, err)
		}
	})

	t.Run("concurrent transactions serialize", func(t *testing.T) {
		store := open(t)
		ctx := context.Background()
		key := mustKey(t, "tx", "concurrent")

		start := make(chan struct{})
		errs := make(chan error, 2)
		var wg sync.WaitGroup
		for _, value := range []string{"first", "second"} {
			value := value
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				errs <- store.WithTx(ctx, func(tx storage.Tx) error {
					return tx.Put(ctx, key, []byte(value))
				})
			}()
		}
		close(start)
		wg.Wait()
		close(errs)

		for err := range errs {
			if err != nil {
				t.Fatalf("concurrent WithTx = %v, want success", err)
			}
		}
		if _, err := store.Get(ctx, key); err != nil {
			t.Fatalf("get after concurrent transactions: %v", err)
		}
	})

	t.Run("concurrent plain operations wait for transaction", func(t *testing.T) {
		store := open(t)
		ctx := context.Background()
		key := mustKey(t, "tx", "plain-concurrent")

		txStarted := make(chan struct{})
		release := make(chan struct{})
		txErr := make(chan error, 1)
		go func() {
			txErr <- store.WithTx(ctx, func(tx storage.Tx) error {
				close(txStarted)
				<-release
				return tx.Put(ctx, key, []byte("transaction"))
			})
		}()
		<-txStarted

		plainErr := make(chan error, 1)
		go func() {
			plainErr <- store.Put(ctx, key, []byte("plain"))
		}()

		select {
		case err := <-plainErr:
			t.Fatalf("plain Put returned while the transaction was active: %v", err)
		case <-time.After(50 * time.Millisecond):
		}

		close(release)
		if err := <-txErr; err != nil {
			t.Fatalf("transaction: %v", err)
		}
		if err := <-plainErr; err != nil {
			t.Fatalf("concurrent plain Put: %v", err)
		}
		if got, err := store.Get(ctx, key); err != nil || string(got) != "plain" {
			t.Fatalf("get after concurrent Put = %q, %v; want plain", got, err)
		}
	})

	t.Run("cancelled context aborts", func(t *testing.T) {
		store := open(t)
		key := mustKey(t, "tx", "cancelled")

		cancelled, cancel := context.WithCancel(context.Background())
		cancel()
		called := false
		err := store.WithTx(cancelled, func(tx storage.Tx) error {
			called = true
			return tx.Put(cancelled, key, []byte("value"))
		})
		if err == nil {
			t.Fatal("WithTx with cancelled context succeeded")
		}
		if called {
			t.Fatal("WithTx invoked callback with an already-cancelled context")
		}
		if _, err := store.Get(context.Background(), key); !errors.Is(err, storage.ErrNotFound) {
			t.Fatalf("key after cancelled transaction = %v, want ErrNotFound", err)
		}

		ctx, cancel := context.WithCancel(context.Background())
		err = store.WithTx(ctx, func(tx storage.Tx) error {
			if err := tx.Put(context.Background(), key, []byte("value")); err != nil {
				return err
			}
			cancel()
			return nil
		})
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("WithTx cancellation error = %v, want context.Canceled", err)
		}
		if _, err := store.Get(context.Background(), key); !errors.Is(err, storage.ErrNotFound) {
			t.Fatalf("key after mid-transaction cancellation = %v, want ErrNotFound", err)
		}

		putCtx, putCancel := context.WithCancel(context.Background())
		putCancel()
		if err := store.Put(putCtx, key, []byte("value")); err == nil {
			t.Fatal("Put with cancelled context succeeded")
		}
		if _, err := store.Get(context.Background(), key); !errors.Is(err, storage.ErrNotFound) {
			t.Fatalf("key after cancelled Put = %v, want ErrNotFound", err)
		}
	})

	t.Run("lifecycle", func(t *testing.T) {
		store := open(t)
		ctx := context.Background()
		if err := store.Ping(ctx); err != nil {
			t.Fatalf("ping: %v", err)
		}
		if err := store.Close(); err != nil {
			t.Fatalf("close: %v", err)
		}
		key := mustKey(t, "lifecycle")
		if err := store.Put(ctx, key, []byte("value")); !errors.Is(err, storage.ErrClosed) {
			t.Fatalf("put after close = %v, want ErrClosed", err)
		}
		if _, err := store.Get(ctx, key); !errors.Is(err, storage.ErrClosed) {
			t.Fatalf("get after close = %v, want ErrClosed", err)
		}
		if _, err := store.List(ctx, mustPrefix(t, "lifecycle")); !errors.Is(err, storage.ErrClosed) {
			t.Fatalf("list after close = %v, want ErrClosed", err)
		}
		if err := store.Ping(ctx); !errors.Is(err, storage.ErrClosed) {
			t.Fatalf("ping after close = %v, want ErrClosed", err)
		}
	})

	t.Run("transaction failure injection", func(t *testing.T) {
		store := open(t)
		ctx := context.Background()
		existing := mustKey(t, "tx", "existing-fi")
		created := mustKey(t, "tx", "created-fi")
		if err := store.Put(ctx, existing, []byte("before")); err != nil {
			t.Fatalf("seed key: %v", err)
		}
		boom := errors.New("boom")
		err := store.WithTx(ctx, func(tx storage.Tx) error {
			if err := tx.Put(ctx, existing, []byte("after")); err != nil {
				return err
			}
			if err := tx.Delete(ctx, created); err != nil {
				// Delete may be no-op; ignore
			}
			if err := tx.Put(ctx, created, []byte("temp")); err != nil {
				return err
			}
			if _, err := tx.Get(ctx, existing); err != nil {
				return err
			}
			return boom
		})
		if !errors.Is(err, boom) {
			t.Fatalf("transaction error = %v, want %v", err, boom)
		}
		if got, err := store.Get(ctx, existing); err != nil || string(got) != "before" {
			t.Fatalf("rolled-back existing = %q, %v; want before", got, err)
		}
		if _, err := store.Get(ctx, created); !errors.Is(err, storage.ErrNotFound) {
			t.Fatalf("rolled-back created key = %v, want ErrNotFound", err)
		}
	})
}

func mustKey(t *testing.T, parts ...string) string {
	t.Helper()
	key, err := storage.Key(parts...)
	if err != nil {
		t.Fatalf("storage.Key(%v): %v", parts, err)
	}
	return key
}

func mustPrefix(t *testing.T, parts ...string) string {
	t.Helper()
	prefix, err := storage.Prefix(parts...)
	if err != nil {
		t.Fatalf("storage.Prefix(%v): %v", parts, err)
	}
	return prefix
}
