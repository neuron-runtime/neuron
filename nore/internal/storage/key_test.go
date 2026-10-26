package storage

import (
	"errors"
	"testing"
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
