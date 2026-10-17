// Package projectruntime defines the discovery contract between the neuron CLI
// and a project-scoped N.O.R.E. process.
//
// Bare `neuron` starts a child `nore --project` process bound to a loopback TCP
// port, and a later `neuron run` has to find that endpoint without being told a
// port and without a global daemon registry. The runtime therefore publishes a
// small record under <projectRoot>/.neuron/runtime/, and this package owns the
// record's shape and its read/write rules so the writer (the nore process) and
// the reader (the neuron client) cannot drift apart.
//
// A session never overwrites another session's record: each runtime writes its
// own file, named by its session id, and removes only that file. Discovery reads
// the newest record for the project. Running several `neuron` sessions for the
// same project therefore cannot corrupt each other's metadata.
//
// The record carries the API token for the loopback endpoint, so the files are
// written owner-only. Loopback is not a security boundary on a machine with
// other users: any local process can connect to the port.
package projectruntime

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

const (
	// DirName is the per-project directory holding mutable project state.
	DirName = ".neuron"

	// SessionsDirName is the directory of per-session discovery records inside
	// DirName. One file per session keeps concurrent sessions from clobbering
	// one another.
	SessionsDirName = "runtime"

	// recordSuffix is the file extension of a discovery record.
	recordSuffix = ".json"

	// fileMode keeps each record owner-only: it carries the API token.
	fileMode os.FileMode = 0o600

	// dirMode is the permission for the state directory.
	dirMode os.FileMode = 0o700
)

// Record is the discovery contract for one live project runtime session. It is
// written when the runtime is ready to serve and removed when it stops.
type Record struct {
	// SessionID identifies one runtime session and names its record file. It is
	// unique per session so concurrent sessions for the same project do not
	// share a file.
	SessionID string `json:"sessionId"`

	// Port is the bound TCP port, as a string because it is always used to
	// build a URL.
	Port string `json:"port"`

	// Host is the interface the runtime bound to.
	Host string `json:"host"`

	// PID identifies the process owning the runtime, so a reader can reject a
	// record left behind by a crashed runtime.
	PID int `json:"pid"`

	// ProjectRoot is the absolute project directory this runtime serves. A
	// reader rejects a record whose root does not match its own project.
	ProjectRoot string `json:"projectRoot"`

	// StartedAt is an RFC 3339 UTC timestamp, for diagnostics and to order
	// concurrent sessions.
	StartedAt string `json:"startedAt"`

	// Token is the bearer credential for the runtime's API.
	Token string `json:"token"`
}

// Dir returns the .neuron directory for a project root.
func Dir(projectRoot string) string {
	return filepath.Join(projectRoot, DirName)
}

// SessionsDir returns the directory holding the project's per-session
// discovery records.
func SessionsDir(projectRoot string) string {
	return filepath.Join(Dir(projectRoot), SessionsDirName)
}

// Path returns the discovery record path for one session of a project.
func Path(projectRoot, sessionID string) string {
	return filepath.Join(SessionsDir(projectRoot), sessionID+recordSuffix)
}

// New builds a record for a runtime that has just bound addr on host. The
// session id is generated here so every session owns a distinct record file.
func New(host, port, projectRoot string, pid int, token string) Record {
	return Record{
		SessionID:   newSessionID(),
		Port:        port,
		Host:        host,
		PID:         pid,
		ProjectRoot: projectRoot,
		StartedAt:   time.Now().UTC().Format(time.RFC3339),
		Token:       token,
	}
}

// Endpoint returns the base HTTP URL for the runtime, defaulting the host to
// loopback when the record omits it.
func (r Record) Endpoint() string {
	host := r.Host
	if host == "" {
		host = "127.0.0.1"
	}
	return fmt.Sprintf("http://%s:%s", host, r.Port)
}

// Write publishes a session's record, creating the state directory if needed.
// The record is written to a sibling temporary file and renamed into place, so a
// reader never observes a partial record, and the directory and file are
// owner-only because the record holds the API token.
//
// Only the record's own file is written. A concurrent session for the same
// project has a different session id and therefore a different file, which this
// write never touches.
func Write(projectRoot string, record Record) error {
	if record.SessionID == "" {
		return fmt.Errorf("discovery record has no session id")
	}

	dir := SessionsDir(projectRoot)
	if err := os.MkdirAll(dir, dirMode); err != nil {
		return fmt.Errorf("create project state directory: %w", err)
	}
	// MkdirAll leaves the mode of an existing directory untouched, and the token
	// must not stay world-readable just because the directory predates this code.
	if err := os.Chmod(Dir(projectRoot), dirMode); err != nil {
		return fmt.Errorf("secure project state directory: %w", err)
	}
	if err := os.Chmod(dir, dirMode); err != nil {
		return fmt.Errorf("secure session state directory: %w", err)
	}

	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal discovery record: %w", err)
	}

	path := Path(projectRoot, record.SessionID)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, fileMode); err != nil {
		return fmt.Errorf("write discovery record: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("commit discovery record: %w", err)
	}
	return nil
}

// Read returns the most recently started discovery record for projectRoot. The
// boolean reports whether any record exists; a missing directory is not an
// error, because a project may simply not have a running runtime.
//
// It does not judge process liveness: a record left by a crashed runtime is
// rejected by the caller's process check, so this package stays free of
// platform-specific process probing.
func Read(projectRoot string) (Record, bool, error) {
	records, err := ReadAll(projectRoot)
	if err != nil {
		return Record{}, false, err
	}
	if len(records) == 0 {
		return Record{}, false, nil
	}
	// Newest first: concurrent sessions are ordered by start time so discovery
	// is deterministic rather than filesystem-order dependent.
	newest := records[0]
	for _, r := range records[1:] {
		if r.StartedAt > newest.StartedAt {
			newest = r
		}
	}
	return newest, true, nil
}

// ReadAll returns every discovery record for projectRoot, newest first. It is
// the full view for callers that need to consider concurrent sessions
// individually.
func ReadAll(projectRoot string) ([]Record, error) {
	entries, err := os.ReadDir(SessionsDir(projectRoot))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read session state directory: %w", err)
	}

	records := make([]Record, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != recordSuffix {
			continue
		}
		data, err := os.ReadFile(filepath.Join(SessionsDir(projectRoot), entry.Name()))
		if err != nil {
			return nil, fmt.Errorf("read discovery record %s: %w", entry.Name(), err)
		}
		var record Record
		if err := json.Unmarshal(data, &record); err != nil {
			return nil, fmt.Errorf("parse discovery record %s: %w", entry.Name(), err)
		}
		records = append(records, record)
	}

	sort.SliceStable(records, func(i, j int) bool {
		return records[i].StartedAt > records[j].StartedAt
	})
	return records, nil
}

// Remove deletes one session's discovery record. A missing file is not an
// error: removal is cleanup on shutdown and must be idempotent. Another
// session's record is never touched.
func Remove(projectRoot, sessionID string) error {
	if err := os.Remove(Path(projectRoot, sessionID)); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove discovery record: %w", err)
	}
	return nil
}

// newSessionID returns a random, filesystem-safe session identifier.
func newSessionID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand does not fail in practice; fall back to a time-based id
		// so discovery still works rather than panicking a runtime start.
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b[:])
}
