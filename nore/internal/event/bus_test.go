package event

import (
	"context"
	"testing"

	"github.com/neuron-runtime/neuron/shared/types/core"
)

func TestSubscribeExecutionScopesToExecution(t *testing.T) {
	bus := NewBus()
	ctx := context.Background()

	execA := core.NewID("exec_")
	execB := core.NewID("exec_")

	all, err := bus.Subscribe(All, 8)
	if err != nil {
		t.Fatal(err)
	}
	defer all.Close()
	scoped, err := bus.SubscribeExecution(execA, 8)
	if err != nil {
		t.Fatal(err)
	}
	defer scoped.Close()

	if err := bus.Publish(ctx, New(ExecutionStarted, execA, "", "", ExecutionStartedPayload{})); err != nil {
		t.Fatal(err)
	}
	if err := bus.Publish(ctx, New(ExecutionStarted, execB, "", "", ExecutionStartedPayload{})); err != nil {
		t.Fatal(err)
	}

	allGot := []core.ID{(<-all.Events()).Metadata.ExecutionID, (<-all.Events()).Metadata.ExecutionID}
	if allGot[0] != execA || allGot[1] != execB {
		t.Fatalf("all subscription expected [execA, execB], got %v", allGot)
	}
	if got := <-scoped.Events(); got.Metadata.ExecutionID != execA {
		t.Fatalf("execution subscription expected execA, got %s", got.Metadata.ExecutionID)
	}
}

func TestSubscribeExecutionUnsubscribeClosesChannel(t *testing.T) {
	bus := NewBus()
	exec := core.NewID("exec_")

	scoped, err := bus.SubscribeExecution(exec, 4)
	if err != nil {
		t.Fatal(err)
	}
	if err := scoped.Close(); err != nil {
		t.Fatal(err)
	}
	if _, ok := <-scoped.Events(); ok {
		t.Fatal("expected channel closed after unsubscribe")
	}

	if err := bus.Publish(context.Background(), New(ExecutionStarted, exec, "", "", ExecutionStartedPayload{})); err != nil {
		t.Fatalf("publish after unsubscribe must not fail: %v", err)
	}
}

func TestSubscribeExecutionRejectsEmptyID(t *testing.T) {
	bus := NewBus()
	if _, err := bus.SubscribeExecution("", 4); err == nil {
		t.Fatal("expected error for empty execution id")
	}
}

func TestBusCloseReleasesExecutionSubscriptions(t *testing.T) {
	bus := NewBus()
	exec := core.NewID("exec_")

	scoped, err := bus.SubscribeExecution(exec, 4)
	if err != nil {
		t.Fatal(err)
	}
	if err := bus.Close(); err != nil {
		t.Fatal(err)
	}
	if _, ok := <-scoped.Events(); ok {
		t.Fatal("expected channel closed after bus close")
	}
	if _, err := bus.SubscribeExecution(exec, 4); err != ErrBusClosed {
		t.Fatalf("expected ErrBusClosed, got %v", err)
	}
	if err := bus.Publish(context.Background(), New(ExecutionStarted, exec, "", "", ExecutionStartedPayload{})); err != ErrBusClosed {
		t.Fatalf("expected ErrBusClosed, got %v", err)
	}
}
