// Package projectruntime defines the discovery contract between the neuron CLI
// and a project-scoped N.O.R.E. process.
//
// Bare `neuron` starts a child `nore --project` process bound to a loopback TCP
// port, and a later `neuron run` has to find that endpoint without being told a
// port and without a global daemon registry. The runtime therefore publishes a
// small record at <projectRoot>/.neuron/runtime.json, and this package owns the
// record's shape and its read/write rules so the writer (the nore process) and
// the reader (the neuron client) cannot drift apart.
//
// The record carries the API token for the loopback endpoint, so the file is
// written owner-only. Loopback is not a security boundary on a machine with
// other users: any local process can connect to the port.
package projectruntime

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

const (
	// DirName is the per-project directory holding mutable project state.
	DirName = ".neuron"

	// FileName is the discovery record inside DirName.
	FileName = "runtime.json"

	// fileMode keeps the record owner-only: it carries the API token.
	fileMode os.FileMode = 0o600

	// dirMode is the permission for the .neuron directory.
	dirMode os.FileMode = 0o700
)

// Record is the discovery contract for one live project runtime. It is written
// when the runtime is ready to serve and removed when it stops.
type Record struct {
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

	// StartedAt is an RFC 3339 UTC timestamp, for diagnostics.
	StartedAt string `json:"startedAt"`

	// Token is the bearer credential for the runtime's API.
	Token string `json:"token"`
}

// Dir returns the .neuron directory for a project root.
func Dir(projectRoot string) string {
	return filepath.Join(projectRoot, DirName)
}

// Path returns the discovery record path for a project root.
func Path(projectRoot string) string {
	return filepath.Join(Dir(projectRoot), FileName)
}

// New builds a record for a runtime that has just bound addr on host.
func New(host, port, projectRoot string, pid int, token string) Record {
	return Record{
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

// Write publishes the record for projectRoot atomically, creating the state
// directory if needed. The record is written to a sibling temporary file and
// renamed into place, so a reader never observes a partial record, and the
// directory and file are owner-only because the record holds the API token.
func Write(projectRoot string, record Record) error {
	path := Path(projectRoot)
	if err := os.MkdirAll(Dir(projectRoot), dirMode); err != nil {
		return fmt.Errorf("create project state directory: %w", err)
	}
	// MkdirAll leaves the mode of an existing directory untouched, and the token
	// must not stay world-readable just because .neuron predates this code.
	if err := os.Chmod(Dir(projectRoot), dirMode); err != nil {
		return fmt.Errorf("secure project state directory: %w", err)
	}

	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal discovery record: %w", err)
	}

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

// Read loads the discovery record for projectRoot. The boolean reports whether
// a record exists; a missing file is not an error, because a project may simply
// not have a running runtime.
func Read(projectRoot string) (Record, bool, error) {
	data, err := os.ReadFile(Path(projectRoot))
	if err != nil {
		if os.IsNotExist(err) {
			return Record{}, false, nil
		}
		return Record{}, false, fmt.Errorf("read discovery record: %w", err)
	}

	var record Record
	if err := json.Unmarshal(data, &record); err != nil {
		return Record{}, false, fmt.Errorf("parse discovery record: %w", err)
	}
	return record, true, nil
}

// Remove deletes the discovery record for projectRoot. A missing file is not an
// error: removal is cleanup on shutdown and must be idempotent.
func Remove(projectRoot string) error {
	if err := os.Remove(Path(projectRoot)); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove discovery record: %w", err)
	}
	return nil
}
