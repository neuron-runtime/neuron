package sqlite_test

import (
	"context"
	"testing"

	"github.com/neuron-runtime/neuron/nore/internal/storage"
	"github.com/neuron-runtime/neuron/nore/internal/storage/sqlite"
	"github.com/neuron-runtime/neuron/nore/internal/storage/storagetest"
)

func TestSQLiteStorageContract(t *testing.T) {
	open := func(t *testing.T) storage.Store {
		t.Helper()
		store, err := sqlite.New(context.Background(), storage.Config{DataDir: t.TempDir()})
		if err != nil {
			t.Fatalf("open sqlite: %v", err)
		}
		t.Cleanup(func() { _ = store.Close() })
		return store
	}
	storagetest.Run(t, open)
	storagetest.RunTimerContract(t, open)
}
