package sqlite

import (
	"context"
	"errors"
	"testing"

	"github.com/neuron-runtime/neuron/nore/internal/storage"
)

// TestCommitFailureIsReported proves a failed COMMIT is not converted into
// success. The callback rolls the transaction back underneath WithTx, so the
// subsequent Commit fails after the transaction has already written.
func TestCommitFailureIsReported(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	store, err := New(ctx, storage.Config{DataDir: dir})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	key, err := storage.Key("failure", "commit")
	if err != nil {
		t.Fatalf("key: %v", err)
	}

	err = store.WithTx(ctx, func(tx storage.Tx) error {
		if err := tx.Put(ctx, key, []byte("value")); err != nil {
			return err
		}
		ops, ok := tx.(txOps)
		if !ok {
			return errors.New("unexpected transaction type")
		}
		if err := ops.tx.Rollback(); err != nil {
			return err
		}
		return nil
	})
	if err == nil {
		t.Fatal("commit failure was reported as success")
	}

	reopened, err := New(ctx, storage.Config{DataDir: dir})
	if err != nil {
		t.Fatalf("reopen sqlite: %v", err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	if _, err := reopened.Get(ctx, key); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("key from failed commit = %v, want ErrNotFound", err)
	}
}
