// Package storagetest defines the backend-neutral storage contract as an
// executable suite. A backend runs Run with its own constructor so every
// backend proves the same transaction and error semantics.
package storagetest

import (
	"context"
	"errors"
	"fmt"
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

	t.Run("compare and swap", func(t *testing.T) {
		store := open(t)
		ctx := context.Background()
		key := mustKey(t, "cas", "value")

		if err := store.CompareAndSwap(ctx, key, []byte("old"), []byte("new")); !errors.Is(err, storage.ErrNotFound) {
			t.Fatalf("compare and swap missing key = %v, want ErrNotFound", err)
		}
		if err := store.Put(ctx, key, []byte("old")); err != nil {
			t.Fatalf("seed key: %v", err)
		}
		if err := store.CompareAndSwap(ctx, key, []byte("stale"), []byte("new")); !errors.Is(err, storage.ErrConflict) {
			t.Fatalf("compare and swap stale value = %v, want ErrConflict", err)
		}
		if got, err := store.Get(ctx, key); err != nil || string(got) != "old" {
			t.Fatalf("value after conflict = %q, %v; want old", got, err)
		}
		if err := store.CompareAndSwap(ctx, key, []byte("old"), []byte("new")); err != nil {
			t.Fatalf("compare and swap current value: %v", err)
		}
		if got, err := store.Get(ctx, key); err != nil || string(got) != "new" {
			t.Fatalf("value after swap = %q, %v; want new", got, err)
		}
		if err := store.CompareAndSwap(ctx, key, []byte("new"), []byte("new")); err != nil {
			t.Fatalf("compare and swap identical value: %v", err)
		}
	})

	t.Run("compare and swap transaction commits", func(t *testing.T) {
		store := open(t)
		ctx := context.Background()
		key := mustKey(t, "cas", "commit")

		if err := store.Put(ctx, key, []byte("before")); err != nil {
			t.Fatalf("seed key: %v", err)
		}
		err := store.WithTx(ctx, func(tx storage.Tx) error {
			return tx.CompareAndSwap(ctx, key, []byte("before"), []byte("after"))
		})
		if err != nil {
			t.Fatalf("compare and swap transaction: %v", err)
		}
		if got, err := store.Get(ctx, key); err != nil || string(got) != "after" {
			t.Fatalf("committed value = %q, %v; want after", got, err)
		}
	})

	t.Run("compare and swap transaction rolls back", func(t *testing.T) {
		store := open(t)
		ctx := context.Background()
		key := mustKey(t, "cas", "rollback")
		abort := errors.New("abort compare and swap")

		if err := store.Put(ctx, key, []byte("before")); err != nil {
			t.Fatalf("seed key: %v", err)
		}
		err := store.WithTx(ctx, func(tx storage.Tx) error {
			if err := tx.CompareAndSwap(ctx, key, []byte("before"), []byte("after")); err != nil {
				return err
			}
			return abort
		})
		if !errors.Is(err, abort) {
			t.Fatalf("transaction error = %v, want %v", err, abort)
		}
		if got, err := store.Get(ctx, key); err != nil || string(got) != "before" {
			t.Fatalf("rolled-back value = %q, %v; want before", got, err)
		}
	})

	t.Run("compare and swap cancellation", func(t *testing.T) {
		store := open(t)
		ctx := context.Background()
		key := mustKey(t, "cas", "cancelled")

		if err := store.Put(ctx, key, []byte("before")); err != nil {
			t.Fatalf("seed key: %v", err)
		}

		cancelled, cancel := context.WithCancel(ctx)
		cancel()
		if err := store.CompareAndSwap(cancelled, key, []byte("before"), []byte("after")); err == nil {
			t.Fatal("compare and swap with cancelled context succeeded")
		}
		if got, err := store.Get(ctx, key); err != nil || string(got) != "before" {
			t.Fatalf("value after cancelled compare and swap = %q, %v; want before", got, err)
		}

		txCtx, txCancel := context.WithCancel(ctx)
		err := store.WithTx(txCtx, func(tx storage.Tx) error {
			if err := tx.CompareAndSwap(ctx, key, []byte("before"), []byte("after")); err != nil {
				return err
			}
			txCancel()
			return nil
		})
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled compare and swap transaction = %v, want context.Canceled", err)
		}
		if got, err := store.Get(ctx, key); err != nil || string(got) != "before" {
			t.Fatalf("value after cancelled transaction = %q, %v; want before", got, err)
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

func RunTimerContract(t *testing.T, open func(t *testing.T) storage.Store) {
	t.Helper()

	t.Run("put/get/delete/list", func(t *testing.T) {
		store := open(t)
		ctx := context.Background()
		now := time.Now().Truncate(time.Millisecond)
		if err := store.PutTimer(ctx, "timers/a/1", now.Add(-time.Minute), []byte("one")); err != nil {
			t.Fatalf("put timer: %v", err)
		}
		if err := store.PutTimer(ctx, "timers/a/2", now, []byte("two")); err != nil {
			t.Fatalf("put timer: %v", err)
		}
		if err := store.PutTimer(ctx, "timers/b/1", now, []byte("other")); err != nil {
			t.Fatalf("put timer: %v", err)
		}
		if got, err := store.GetTimer(ctx, "timers/a/1"); err != nil || string(got) != "one" {
			t.Fatalf("get timer = %q, %v; want one", got, err)
		}
		keys, err := store.ListTimers(ctx, "timers/a/")
		if err != nil {
			t.Fatalf("list timers: %v", err)
		}
		if len(keys) != 2 || keys[0] != "timers/a/1" || keys[1] != "timers/a/2" {
			t.Fatalf("list timers = %v", keys)
		}
		if err := store.DeleteTimer(ctx, "timers/a/1"); err != nil {
			t.Fatalf("delete timer: %v", err)
		}
		if _, err := store.GetTimer(ctx, "timers/a/1"); !errors.Is(err, storage.ErrTimerNotFound) {
			t.Fatalf("get deleted timer = %v, want ErrTimerNotFound", err)
		}
	})

	t.Run("due timers ordered and limited", func(t *testing.T) {
		store := open(t)
		ctx := context.Background()
		base := time.Now().Add(-10 * time.Minute).Truncate(time.Millisecond)
		now := base.Add(5 * time.Minute)
		for _, tc := range []struct {
			key  string
			due  time.Time
			data string
		}{
			{"timers/b", base.Add(1 * time.Minute), "b"},
			{"timers/a", base.Add(1 * time.Minute), "a"},
			{"timers/c", base.Add(2 * time.Minute), "c"},
			{"timers/future", base.Add(10 * time.Minute), "future"},
		} {
			if err := store.PutTimer(ctx, tc.key, tc.due, []byte(tc.data)); err != nil {
				t.Fatalf("put timer %s: %v", tc.key, err)
			}
		}
		due, err := store.DueTimers(ctx, now, 10)
		if err != nil {
			t.Fatalf("due timers: %v", err)
		}
		if len(due) != 3 || due[0].Key != "timers/a" || due[1].Key != "timers/b" || due[2].Key != "timers/c" {
			t.Fatalf("due timers = %#v", due)
		}
		if string(due[0].Data) != "a" || string(due[1].Data) != "b" {
			t.Fatalf("due timer data mismatch: %#v", due[:2])
		}
		limited, err := store.DueTimers(ctx, now, 2)
		if err != nil {
			t.Fatalf("limited due timers: %v", err)
		}
		if len(limited) != 2 || limited[0].Key != "timers/a" || limited[1].Key != "timers/b" {
			t.Fatalf("limited due timers = %#v", limited)
		}
	})

	t.Run("claim respects due and live lease", func(t *testing.T) {
		store := open(t)
		ctx := context.Background()
		now := time.Now()
		due := now.Add(-time.Minute)

		claimed, err := store.ClaimTimer(ctx, "missing", "w1", time.Minute)
		if err != nil || claimed {
			t.Fatalf("claim missing = %v, %v; want false,nil", claimed, err)
		}

		if err := store.PutTimer(ctx, "future", now.Add(time.Hour), []byte("x")); err != nil {
			t.Fatalf("put future: %v", err)
		}
		claimed, err = store.ClaimTimer(ctx, "future", "w1", time.Minute)
		if err != nil || claimed {
			t.Fatalf("claim future = %v, %v; want false,nil", claimed, err)
		}

		if err := store.PutTimer(ctx, "due", due, []byte("x")); err != nil {
			t.Fatalf("put due: %v", err)
		}
		claimed, err = store.ClaimTimer(ctx, "due", "w1", time.Hour)
		if err != nil || !claimed {
			t.Fatalf("claim due = %v, %v; want true,nil", claimed, err)
		}
		claimed, err = store.ClaimTimer(ctx, "due", "w2", time.Hour)
		if err != nil || claimed {
			t.Fatalf("claim live lease = %v, %v; want false,nil", claimed, err)
		}
		dueTimers, err := store.DueTimers(ctx, time.Now(), 10)
		if err != nil {
			t.Fatalf("due timers after claim: %v", err)
		}
		for _, tm := range dueTimers {
			if tm.Key == "due" {
				t.Fatalf("live-lease timer returned by DueTimers: %#v", tm)
			}
		}

		if err := store.PutTimer(ctx, "expired", due, []byte("x")); err != nil {
			t.Fatalf("put expired: %v", err)
		}
		claimed, err = store.ClaimTimer(ctx, "expired", "w1", 20*time.Millisecond)
		if err != nil || !claimed {
			t.Fatalf("initial claim expired-case = %v, %v", claimed, err)
		}
		deadline := time.Now().Add(500 * time.Millisecond)
		for time.Now().Before(deadline) {
			claimed, err = store.ClaimTimer(ctx, "expired", "w2", time.Hour)
			if err != nil {
				t.Fatalf("reclaim expired lease: %v", err)
			}
			if claimed {
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
		t.Fatal("reclaim expired lease = false; want true after lease expiry")
	})

	t.Run("renew extends live owned lease", func(t *testing.T) {
		store := open(t)
		ctx := context.Background()
		due := time.Now().Add(-time.Minute)

		if err := store.PutTimer(ctx, "renew", due, []byte("x")); err != nil {
			t.Fatalf("put timer: %v", err)
		}
		claimed, err := store.ClaimTimer(ctx, "renew", "w1", 80*time.Millisecond)
		if err != nil || !claimed {
			t.Fatalf("claim = %v, %v; want true,nil", claimed, err)
		}
		ok, err := store.RenewTimer(ctx, "renew", "w1", time.Hour)
		if err != nil || !ok {
			t.Fatalf("renew = %v, %v; want true,nil", ok, err)
		}
		time.Sleep(120 * time.Millisecond)
		claimed, err = store.ClaimTimer(ctx, "renew", "w2", time.Hour)
		if err != nil || claimed {
			t.Fatalf("claim after renew = %v, %v; want false,nil (lease should still be live)", claimed, err)
		}
		ok, err = store.RenewTimer(ctx, "renew", "w2", time.Hour)
		if err != nil || ok {
			t.Fatalf("renew wrong owner = %v, %v; want false,nil", ok, err)
		}

		ok, err = store.RenewTimer(ctx, "missing", "w1", time.Hour)
		if err != nil || ok {
			t.Fatalf("renew missing = %v, %v; want false,nil", ok, err)
		}

		if err := store.PutTimer(ctx, "expired-renew", due, []byte("x")); err != nil {
			t.Fatalf("put timer: %v", err)
		}
		claimed, err = store.ClaimTimer(ctx, "expired-renew", "w1", 10*time.Millisecond)
		if err != nil || !claimed {
			t.Fatalf("claim = %v, %v; want true,nil", claimed, err)
		}
		time.Sleep(30 * time.Millisecond)
		ok, err = store.RenewTimer(ctx, "expired-renew", "w1", time.Hour)
		if err != nil {
			t.Fatalf("renew expired lease: %v", err)
		}
		if ok {
			t.Fatalf("renew expired lease = true; want false after expiry")
		}
	})

	t.Run("delete owned and put lease fencing", func(t *testing.T) {
		store := open(t)
		ctx := context.Background()
		due := time.Now().Add(-time.Minute)

		ok, err := store.DeleteTimerOwned(ctx, "missing", "w1")
		if err != nil || ok {
			t.Fatalf("delete owned missing = %v, %v; want false,nil", ok, err)
		}

		if err := store.PutTimer(ctx, "owned", due, []byte("x")); err != nil {
			t.Fatalf("put timer: %v", err)
		}
		claimed, err := store.ClaimTimer(ctx, "owned", "w1", time.Hour)
		if err != nil || !claimed {
			t.Fatalf("claim = %v, %v; want true,nil", claimed, err)
		}
		ok, err = store.DeleteTimerOwned(ctx, "owned", "w2")
		if err != nil || ok {
			t.Fatalf("delete by wrong owner = %v, %v; want false,nil", ok, err)
		}
		if _, err := store.GetTimer(ctx, "owned"); err != nil {
			t.Fatalf("timer removed by wrong owner: %v", err)
		}
		if err := store.PutTimer(ctx, "owned", due, []byte("y")); !errors.Is(err, storage.ErrTimerLeased) {
			t.Fatalf("put over live lease = %v, want ErrTimerLeased", err)
		}
		ok, err = store.DeleteTimerOwned(ctx, "owned", "w1")
		if err != nil || !ok {
			t.Fatalf("delete by owner = %v, %v; want true,nil", ok, err)
		}
		if _, err := store.GetTimer(ctx, "owned"); !errors.Is(err, storage.ErrTimerNotFound) {
			t.Fatalf("get after owner delete = %v, want ErrTimerNotFound", err)
		}

		if err := store.PutTimer(ctx, "stale", due, []byte("x")); err != nil {
			t.Fatalf("put timer: %v", err)
		}
		claimed, err = store.ClaimTimer(ctx, "stale", "w1", 20*time.Millisecond)
		if err != nil || !claimed {
			t.Fatalf("claim = %v, %v; want true,nil", claimed, err)
		}
		time.Sleep(30 * time.Millisecond)
		if err := store.PutTimer(ctx, "stale", due.Add(-time.Minute), []byte("new")); err != nil {
			t.Fatalf("put over expired lease: %v", err)
		}
		got, err := store.GetTimer(ctx, "stale")
		if err != nil || string(got) != "new" {
			t.Fatalf("get after expired re-put = %q, %v; want new", got, err)
		}
		// stale owner cleared: another worker can claim and delete-owned
		claimed, err = store.ClaimTimer(ctx, "stale", "w2", time.Hour)
		if err != nil || !claimed {
			t.Fatalf("claim after expired re-put = %v, %v; want true,nil", claimed, err)
		}
		ok, err = store.DeleteTimerOwned(ctx, "stale", "w1")
		if err != nil || ok {
			t.Fatalf("stale owner delete = %v, %v; want false,nil", ok, err)
		}
		ok, err = store.DeleteTimerOwned(ctx, "stale", "w2")
		if err != nil || !ok {
			t.Fatalf("new owner delete = %v, %v; want true,nil", ok, err)
		}
	})

	t.Run("timer tx is atomic with kv and timers", func(t *testing.T) {
		store := open(t)
		ctx := context.Background()
		due := time.Now().Add(-time.Minute)
		abort := errors.New("abort")
		err := store.WithTimer(ctx, func(tx storage.TimerTx) error {
			if err := tx.Put(ctx, "kv/key", []byte("v")); err != nil {
				return err
			}
			if err := tx.PutTimer(ctx, "timers/rollback", due, []byte("t")); err != nil {
				return err
			}
			return abort
		})
		if !errors.Is(err, abort) {
			t.Fatalf("with timer err = %v, want abort", err)
		}
		if _, err := store.Get(ctx, "kv/key"); !errors.Is(err, storage.ErrNotFound) {
			t.Fatalf("kv after rollback = %v, want ErrNotFound", err)
		}
		if _, err := store.GetTimer(ctx, "timers/rollback"); !errors.Is(err, storage.ErrTimerNotFound) {
			t.Fatalf("timer after rollback = %v, want ErrTimerNotFound", err)
		}

		if err := store.WithTimer(ctx, func(tx storage.TimerTx) error {
			if err := tx.Put(ctx, "kv/ok", []byte("v")); err != nil {
				return err
			}
			return tx.PutTimer(ctx, "timers/ok", due, []byte("t"))
		}); err != nil {
			t.Fatalf("with timer commit: %v", err)
		}
		if _, err := store.Get(ctx, "kv/ok"); err != nil {
			t.Fatalf("kv after commit: %v", err)
		}
		if _, err := store.GetTimer(ctx, "timers/ok"); err != nil {
			t.Fatalf("timer after commit: %v", err)
		}
	})

	t.Run("timer tx lease fencing and empty owner", func(t *testing.T) {
		store := open(t)
		ctx := context.Background()
		due := time.Now().Add(-time.Minute)

		if err := store.PutTimer(ctx, "tx-owned", due, []byte("x")); err != nil {
			t.Fatalf("put timer: %v", err)
		}
		claimed, err := store.ClaimTimer(ctx, "tx-owned", "w1", time.Hour)
		if err != nil || !claimed {
			t.Fatalf("claim = %v, %v; want true,nil", claimed, err)
		}
		err = store.WithTimer(ctx, func(tx storage.TimerTx) error {
			if err := tx.PutTimer(ctx, "tx-owned", due, []byte("y")); !errors.Is(err, storage.ErrTimerLeased) {
				return fmt.Errorf("put over live lease = %v, want ErrTimerLeased", err)
			}
			if _, err := tx.ClaimTimer(ctx, "tx-owned", "", time.Hour); !errors.Is(err, storage.ErrTimerOwnerEmpty) {
				return fmt.Errorf("claim empty owner = %v, want ErrTimerOwnerEmpty", err)
			}
			if _, err := tx.RenewTimer(ctx, "tx-owned", "", time.Hour); !errors.Is(err, storage.ErrTimerOwnerEmpty) {
				return fmt.Errorf("renew empty owner = %v, want ErrTimerOwnerEmpty", err)
			}
			if _, err := tx.DeleteTimerOwned(ctx, "tx-owned", ""); !errors.Is(err, storage.ErrTimerOwnerEmpty) {
				return fmt.Errorf("delete owned empty owner = %v, want ErrTimerOwnerEmpty", err)
			}
			if ok, err := tx.RenewTimer(ctx, "tx-owned", "w1", time.Hour); err != nil || !ok {
				return fmt.Errorf("renew correct owner = %v, %v; want true,nil", ok, err)
			}
			if ok, err := tx.DeleteTimerOwned(ctx, "tx-owned", "w1"); err != nil || !ok {
				return fmt.Errorf("delete owned correct owner = %v, %v; want true,nil", ok, err)
			}
			return nil
		})
		if err != nil {
			t.Fatalf("timer tx fencing: %v", err)
		}
	})
}
