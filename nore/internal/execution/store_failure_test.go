package execution

import (
	"context"
	"errors"
	"testing"
	"time"

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

func (f *faultStore) CompareAndSwap(ctx context.Context, key string, old, new []byte) error {
	current, err := f.Get(ctx, key)
	if err != nil {
		return err
	}
	if string(current) != string(old) {
		return storage.ErrConflict
	}
	return f.Put(ctx, key, new)
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

func (f *faultStore) WithTimer(ctx context.Context, fn func(storage.TimerTx) error) error {
	if f.withTx != nil {
		return f.withTx(ctx, func(storage.Tx) error { return fn(f) })
	}
	return fn(f)
}

func (f *faultStore) PutTimer(context.Context, string, time.Time, []byte) error { return nil }

func (f *faultStore) GetTimer(context.Context, string) ([]byte, error) {
	return nil, storage.ErrTimerNotFound
}

func (f *faultStore) DeleteTimer(context.Context, string) error { return nil }

func (f *faultStore) DeleteTimerOwned(context.Context, string, string) (bool, error) {
	return false, nil
}

func (f *faultStore) ListTimers(context.Context, string) ([]string, error) { return nil, nil }

func (f *faultStore) ClaimTimer(context.Context, string, string, time.Duration) (bool, error) {
	return false, nil
}

func (f *faultStore) RenewTimer(context.Context, string, string, time.Duration) (bool, error) {
	return false, nil
}

func (f *faultStore) DueTimers(context.Context, time.Time, int) ([]storage.Timer, error) {
	return nil, nil
}

func (f *faultStore) Ping(context.Context) error { return nil }
func (f *faultStore) Close() error               { return nil }

// dispatchClaimFaultStore wraps a real store so a test can fail one claim and,
// independently, fail the release that follows a partial claim failure. It is
// armed only after test setup so enqueue writes keep their real behavior.
// Every other operation is delegated, so compare-and-swap and lease fencing are
// exercised against the real backend rather than a fake.
type dispatchClaimFaultStore struct {
	storage.Store
	armed              bool
	failOnAttempt      int
	claimErr           error
	cancelOnClaimFail  func()
	failRelease        bool
	releaseErr         error
	claimAttempts      int
	releasePutCalls    int
	successfulReleases int
}

func (s *dispatchClaimFaultStore) WithTx(ctx context.Context, fn func(storage.Tx) error) error {
	return s.Store.WithTx(ctx, func(tx storage.Tx) error {
		return fn(&dispatchClaimFaultTx{Tx: tx, store: s})
	})
}

type dispatchClaimFaultTx struct {
	storage.Tx
	store *dispatchClaimFaultStore
}

func (tx *dispatchClaimFaultTx) CompareAndSwap(ctx context.Context, key string, old, new []byte) error {
	s := tx.store
	if s.armed && s.failOnAttempt > 0 {
		s.claimAttempts++
		if s.claimAttempts == s.failOnAttempt {
			if s.cancelOnClaimFail != nil {
				s.cancelOnClaimFail()
			}
			return s.claimErr
		}
	}
	return tx.Tx.CompareAndSwap(ctx, key, old, new)
}

func (tx *dispatchClaimFaultTx) Put(ctx context.Context, key string, data []byte) error {
	s := tx.store
	if !s.armed {
		return tx.Tx.Put(ctx, key, data)
	}
	s.releasePutCalls++
	if s.failRelease {
		return s.releaseErr
	}
	if err := tx.Tx.Put(ctx, key, data); err != nil {
		return err
	}
	s.successfulReleases++
	return nil
}

func testExecution(t *testing.T) *Execution {
	t.Helper()
	exec, err := NewExecution(&types.ExecutionBlueprint{Nodes: map[shared.ID]types.ExecutionNode{}}, shared.NewID("corr_"), "inst_test", "", "", "", "")
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
