package projectruntime

import (
	"encoding/json"
	"os"
	"testing"
)

func TestWriteReadRoundTrip(t *testing.T) {
	root := t.TempDir()
	want := New("127.0.0.1", "41234", root, os.Getpid(), "secret-token")

	if err := Write(root, want); err != nil {
		t.Fatalf("write: %v", err)
	}

	got, ok, err := Read(root)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !ok {
		t.Fatal("record not found after write")
	}
	if got != want {
		t.Errorf("round trip = %+v, want %+v", got, want)
	}
}

func TestReadMissingFileIsNotAnError(t *testing.T) {
	_, ok, err := Read(t.TempDir())
	if err != nil {
		t.Fatalf("read missing: %v", err)
	}
	if ok {
		t.Error("missing record reported as present")
	}
}

// The record carries the API token, so its permissions are part of the
// contract, not an implementation detail. A world-readable token lets any
// local user authenticate to the loopback TCP endpoint.
func TestWriteIsOwnerOnly(t *testing.T) {
	root := t.TempDir()
	if err := Write(root, New("127.0.0.1", "1", root, os.Getpid(), "token")); err != nil {
		t.Fatalf("write: %v", err)
	}

	info, err := os.Stat(Path(root))
	if err != nil {
		t.Fatalf("stat record: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("record permissions = %o, want 600", perm)
	}

	dirInfo, err := os.Stat(Dir(root))
	if err != nil {
		t.Fatalf("stat dir: %v", err)
	}
	if perm := dirInfo.Mode().Perm(); perm != 0o700 {
		t.Errorf("state directory permissions = %o, want 700", perm)
	}
}

// An interrupted write must never leave the temporary file behind.
func TestWriteLeavesNoTempFile(t *testing.T) {
	root := t.TempDir()
	if err := Write(root, New("127.0.0.1", "1", root, os.Getpid(), "token")); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := os.Stat(Path(root) + ".tmp"); !os.IsNotExist(err) {
		t.Errorf("temp file still present after write (err=%v)", err)
	}
}

func TestRemoveIsIdempotent(t *testing.T) {
	root := t.TempDir()
	if err := Write(root, New("127.0.0.1", "1", root, os.Getpid(), "token")); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := Remove(root); err != nil {
		t.Fatalf("first remove: %v", err)
	}
	if err := Remove(root); err != nil {
		t.Fatalf("second remove must be a no-op: %v", err)
	}
	if _, err := os.Stat(Path(root)); !os.IsNotExist(err) {
		t.Errorf("record still present after remove (err=%v)", err)
	}
}

func TestEndpointDefaultsToLoopback(t *testing.T) {
	got := Record{Port: "8080"}.Endpoint()
	if want := "http://127.0.0.1:8080"; got != want {
		t.Errorf("endpoint = %q, want %q", got, want)
	}
}

// The JSON field names are the wire contract shared with the runtime; pinning
// them means a renamed struct tag cannot silently break discovery.
func TestJSONFieldNames(t *testing.T) {
	data, err := json.Marshal(Record{
		Port:        "1",
		Host:        "127.0.0.1",
		PID:         7,
		ProjectRoot: "/p",
		StartedAt:   "t",
		Token:       "x",
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, field := range []string{"port", "host", "pid", "projectRoot", "startedAt", "token"} {
		if _, ok := raw[field]; !ok {
			t.Errorf("record missing wire field %q (got %v)", field, data)
		}
	}
}
