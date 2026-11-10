package storage

import (
	"context"
	"errors"
	"testing"
)

func TestMemoryStoreRoundTripCopiesValues(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	defer store.Close()

	value := []byte("original")
	if err := store.Put(ctx, "executions/a", value); err != nil {
		t.Fatalf("put: %v", err)
	}
	value[0] = 'X'

	got, err := store.Get(ctx, "executions/a")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if string(got) != "original" {
		t.Fatalf("stored value changed through the caller's slice: %q", got)
	}
	got[0] = 'Y'

	again, err := store.Get(ctx, "executions/a")
	if err != nil {
		t.Fatalf("get again: %v", err)
	}
	if string(again) != "original" {
		t.Fatalf("read value changed through the returned slice: %q", again)
	}
}

func TestMemoryStoreListIsSortedAndPrefixScoped(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	defer store.Close()

	for _, key := range []string{"events/b", "events/a", "executions/z"} {
		if err := store.Put(ctx, key, []byte("x")); err != nil {
			t.Fatalf("put %s: %v", key, err)
		}
	}

	keys, err := store.List(ctx, "events/")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(keys) != 2 || keys[0] != "events/a" || keys[1] != "events/b" {
		t.Fatalf("list = %v, want [events/a events/b]", keys)
	}
}

func TestMemoryStoreTransactionCommits(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	defer store.Close()

	if err := store.WithTx(ctx, func(tx Tx) error {
		return tx.Put(ctx, "k", []byte("v"))
	}); err != nil {
		t.Fatalf("with tx: %v", err)
	}
	if _, err := store.Get(ctx, "k"); err != nil {
		t.Fatalf("committed value is missing: %v", err)
	}
}

func TestMemoryStoreTransactionRollsBack(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	defer store.Close()

	if err := store.Put(ctx, "existing", []byte("before")); err != nil {
		t.Fatalf("put: %v", err)
	}
	rollback := errors.New("rollback")
	err := store.WithTx(ctx, func(tx Tx) error {
		if err := tx.Put(ctx, "existing", []byte("after")); err != nil {
			return err
		}
		if err := tx.Put(ctx, "new", []byte("value")); err != nil {
			return err
		}
		return rollback
	})
	if !errors.Is(err, rollback) {
		t.Fatalf("with tx error = %v, want rollback", err)
	}

	got, err := store.Get(ctx, "existing")
	if err != nil {
		t.Fatalf("get existing: %v", err)
	}
	if string(got) != "before" {
		t.Fatalf("existing value = %q, want the pre-transaction value", got)
	}
	if _, err := store.Get(ctx, "new"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("get new = %v, want ErrNotFound after rollback", err)
	}
}

func TestMemoryStoreRejectsNestedAndPlainOperationsInsideTx(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	defer store.Close()

	err := store.WithTx(ctx, func(tx Tx) error {
		if err := store.WithTx(ctx, func(Tx) error { return nil }); !errors.Is(err, ErrNestedTransaction) {
			t.Errorf("nested WithTx = %v, want ErrNestedTransaction", err)
		}
		if _, err := store.Get(ctx, "k"); !errors.Is(err, ErrTransactionActive) {
			t.Errorf("plain Get inside tx = %v, want ErrTransactionActive", err)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("outer WithTx: %v", err)
	}
}

func TestMemoryStoreClosedRejectsOperations(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	if err := store.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if _, err := store.Get(ctx, "k"); !errors.Is(err, ErrClosed) {
		t.Fatalf("get after close = %v, want ErrClosed", err)
	}
}
