package execution

import (
	"errors"
	"testing"
	"time"
)

// TestRunTransitionSerializesCallbacksOnOneExecution proves the guard is held
// across the whole composite transition: a second driver cannot observe or
// mutate the execution until the first callback returns.
func TestRunTransitionSerializesCallbacksOnOneExecution(t *testing.T) {
	exec := testExecution(t)
	entered := make(chan struct{}, 2)
	release := make(chan struct{})
	results := make(chan error, 2)

	go func() {
		results <- exec.RunTransition(func() error {
			entered <- struct{}{}
			<-release
			return nil
		})
	}()

	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("first transition never entered the guard")
	}

	go func() {
		results <- exec.RunTransition(func() error {
			entered <- struct{}{}
			return nil
		})
	}()

	select {
	case <-entered:
		t.Fatal("second transition entered while the first still held the guard")
	case <-time.After(50 * time.Millisecond):
	}

	close(release)

	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("second transition never entered after the first released the guard")
	}

	for i := 0; i < 2; i++ {
		if err := <-results; err != nil {
			t.Fatalf("transition %d returned %v", i, err)
		}
	}
}

// TestGuardedFailureRestoresOnlyItsOwnSnapshot enforces the ADR 0004 rule that
// a rollback snapshot is captured inside the guard. A failing transition that
// reloads its own before-state cannot lower a revision committed by the
// preceding guarded transition.
func TestGuardedFailureRestoresOnlyItsOwnSnapshot(t *testing.T) {
	exec := testExecution(t)
	exec.setRevision(7)

	if err := exec.RunTransition(func() error {
		before := exec.Snapshot()
		if before.Revision != 7 {
			t.Fatalf("first guard snapshot revision = %d, want 7", before.Revision)
		}
		exec.setRevision(8)
		return nil
	}); err != nil {
		t.Fatalf("committed guarded transition: %v", err)
	}

	errCommit := errors.New("commit rejected")
	if err := exec.RunTransition(func() error {
		before := exec.Snapshot()
		if before.Revision != 8 {
			t.Fatalf("second guard snapshot revision = %d, want the peer's 8", before.Revision)
		}
		exec.setRevision(9)
		exec.Restore(before)
		return errCommit
	}); !errors.Is(err, errCommit) {
		t.Fatalf("guarded failure error = %v, want %v", err, errCommit)
	}

	if got := exec.Revision(); got != 8 {
		t.Fatalf("revision after guarded failure = %d, want the committed 8", got)
	}
}
