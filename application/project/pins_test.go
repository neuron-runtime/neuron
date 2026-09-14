package project

import (
	"reflect"
	"testing"
)

func TestExecutorsFileRoundTrip(t *testing.T) {
	root := t.TempDir()

	var file ExecutorsFile
	file.Upsert(ExecutorPin{Type: "github:read", Version: "1.0.0", Registry: "github"})
	file.Upsert(ExecutorPin{Type: "local:echo", Version: "0.2.0", Registry: "local"})

	if err := SaveExecutorsFile(root, file); err != nil {
		t.Fatalf("SaveExecutorsFile: %v", err)
	}

	got, err := LoadExecutorsFile(root)
	if err != nil {
		t.Fatalf("LoadExecutorsFile: %v", err)
	}
	if !reflect.DeepEqual(got, file) {
		t.Errorf("round-trip mismatch:\n got %+v\nwant %+v", got, file)
	}
}

func TestExecutorsFileUpsertReplacesByType(t *testing.T) {
	root := t.TempDir()

	var file ExecutorsFile
	file.Upsert(ExecutorPin{Type: "github:read", Version: "1.0.0"})
	file.Upsert(ExecutorPin{Type: "github:read", Version: "1.5.0", Registry: "github"})
	file.Upsert(ExecutorPin{Type: "other:tool", Version: "2.0.0"})

	if len(file.Pins) != 2 {
		t.Fatalf("pins = %d, want 2 after replacing a type", len(file.Pins))
	}
	if file.Pins[0].Version != "1.5.0" {
		t.Errorf("pins[0].Version = %q, want 1.5.0", file.Pins[0].Version)
	}
	if file.Pins[0].Registry != "github" {
		t.Errorf("pins[0].Registry = %q, want github", file.Pins[0].Registry)
	}

	if err := SaveExecutorsFile(root, file); err != nil {
		t.Fatalf("SaveExecutorsFile: %v", err)
	}
	got, err := LoadExecutorsFile(root)
	if err != nil {
		t.Fatalf("LoadExecutorsFile: %v", err)
	}
	if len(got.Pins) != 2 {
		t.Fatalf("loaded pins = %d, want 2", len(got.Pins))
	}
}

func TestExecutorsFileMissingIsEmpty(t *testing.T) {
	file, err := LoadExecutorsFile(t.TempDir())
	if err != nil {
		t.Fatalf("LoadExecutorsFile on missing record should be empty: %v", err)
	}
	if len(file.Pins) != 0 {
		t.Fatalf("pins = %d, want 0 for a missing record", len(file.Pins))
	}
}
