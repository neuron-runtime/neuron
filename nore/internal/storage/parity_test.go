package storage_test

import (
	"context"
	"os"
	"testing"

	"github.com/neuron-runtime/neuron/nore/internal/execution"
	"github.com/neuron-runtime/neuron/nore/internal/storage"
	"github.com/neuron-runtime/neuron/nore/internal/storage/postgres"
	"github.com/neuron-runtime/neuron/nore/internal/storage/sqlite"
	types "github.com/neuron-runtime/neuron/nore/internal/types"
	shared "github.com/neuron-runtime/neuron/shared/types/core"
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

func TestCrossBackendExecutionParity(t *testing.T) {
	if os.Getenv("NEURON_TEST_POSTGRES_DSN") == "" && os.Getenv("NEURON_DATABASE_URL") == "" {
		t.Skipf("postgres DSN not set; skipping cross-backend execution parity")
	}

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
	pgStore, err := postgres.New(context.Background(), storage.Config{DSN: pgDSN})
	if err != nil {
		t.Fatalf("postgres new: %v", err)
	}
	defer pgStore.Close()

	ctx := context.Background()

	blueprint := &types.ExecutionBlueprint{
		Nodes: map[shared.ID]types.ExecutionNode{
			shared.NewID("cap_1"): {Capability: shared.Capability{Type: "neuron:core:set"}},
		},
	}

	sqliteExecStore := execution.NewExecutionStore(sqliteStore)
	pgExecStore := execution.NewExecutionStore(pgStore)

	execID := shared.NewID("exec_test")
	exec, err := execution.NewExecution(blueprint, shared.NewID("corr_"), shared.NewID("inst_test"))
	if err != nil {
		t.Fatalf("new execution: %v", err)
	}

	// Add execution to both stores
	if err := sqliteExecStore.Add(ctx, exec); err != nil {
		t.Fatalf("sqlite add: %v", err)
	}
	if err := pgExecStore.Add(ctx, exec); err != nil {
		t.Fatalf("postgres add: %v", err)
	}

	// Save execution to both stores (increment revision)
	if err := sqliteExecStore.Save(ctx, exec); err != nil {
		t.Fatalf("sqlite save: %v", err)
	}
	if err := pgExecStore.Save(ctx, exec); err != nil {
		t.Fatalf("postgres save: %v", err)
	}

	// Get execution from both stores
	gettedExec, err := sqliteExecStore.Get(ctx, execID)
	if err != nil {
		t.Fatalf("sqlite get: %v", err)
	}
	gotExec, err := pgExecStore.Get(ctx, execID)
	if err != nil {
		t.Fatalf("postgres get: %v", err)
	}

	// Assert identical observable results using accessor methods
	if gotExec.Status() != gettedExec.Status() {
		t.Fatalf("status mismatch: sqlite=%v postgres=%v", gettedExec.Status(), gotExec.Status())
	}
	if gotExec.ID != gettedExec.ID {
		t.Fatalf("id mismatch: sqlite=%v postgres=%v", gettedExec.ID, gotExec.ID)
	}
	// Compare results - use StringKeyedResults for string-keyed comparison
	gettedResults := gettedExec.StringKeyedResults()
	gotResults := gotExec.StringKeyedResults()
	if len(gettedResults) != len(gotResults) {
		t.Fatalf("result count mismatch: sqlite=%d postgres=%d", len(gettedResults), len(gotResults))
	}
	for k := range gettedResults {
		v, inGot := gotResults[k]
		if !inGot {
			t.Fatalf("result key %q missing from postgres", k)
		}
		vStr, vOK := v[""].(string)
		gvStr, gvOK := v[""].(string)
		if !vOK || !gvOK {
			t.Fatalf("result value for key %q is not a string: sqlite=%v postgres=%v", k, v[""], v[""])
		}
		if vStr != gvStr {
			t.Fatalf("result value mismatch for key %q: sqlite=%q postgres=%q", k, vStr, gvStr)
		}
	}
}
