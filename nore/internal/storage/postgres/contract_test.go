package postgres

import (
	"context"
	"os"
	"testing"

	"github.com/neuron-runtime/neuron/nore/internal/storage"
	"github.com/neuron-runtime/neuron/nore/internal/storage/storagetest"
)

func TestContract(t *testing.T) {
	dsn := os.Getenv("NEURON_TEST_POSTGRES_DSN")
	if dsn == "" {
		dsn = os.Getenv("NEURON_DATABASE_URL")
	}
	if dsn == "" {
		t.Skipf("postgres DSN not set (NEURON_TEST_POSTGRES_DSN or NEURON_DATABASE_URL); skipping contract tests")
	}
	storagetest.Run(t, func(t *testing.T) storage.Store {
		t.Helper()
		ctx := context.Background()
		store, err := New(ctx, storage.Config{DSN: dsn})
		if err != nil {
			t.Fatalf("open postgres: %v", err)
		}
		t.Cleanup(func() {
			_ = store.Close()
		})
		return store
	})
}
