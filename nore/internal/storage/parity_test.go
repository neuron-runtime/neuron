package storage_test

import (
	"context"
	"os"
	"testing"

	"github.com/neuron-runtime/neuron/nore/internal/storage"
	"github.com/neuron-runtime/neuron/nore/internal/storage/postgres"
	"github.com/neuron-runtime/neuron/nore/internal/storage/sqlite"
)

func TestCrossBackendParitySQLitePostgres(t *testing.T) {
	// SQLite
	sqliteDir := t.TempDir()
	sqliteStore, err := sqlite.New(context.Background(), storage.Config{DataDir: sqliteDir})
	if err != nil {
		t.Fatalf("sqlite new: %v", err)
	}
	defer sqliteStore.Close()

	// Postgres if available
	pgDSN := os.Getenv("NEURON_TEST_POSTGRES_DSN")
	if pgDSN == "" {
		pgDSN = os.Getenv("NEURON_DATABASE_URL")
	}
	if pgDSN == "" {
		t.Skipf("postgres DSN not set; skipping cross-backend parity")
	}
	pgStore, err := postgres.New(context.Background(), storage.Config{DSN: pgDSN})
	if err != nil {
		t.Fatalf("postgres new: %v", err)
	}
	defer pgStore.Close()

	ctx := context.Background()
	key := mustKeyParity(t, "parity", "k1")
	val := []byte("hello")
	for _, s := range []storage.Store{sqliteStore, pgStore} {
		if err := s.Put(ctx, key, val); err != nil {
			t.Fatalf("put: %v", err)
		}
		got, err := s.Get(ctx, key)
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		if string(got) != string(val) {
			t.Fatalf("mismatch")
		}
		keys, err := s.List(ctx, mustPrefixParity(t, "parity"))
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		if len(keys) != 1 {
			t.Fatalf("list len")
		}
		if err := s.Delete(ctx, key); err != nil {
			t.Fatalf("del: %v", err)
		}
		if _, err := s.Get(ctx, key); err == nil {
			t.Fatalf("expected err after delete")
		}
	}
}

func mustKeyParity(t *testing.T, parts ...string) string {
	t.Helper()
	key, err := storage.Key(parts...)
	if err != nil {
		t.Fatalf("key: %v", err)
	}
	return key
}

func mustPrefixParity(t *testing.T, parts ...string) string {
	t.Helper()
	p, err := storage.Prefix(parts...)
	if err != nil {
		t.Fatalf("prefix: %v", err)
	}
	return p
}
