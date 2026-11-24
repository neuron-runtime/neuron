package storage

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestKeyJoinsSegments(t *testing.T) {
	key, err := Key("executions", "exec_1")
	if err != nil {
		t.Fatalf("Key: %v", err)
	}
	if key != "executions/exec_1" {
		t.Fatalf("Key = %q, want executions/exec_1", key)
	}

	prefix, err := Prefix("executions")
	if err != nil {
		t.Fatalf("Prefix: %v", err)
	}
	if prefix != "executions/" {
		t.Fatalf("Prefix = %q, want executions/", prefix)
	}
}

func TestKeyPreservesEmptySegments(t *testing.T) {
	key, err := Key("assemblies", "sys", "1.0.0", "hash", "")
	if err != nil {
		t.Fatalf("Key with empty environment: %v", err)
	}
	if key != "assemblies/sys/1.0.0/hash/" {
		t.Fatalf("Key = %q, want assemblies/sys/1.0.0/hash/", key)
	}
}

func TestKeyRejectsAmbiguousSegments(t *testing.T) {
	for _, part := range []string{"a/b", "a\x00b"} {
		if _, err := Key("prefix", part); !errors.Is(err, ErrInvalidKey) {
			t.Errorf("Key(%q) = %v, want ErrInvalidKey", part, err)
		}
	}
	if _, err := Key(); !errors.Is(err, ErrInvalidKey) {
		t.Errorf("Key() = %v, want ErrInvalidKey", err)
	}
}

func TestExecutionDeadlineKeysAreOrderedAndParseable(t *testing.T) {
	early := time.Date(2026, 11, 23, 10, 0, 0, 123456789, time.UTC)
	later := early.Add(time.Second)
	earlyKey, err := ExecutionDeadlineKey("inst_a", early, "exec_1")
	if err != nil {
		t.Fatalf("ExecutionDeadlineKey(early): %v", err)
	}
	laterKey, err := ExecutionDeadlineKey("inst_a", later, "exec_2")
	if err != nil {
		t.Fatalf("ExecutionDeadlineKey(later): %v", err)
	}
	if earlyKey >= laterKey {
		t.Fatalf("deadline keys are not time ordered: %q >= %q", earlyKey, laterKey)
	}
	if !strings.HasPrefix(earlyKey, "execution_deadlines/inst_a/") {
		t.Fatalf("deadline key = %q, want instance prefix", earlyKey)
	}
	instanceID, gotDue, executionID, err := ParseExecutionDeadlineKey(earlyKey)
	if err != nil {
		t.Fatalf("ParseExecutionDeadlineKey: %v", err)
	}
	if instanceID != "inst_a" || executionID != "exec_1" || !gotDue.Equal(early) {
		t.Fatalf("parsed = (%q, %v, %q), want (inst_a, %v, exec_1)", instanceID, gotDue, executionID, early)
	}
	if _, _, _, err := ParseExecutionDeadlineKey("executions/exec_1"); err == nil {
		t.Fatal("ParseExecutionDeadlineKey accepted an execution record key")
	}
	if _, err := ExecutionDeadlineKey("bad/instance", early, "exec_1"); !errors.Is(err, ErrInvalidKey) {
		t.Fatalf("ExecutionDeadlineKey with slash = %v, want ErrInvalidKey", err)
	}
}

func TestWaitDueKeysAreInstanceScopedAndLookupable(t *testing.T) {
	key, err := WaitDueKey("inst_a", "timers/exec_1/cap_1/attempt_1")
	if err != nil {
		t.Fatalf("WaitDueKey: %v", err)
	}
	if !strings.HasPrefix(key, "wait_due/inst_a/") {
		t.Fatalf("wait due key = %q, want instance prefix", key)
	}
	instanceID, timerKey, err := ParseWaitDueKey(key)
	if err != nil {
		t.Fatalf("ParseWaitDueKey: %v", err)
	}
	if instanceID != "inst_a" || timerKey != "timers/exec_1/cap_1/attempt_1" {
		t.Fatalf("parsed = (%q, %q)", instanceID, timerKey)
	}
	lookup, err := WaitDueLookupKey(timerKey)
	if err != nil {
		t.Fatalf("WaitDueLookupKey: %v", err)
	}
	if !strings.HasPrefix(lookup, "wait_due_lookup/") {
		t.Fatalf("lookup key = %q, want wait_due_lookup prefix", lookup)
	}
}
