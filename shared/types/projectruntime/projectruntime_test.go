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
	record := New("127.0.0.1", "1", root, os.Getpid(), "token")
	if err := Write(root, record); err != nil {
		t.Fatalf("write: %v", err)
	}

	info, err := os.Stat(Path(root, record.SessionID))
	if err != nil {
		t.Fatalf("stat record: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("record permissions = %o, want 600", perm)
	}

	dirInfo, err := os.Stat(SessionsDir(root))
	if err != nil {
		t.Fatalf("stat dir: %v", err)
	}
	if perm := dirInfo.Mode().Perm(); perm != 0o700 {
		t.Errorf("session directory permissions = %o, want 700", perm)
	}
}

// An interrupted write must never leave the temporary file behind.
func TestWriteLeavesNoTempFile(t *testing.T) {
	root := t.TempDir()
	record := New("127.0.0.1", "1", root, os.Getpid(), "token")
	if err := Write(root, record); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := os.Stat(Path(root, record.SessionID) + ".tmp"); !os.IsNotExist(err) {
		t.Errorf("temp file still present after write (err=%v)", err)
	}
}

func TestRemoveIsIdempotent(t *testing.T) {
	root := t.TempDir()
	record := New("127.0.0.1", "1", root, os.Getpid(), "token")
	if err := Write(root, record); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := Remove(root, record.SessionID); err != nil {
		t.Fatalf("first remove: %v", err)
	}
	if err := Remove(root, record.SessionID); err != nil {
		t.Fatalf("second remove must be a no-op: %v", err)
	}
	if _, err := os.Stat(Path(root, record.SessionID)); !os.IsNotExist(err) {
		t.Errorf("record still present after remove (err=%v)", err)
	}
}

// Two sessions for the same project must each keep their own record: a write
// must not overwrite the other session, and removing one must not delete the
// other's metadata.
func TestSessionsDoNotClobberEachOther(t *testing.T) {
	root := t.TempDir()
	first := New("127.0.0.1", "1", root, os.Getpid(), "first")
	second := New("127.0.0.1", "2", root, os.Getpid(), "second")

	if err := Write(root, first); err != nil {
		t.Fatalf("write first: %v", err)
	}
	if err := Write(root, second); err != nil {
		t.Fatalf("write second: %v", err)
	}

	records, err := ReadAll(root)
	if err != nil {
		t.Fatalf("read all: %v", err)
	}
	if len(records) != 2 {
		t.Fatalf("records = %d, want 2 (sessions overwrote each other)", len(records))
	}

	if err := Remove(root, first.SessionID); err != nil {
		t.Fatalf("remove first: %v", err)
	}
	if _, err := os.Stat(Path(root, second.SessionID)); err != nil {
		t.Errorf("removing one session deleted the other's record: %v", err)
	}
}

// Read resolves the newest record so discovery is deterministic when several
// sessions are running.
func TestReadReturnsNewestSession(t *testing.T) {
	root := t.TempDir()
	older := New("127.0.0.1", "1", root, os.Getpid(), "older")
	older.StartedAt = "2020-01-01T00:00:00Z"
	newer := New("127.0.0.1", "2", root, os.Getpid(), "newer")
	newer.StartedAt = "2030-01-01T00:00:00Z"

	if err := Write(root, newer); err != nil {
		t.Fatalf("write newer: %v", err)
	}
	if err := Write(root, older); err != nil {
		t.Fatalf("write older: %v", err)
	}

	got, ok, err := Read(root)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !ok {
		t.Fatal("record not found")
	}
	if got.SessionID != newer.SessionID {
		t.Errorf("read resolved %q, want the newest session %q", got.SessionID, newer.SessionID)
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
