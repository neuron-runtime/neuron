package execution

import (
	"context"
	"errors"
	"testing"

	"github.com/neuron-runtime/neuron/nore/internal/storage"
	"github.com/neuron-runtime/neuron/nore/internal/types"
	shared "github.com/neuron-runtime/neuron/shared/types/core"
)

var errInjected = errors.New("injected storage failure")

// faultStore lets a test fail one storage operation while leaving the rest
// behaving like an empty backend. It is used to prove that runtime code reports
// failures instead of converting them into absence, partial data, or success.
type faultStore struct {
	get    func(string) ([]byte, error)
	put    func(string, []byte) error
	del    func(string) error
	list   func(string) ([]string, error)
	withTx func(context.Context, func(storage.Tx) error) error
}

func (f *faultStore) Get(_ context.Context, key string) ([]byte, error) {
	if f.get != nil {
		return f.get(key)
	}
	return nil, storage.ErrNotFound
}

func (f *faultStore) Put(_ context.Context, key string, data []byte) error {
	if f.put != nil {
		return f.put(key, data)
	}
	return nil
}

func (f *faultStore) Delete(_ context.Context, key string) error {
	if f.del != nil {
		return f.del(key)
	}
	return nil
}

func (f *faultStore) List(_ context.Context, prefix string) ([]string, error) {
	if f.list != nil {
		return f.list(prefix)
	}
	return nil, nil
}

func (f *faultStore) WithTx(ctx context.Context, fn func(storage.Tx) error) error {
	if f.withTx != nil {
		return f.withTx(ctx, fn)
	}
	return fn(f)
}

func (f *faultStore) Ping(context.Context) error { return nil }
func (f *faultStore) Close() error               { return nil }

func testExecution(t *testing.T) *Execution {
	t.Helper()
	exec, err := NewExecution(&types.ExecutionBlueprint{Nodes: map[shared.ID]types.ExecutionNode{}}, shared.NewID("corr_"), "inst_test")
	if err != nil {
		t.Fatalf("new execution: %v", err)
	}
	return exec
}

func TestExecutionStoreSurfacesReadFailure(t *testing.T) {
	s := NewExecutionStore(&faultStore{get: func(string) ([]byte, error) { return nil, errInjected }})
	_, err := s.Get(context.Background(), "exec_missing")
	if !errors.Is(err, errInjected) {
		t.Fatalf("Get error = %v, want injected failure", err)
	}
}

func TestExecutionStoreReportsDecodeFailureAsError(t *testing.T) {
	s := NewExecutionStore(&faultStore{get: func(string) ([]byte, error) { return []byte("{not json"), nil }})
	_, err := s.Get(context.Background(), "exec_bad")
	if err == nil || errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("Get malformed execution = %v, want a decode error", err)
	}
}

func TestExecutionStoreAddDoesNotCacheWhenTransactionFails(t *testing.T) {
	store := &faultStore{withTx: func(context.Context, func(storage.Tx) error) error { return errInjected }}
	s := NewExecutionStore(store)
	exec := testExecution(t)

	if err := s.Add(context.Background(), exec); !errors.Is(err, errInjected) {
		t.Fatalf("Add error = %v, want injected failure", err)
	}
	if _, err := s.mem.Get(context.Background(), exec.ID); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("failed Add left execution in memory: %v", err)
	}
}

func TestExecutionStoreAddDoesNotCacheWhenPutFails(t *testing.T) {
	store := &faultStore{put: func(string, []byte) error { return errInjected }}
	s := NewExecutionStore(store)
	exec := testExecution(t)

	if err := s.Add(context.Background(), exec); !errors.Is(err, errInjected) {
		t.Fatalf("Add error = %v, want injected failure", err)
	}
	if _, err := s.mem.Get(context.Background(), exec.ID); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("failed Add left execution in memory: %v", err)
	}
}

func TestExecutionStoreListSurfacesStorageFailure(t *testing.T) {
	store := &faultStore{list: func(string) ([]string, error) { return nil, errInjected }}
	s := NewExecutionStore(store)
	exec := testExecution(t)
	if err := s.mem.Save(context.Background(), exec); err != nil {
		t.Fatalf("seed memory: %v", err)
	}

	if _, err := s.List(context.Background()); !errors.Is(err, errInjected) {
		t.Fatalf("List error = %v, want injected failure", err)
	}
	if _, err := s.ListByInstance(context.Background(), exec.InstanceID); !errors.Is(err, errInjected) {
		t.Fatalf("ListByInstance error = %v, want injected failure", err)
	}
}

func TestExecutionStoreDeleteKeepsMemoryWhenBackendFails(t *testing.T) {
	store := &faultStore{del: func(string) error { return errInjected }}
	s := NewExecutionStore(store)
	exec := testExecution(t)
	if err := s.mem.Save(context.Background(), exec); err != nil {
		t.Fatalf("seed memory: %v", err)
	}

	if err := s.Delete(context.Background(), exec.ID); !errors.Is(err, errInjected) {
		t.Fatalf("Delete error = %v, want injected failure", err)
	}
	if _, err := s.mem.Get(context.Background(), exec.ID); err != nil {
		t.Fatalf("failed Delete removed execution from memory: %v", err)
	}
}
