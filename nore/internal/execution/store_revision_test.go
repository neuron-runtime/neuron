package execution

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/neuron-runtime/neuron/nore/internal/storage"
	"github.com/neuron-runtime/neuron/nore/internal/types"
	shared "github.com/neuron-runtime/neuron/shared/types/core"
)

func TestSaveRejectsStaleWriter(t *testing.T) {
	store := openTestStore(t)
	s := NewExecutionStore(store)
	ctx := context.Background()
	exec, err := NewExecution(&types.ExecutionBlueprint{Nodes: map[shared.ID]types.ExecutionNode{}}, shared.NewID("corr_"), "inst_test")
	if err != nil {
		t.Fatalf("new execution: %v", err)
	}
	if err := s.Add(ctx, exec); err != nil {
		t.Fatalf("add execution: %v", err)
	}

	first, err := UnmarshalExecution(mustMarshal(t, exec))
	if err != nil {
		t.Fatalf("copy execution: %v", err)
	}
	second, err := UnmarshalExecution(mustMarshal(t, exec))
	if err != nil {
		t.Fatalf("copy execution: %v", err)
	}

	start := make(chan struct{})
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for _, candidate := range []*Execution{first, second} {
		candidate := candidate
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			errs <- s.Save(ctx, candidate)
		}()
	}
	close(start)
	wg.Wait()
	close(errs)

	var successes, conflicts int
	for err := range errs {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, storage.ErrConflict):
			conflicts++
		default:
			t.Fatalf("save error = %v, want success or ErrConflict", err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("concurrent saves: %d successes, %d conflicts; want 1 and 1", successes, conflicts)
	}
}

func TestSaveUsesCallerCancellation(t *testing.T) {
	store := openTestStore(t)
	s := NewExecutionStore(store)
	ctx := context.Background()
	exec, err := NewExecution(&types.ExecutionBlueprint{Nodes: map[shared.ID]types.ExecutionNode{}}, shared.NewID("corr_"), "inst_test")
	if err != nil {
		t.Fatalf("new execution: %v", err)
	}
	if err := s.Add(ctx, exec); err != nil {
		t.Fatalf("add execution: %v", err)
	}

	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if err := s.Save(cancelled, exec); err == nil {
		t.Fatal("Save with cancelled context succeeded")
	}

	durable, err := store.Get(ctx, mustExecutionKey(t, exec.ID))
	if err != nil {
		t.Fatalf("read durable execution: %v", err)
	}
	loaded, err := UnmarshalExecution(durable)
	if err != nil {
		t.Fatalf("decode durable execution: %v", err)
	}
	if loaded.Revision() != 1 {
		t.Fatalf("durable revision after cancelled Save = %d, want 1", loaded.Revision())
	}
}

func mustMarshal(t *testing.T, exec *Execution) []byte {
	t.Helper()
	data, err := MarshalExecution(exec)
	if err != nil {
		t.Fatalf("marshal execution: %v", err)
	}
	return data
}

func mustExecutionKey(t *testing.T, id shared.ID) string {
	t.Helper()
	key, err := storage.Key("executions", string(id))
	if err != nil {
		t.Fatalf("execution key: %v", err)
	}
	return key
}
