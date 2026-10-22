package filesystem

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/neuron-runtime/neuron/nore/internal/contracts"
	"github.com/neuron-runtime/neuron/shared/types/builtins"
)

// project builds a project root with a few files in it and returns the
// runtime confined to it.
func project(t *testing.T) (string, Filesystem) {
	t.Helper()

	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "data.json"), []byte(`{"answer":42}`), 0o600); err != nil {
		t.Fatalf("write the data file: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "notes.txt"), []byte("plain notes"), 0o600); err != nil {
		t.Fatalf("write the notes file: %v", err)
	}
	if err := os.Mkdir(filepath.Join(root, "nested"), 0o755); err != nil {
		t.Fatalf("create the nested directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "nested", "inner.json"), []byte(`{"inner":true}`), 0o600); err != nil {
		t.Fatalf("write the inner file: %v", err)
	}

	return root, Filesystem{Policy: builtins.Policy{ProjectRoot: root}}
}

// execute performs an operation against the runtime.
func execute(t *testing.T, runtime Filesystem, params map[string]any) (map[string]any, error) {
	t.Helper()
	return runtime.Execute(context.Background(), contracts.ExecutionContext{Params: params})
}

func TestFilesystemReadsAFile(t *testing.T) {
	_, runtime := project(t)

	result, err := execute(t, runtime, map[string]any{"operation": "read", "path": "data.json"})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	content, ok := result["content"].(map[string]any)
	if !ok {
		t.Fatalf("content = %#v, want a parsed object", result["content"])
	}
	if content["answer"] != float64(42) {
		t.Errorf("answer = %v, want 42", content["answer"])
	}
	if result["size"] != float64(13) {
		t.Errorf("size = %v, want 13", result["size"])
	}
}

// A file that is not JSON is refused in the JSON format rather than handed
// over as a string the caller cannot bind.
func TestFilesystemRefusesAFileThatIsNotJSON(t *testing.T) {
	_, runtime := project(t)

	_, err := execute(t, runtime, map[string]any{"operation": "read", "path": "notes.txt"})
	if err == nil {
		t.Fatal("Execute accepted a file that is not JSON")
	}
	if !strings.Contains(err.Error(), "not JSON") {
		t.Errorf("error = %v, want it to state that the file is not JSON", err)
	}
}

// The text format is how a caller asks for a file that is not JSON.
func TestFilesystemReadsAFileAsText(t *testing.T) {
	_, runtime := project(t)

	result, err := execute(t, runtime, map[string]any{
		"operation": "read",
		"path":      "notes.txt",
		"format":    "text",
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result["content"] != "plain notes" {
		t.Errorf("content = %#v, want the raw text", result["content"])
	}
}

// A path outside the permitted roots is refused.
func TestFilesystemRefusesAPathOutsideThePermittedRoots(t *testing.T) {
	_, runtime := project(t)

	_, err := execute(t, runtime, map[string]any{"operation": "read", "path": t.TempDir()})
	if err == nil {
		t.Fatal("Execute read a path outside the permitted roots")
	}
	if !strings.Contains(err.Error(), "not reachable") {
		t.Errorf("error = %v, want it to state that the path is not reachable", err)
	}
}

// A symbolic link inside the permitted roots does not grant reach beyond
// them.
func TestFilesystemRefusesALinkThatLeavesThePermittedRoots(t *testing.T) {
	root, runtime := project(t)

	outside := t.TempDir()
	secret := filepath.Join(outside, "secret.txt")
	if err := os.WriteFile(secret, []byte("secret"), 0o600); err != nil {
		t.Fatalf("write the secret: %v", err)
	}

	link := filepath.Join(root, "link.txt")
	if err := os.Symlink(secret, link); err != nil {
		t.Skipf("the platform does not support symbolic links: %v", err)
	}

	_, err := execute(t, runtime, map[string]any{"operation": "read", "path": "link.txt"})
	if err == nil {
		t.Fatal("Execute read through a link that leaves the permitted roots")
	}
}

func TestFilesystemListsADirectory(t *testing.T) {
	_, runtime := project(t)

	result, err := execute(t, runtime, map[string]any{"operation": "list", "path": "."})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	entries, ok := result["entries"].([]any)
	if !ok {
		t.Fatalf("entries = %#v, want a list", result["entries"])
	}

	names := map[string]string{}
	for _, entry := range entries {
		item, _ := entry.(map[string]any)
		name, _ := item["name"].(string)
		kind, _ := item["type"].(string)
		names[name] = kind
	}

	if names["data.json"] != "file" {
		t.Errorf("data.json = %q, want a file", names["data.json"])
	}
	if names["nested"] != "directory" {
		t.Errorf("nested = %q, want a directory", names["nested"])
	}
}

// A listing that does not descend stops at the directory it was given.
func TestFilesystemListsADirectoryWithoutDescending(t *testing.T) {
	_, runtime := project(t)

	result, err := execute(t, runtime, map[string]any{"operation": "list", "path": "."})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	for _, entry := range result["entries"].([]any) {
		item, _ := entry.(map[string]any)
		if name, _ := item["name"].(string); name == "inner.json" {
			t.Error("the listing descended into a subdirectory")
		}
	}
}

// A recursive listing descends.
func TestFilesystemListsADirectoryRecursively(t *testing.T) {
	_, runtime := project(t)

	result, err := execute(t, runtime, map[string]any{
		"operation": "list",
		"path":      ".",
		"recursive": true,
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	found := false
	for _, entry := range result["entries"].([]any) {
		item, _ := entry.(map[string]any)
		if name, _ := item["name"].(string); name == "inner.json" {
			found = true
		}
	}
	if !found {
		t.Error("the recursive listing did not reach the nested file")
	}
}

// A listing that reaches the limit reports the truncation rather than hiding
// it.
func TestFilesystemReportsATruncatedListing(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"a.txt", "b.txt", "c.txt"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("x"), 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	runtime := Filesystem{Policy: builtins.Policy{ProjectRoot: root, MaxDirectoryEntries: 2}}

	result, err := execute(t, runtime, map[string]any{"operation": "list", "path": "."})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(result["entries"].([]any)) != 2 {
		t.Errorf("entries = %d, want 2", len(result["entries"].([]any)))
	}
	if result["entries_truncated"] != true {
		t.Errorf("entries_truncated = %v, want true", result["entries_truncated"])
	}
}

// A file that is larger than the limit is refused rather than shortened.
func TestFilesystemRefusesAFileAboveTheLimit(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "big.json"), []byte(`{"a":"0123456789"}`), 0o600); err != nil {
		t.Fatalf("write the file: %v", err)
	}

	runtime := Filesystem{Policy: builtins.Policy{ProjectRoot: root, MaxFileReadBytes: 4}}

	_, err := execute(t, runtime, map[string]any{"operation": "read", "path": "big.json"})
	if err == nil {
		t.Fatal("Execute accepted a file above the limit")
	}
	if !strings.Contains(err.Error(), "larger than the limit") {
		t.Errorf("error = %v, want it to state the limit", err)
	}
}

func TestFilesystemReportsWhatAPathIs(t *testing.T) {
	_, runtime := project(t)

	result, err := execute(t, runtime, map[string]any{"operation": "stat", "path": "data.json"})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result["exists"] != true {
		t.Errorf("exists = %v, want true", result["exists"])
	}
	if result["type"] != "file" {
		t.Errorf("type = %v, want file", result["type"])
	}
}

// A path that is not there is an answer, not a failure.
func TestFilesystemReportsAPathThatIsNotThere(t *testing.T) {
	_, runtime := project(t)

	result, err := execute(t, runtime, map[string]any{"operation": "stat", "path": "missing.json"})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result["exists"] != false {
		t.Errorf("exists = %v, want false", result["exists"])
	}
}

func TestFilesystemRequiresAnOperationAndAPath(t *testing.T) {
	_, runtime := project(t)

	if _, err := execute(t, runtime, map[string]any{"path": "data.json"}); err == nil {
		t.Error("Execute succeeded without an operation")
	}
	if _, err := execute(t, runtime, map[string]any{"operation": "read"}); err == nil {
		t.Error("Execute succeeded without a path")
	}
}

// An operation that does not exist is refused, and the error names the ones
// that do.
func TestFilesystemRefusesAnUnknownOperation(t *testing.T) {
	_, runtime := project(t)

	_, err := execute(t, runtime, map[string]any{"operation": "delete", "path": "data.json"})
	if err == nil {
		t.Fatal("Execute accepted an unknown operation")
	}
	if !strings.Contains(err.Error(), "supported operations are read, list, stat") {
		t.Errorf("error = %v, want it to name the supported operations", err)
	}
}

// Reading a directory with the read operation is refused: the caller asked
// for the wrong thing.
func TestFilesystemRefusesToReadADirectory(t *testing.T) {
	_, runtime := project(t)

	_, err := execute(t, runtime, map[string]any{"operation": "read", "path": "nested"})
	if err == nil {
		t.Fatal("Execute read a directory")
	}
	if !strings.Contains(err.Error(), "list operation") {
		t.Errorf("error = %v, want it to point at the list operation", err)
	}
}

// An extra root extends the reach.
func TestFilesystemReadsFromAnExtraRoot(t *testing.T) {
	root := t.TempDir()
	extra := t.TempDir()
	if err := os.WriteFile(filepath.Join(extra, "data.json"), []byte(`{"ok":true}`), 0o600); err != nil {
		t.Fatalf("write the file: %v", err)
	}

	runtime := Filesystem{Policy: builtins.Policy{ProjectRoot: root, FilesystemRoots: []string{extra}}}

	result, err := execute(t, runtime, map[string]any{"operation": "read", "path": filepath.Join(extra, "data.json")})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	content, _ := result["content"].(map[string]any)
	if content["ok"] != true {
		t.Errorf("content = %#v, want the file from the extra root", result["content"])
	}
}
