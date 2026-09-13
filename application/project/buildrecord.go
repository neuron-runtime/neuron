package project

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	shadexec "github.com/Muhammad-Jay/neuron/shared/types/executor"
	"github.com/Muhammad-Jay/neuron/shared/types/protocol"
)

const buildRecordFile = "build.json"

// BuildRecord is the persisted outcome of a successful `neuron build`.
// `neuron run` reads it to address the registered system and to detect
// staleness: when the current authoring fingerprint differs from the recorded
// one, the project must be rebuilt before it runs.
type BuildRecord struct {
	// Fingerprint is the content hash of the authoring inputs at build time
	// (config file, System entry, project sources, local executor roots).
	Fingerprint string `json:"fingerprint"`

	// Key is the registered system key N.O.R.E. assigned.
	Key protocol.InstanceKey `json:"key"`

	// BuiltAt is when the build completed.
	BuiltAt time.Time `json:"builtAt"`

	// Executors is the frozen executor set resolved and installed for the
	// System's executor requirements. Empty when the System is built-in only.
	Executors []shadexec.ResolvedExecutor `json:"executors"`
}

// BuildRecordPath returns the path of the persisted build record inside the
// project's .neuron directory.
func BuildRecordPath(projectRoot string) string {
	return filepath.Join(projectRoot, neuronDirectory, buildRecordFile)
}

// SaveBuildRecord persists a build record for projectRoot.
func SaveBuildRecord(projectRoot string, record BuildRecord) error {
	path := BuildRecordPath(projectRoot)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create .neuron directory: %w", err)
	}
	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return fmt.Errorf("encode build record: %w", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write build record: %w", err)
	}
	return nil
}

// LoadBuildRecord reads the build record persisted by SaveBuildRecord. A
// missing record is ErrNotBuilt.
func LoadBuildRecord(projectRoot string, out *BuildRecord) error {
	if out == nil {
		return fmt.Errorf("build record destination is nil")
	}
	data, err := os.ReadFile(BuildRecordPath(projectRoot))
	if err != nil {
		if os.IsNotExist(err) {
			return ErrNotBuilt
		}
		return fmt.Errorf("read build record: %w", err)
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("decode build record: %w", err)
	}
	return nil
}

// FingerprintInputs enumerates the authoring inputs a fingerprint covers. Every
// field is optional: only declared paths participate in the hash.
type FingerprintInputs struct {
	// ConfigFile is the absolute path of the effective project config file.
	ConfigFile string

	// Entry is the System entry source file (system.ts, system.yaml, ...).
	Entry string

	// Sources are additional authoring inputs: service/ directory, local
	// executor roots, any file whose change invalidates the built System.
	Sources []string

	// Exclude are absolute paths whose content does NOT participate in the
	// hash (for example local-executor build artifacts whose exact bytes are
	// derived by build commands and would otherwise churn the fingerprint).
	// A path that names a directory excludes that entire subtree. Files under
	// an excluded directory never resolve it into a fingerprint input.
	Exclude []string
}

// ComputeFingerprint hashes the authoring inputs listed in in. Files inside
// scanned directories are sorted so the fingerprint is stable; generated or
// environment-owned directories (.neuron, node_modules, .git) are skipped.
// A declared input that does not exist is an error: the fingerprint must
// reflect exactly what the build consumed.
func ComputeFingerprint(in FingerprintInputs) (string, error) {
	excluded := make(map[string]bool, len(in.Exclude))
	for _, p := range in.Exclude {
		if strings.TrimSpace(p) == "" {
			continue
		}
		excluded[filepath.Clean(p)] = true
	}

	paths := []string{in.ConfigFile, in.Entry}
	paths = append(paths, in.Sources...)

	var files []string
	for _, p := range paths {
		if strings.TrimSpace(p) == "" {
			continue
		}
		info, err := os.Stat(p)
		if err != nil {
			return "", fmt.Errorf("fingerprint input %s: %w", p, err)
		}
		if isExcluded(filepath.Clean(p), excluded) {
			continue
		}
		if !info.IsDir() {
			files = append(files, filepath.Clean(p))
			continue
		}
		collected, err := collectFingerprintFiles(p, excluded)
		if err != nil {
			return "", err
		}
		files = append(files, collected...)
	}

	sort.Strings(files)

	h := sha256.New()
	for _, f := range files {
		writeFingerprintFile(h, f)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// collectFingerprintFiles walks dir and returns its authoring files, skipping
// generated or environment-owned subdirectories and any excluded paths.
func collectFingerprintFiles(dir string, excluded map[string]bool) ([]string, error) {
	var out []string
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if isExcluded(path, excluded) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", ".neuron", "node_modules", "dist", "build", ".publish":
				return filepath.SkipDir
			}
			return nil
		}
		out = append(out, filepath.Clean(path))
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("scan fingerprint directory %s: %w", dir, err)
	}
	return out, nil
}

// isExcluded reports whether path or one of its parents is in excluded.
func isExcluded(path string, excluded map[string]bool) bool {
	for p := path; ; p = filepath.Dir(p) {
		if excluded[p] {
			return true
		}
		parent := filepath.Dir(p)
		if parent == p {
			return false
		}
	}
}

// writeFingerprintFile records one file's name and content into h.
func writeFingerprintFile(h hash.Hash, path string) {
	data, err := os.ReadFile(path)
	if err != nil {
		// A file can vanish between scanning and hashing; record a readable
		// tombstone so the hash still depends on the name and the error stays
		// visible in the mismatch rather than silently skipping the file.
		fmt.Fprintf(h, "%s\000<unreadable:%v>\n", filepath.ToSlash(path), err)
		return
	}
	fmt.Fprintf(h, "%s\000%d\000", filepath.ToSlash(path), len(data))
	h.Write(data)
	fmt.Fprint(h, "\n")
}
