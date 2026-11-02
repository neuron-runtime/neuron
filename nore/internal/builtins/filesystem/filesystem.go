// Package filesystem implements the neuron:core:filesystem capability
// runtime.
package filesystem

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/neuron-runtime/neuron/nore/internal/builtins/input"
	"github.com/neuron-runtime/neuron/nore/internal/builtins/paths"
	"github.com/neuron-runtime/neuron/nore/internal/contracts"
	"github.com/neuron-runtime/neuron/shared/types/builtins"
)

// Filesystem reads files and directories.
//
// It is the read-only view of the machine the assembly runs on. The reach is
// bounded by the policy: the project root, and the directories the project
// added to it. A path that resolves outside them is refused, and a symbolic
// link inside them does not grant reach beyond them.
type Filesystem struct {
	// Policy bounds what the built-in may reach and how much it may return.
	// A zero policy means the compiled-in ceilings.
	Policy builtins.Policy
}

// errListingComplete ends a listing that reached the entry limit. It is not a
// failure: the listing is complete as far as the policy allows, and the
// result says so.
var errListingComplete = errors.New("the listing reached its limit")

// Execute performs the operation named by the capability's input.
func (f Filesystem) Execute(ctx context.Context, execution contracts.ExecutionContext) (map[string]any, error) {
	// The policy is resolved here rather than trusted, because a runtime may
	// also be constructed outside the registry. The resolution only narrows,
	// so a policy that has already been resolved is unchanged.
	policy, err := builtins.Effective(f.Policy)
	if err != nil {
		return nil, fmt.Errorf("neuron:core:filesystem: %w", err)
	}

	values := input.New("neuron:core:filesystem", execution.CapabilityConfigurations, execution.Params)
	logger := contracts.LoggerOrNop(execution.Logger)

	operation, err := values.RequiredString("operation")
	if err != nil {
		return nil, err
	}

	target, err := values.RequiredString("path")
	if err != nil {
		return nil, err
	}

	roots := paths.NewRoots(policy.ProjectRoot, policy.FilesystemRoots)

	resolved, err := roots.Resolve(roots.Base(), target)
	if err != nil {
		return nil, fmt.Errorf("neuron:core:filesystem: %w", err)
	}

	logger.Debug(ctx, "neuron:core:filesystem: performing the operation",
		contracts.F("operation", operation),
		contracts.F("path", resolved),
	)

	switch strings.ToLower(strings.TrimSpace(operation)) {
	case "read":
		return f.read(values, resolved, policy)
	case "list":
		return f.list(values, resolved, policy)
	case "stat":
		return f.stat(resolved)
	default:
		return nil, fmt.Errorf("neuron:core:filesystem: unknown operation %q; supported operations are read, list, stat", operation)
	}
}

// read returns the content of a file.
func (f Filesystem) read(values input.Values, resolved string, policy builtins.Policy) (map[string]any, error) {
	format, err := values.Format("format", input.FormatJSON)
	if err != nil {
		return nil, err
	}

	info, err := os.Stat(resolved)
	if err != nil {
		return nil, fmt.Errorf("neuron:core:filesystem: read %q: %w", resolved, err)
	}
	if info.IsDir() {
		return nil, fmt.Errorf("neuron:core:filesystem: %q is a directory; use the list operation", resolved)
	}

	file, err := os.Open(resolved)
	if err != nil {
		return nil, fmt.Errorf("neuron:core:filesystem: read %q: %w", resolved, err)
	}
	defer file.Close()

	// The content is read one byte past the limit so a file that is exactly at
	// the limit is read and a file that exceeds it is refused rather than
	// silently shortened: a caller that receives a shortened file cannot tell
	// it apart from a complete one.
	content, err := io.ReadAll(io.LimitReader(file, policy.MaxFileReadBytes+1))
	if err != nil {
		return nil, fmt.Errorf("neuron:core:filesystem: read %q: %w", resolved, err)
	}
	if int64(len(content)) > policy.MaxFileReadBytes {
		return nil, fmt.Errorf("neuron:core:filesystem: the file %q is larger than the limit of %d bytes",
			resolved, policy.MaxFileReadBytes)
	}

	value, err := representContent(content, format)
	if err != nil {
		return nil, err
	}

	return map[string]any{
		"path":    resolved,
		"content": value,
		"size":    float64(info.Size()),
	}, nil
}

// list returns the entries of a directory.
func (f Filesystem) list(values input.Values, resolved string, policy builtins.Policy) (map[string]any, error) {
	recursive, err := values.Bool("recursive", false)
	if err != nil {
		return nil, err
	}

	info, err := os.Stat(resolved)
	if err != nil {
		return nil, fmt.Errorf("neuron:core:filesystem: list %q: %w", resolved, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("neuron:core:filesystem: %q is not a directory; use the read operation", resolved)
	}

	entries := make([]any, 0, 16)
	truncated := false

	appendEntry := func(path string, entry fs.DirEntry) error {
		if len(entries) >= policy.MaxDirectoryEntries {
			truncated = true
			return errListingComplete
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		entries = append(entries, map[string]any{
			"name": entry.Name(),
			"path": path,
			"type": entryType(entry),
			"size": float64(info.Size()),
		})
		return nil
	}

	err = filepath.WalkDir(resolved, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == resolved {
			return nil
		}
		if !recursive && entry.IsDir() {
			// The directory is listed, but its contents are not.
			if err := appendEntry(path, entry); err != nil {
				return err
			}
			return filepath.SkipDir
		}
		return appendEntry(path, entry)
	})

	if err != nil && !errors.Is(err, errListingComplete) {
		return nil, fmt.Errorf("neuron:core:filesystem: list %q: %w", resolved, err)
	}

	result := map[string]any{
		"path":    resolved,
		"entries": entries,
	}
	// The truncation is reported, so a caller can tell a shortened listing
	// from a complete one.
	if truncated {
		result["entries_truncated"] = true
	}
	return result, nil
}

// stat reports what a path is.
func (f Filesystem) stat(resolved string) (map[string]any, error) {
	info, err := os.Stat(resolved)
	if err != nil {
		if os.IsNotExist(err) {
			// A path that is not there is an answer, not a failure: the
			// caller asked what is there.
			return map[string]any{
				"path":   resolved,
				"exists": false,
			}, nil
		}
		return nil, fmt.Errorf("neuron:core:filesystem: stat %q: %w", resolved, err)
	}

	return map[string]any{
		"path":     resolved,
		"exists":   true,
		"type":     fileType(info),
		"size":     float64(info.Size()),
		"modified": info.ModTime().UTC().Format(time.RFC3339),
	}, nil
}

// representContent converts the content of a file into the representation the
// caller asked for.
//
// The rule is the same as for the other built-ins: a JSON file is returned as
// the value it represents, and a file that is not JSON is refused in the JSON
// format rather than handed over as a string the caller cannot bind.
func representContent(content []byte, format input.Format) (any, error) {
	if len(strings.TrimSpace(string(content))) == 0 {
		if format == input.FormatText {
			return "", nil
		}
		return nil, nil
	}

	if format == input.FormatText {
		return string(content), nil
	}

	var value any
	if err := json.Unmarshal(content, &value); err != nil {
		return nil, fmt.Errorf("neuron:core:filesystem: the file is not JSON; use format \"text\" to receive it as text")
	}
	return value, nil
}

// entryType names the kind of a directory entry.
func entryType(entry fs.DirEntry) string {
	switch {
	case entry.IsDir():
		return "directory"
	case entry.Type().IsRegular():
		return "file"
	case entry.Type()&fs.ModeSymlink != 0:
		return "link"
	default:
		return "other"
	}
}

// fileType names the kind of a file.
func fileType(info fs.FileInfo) string {
	switch {
	case info.IsDir():
		return "directory"
	case info.Mode().IsRegular():
		return "file"
	default:
		return "other"
	}
}
