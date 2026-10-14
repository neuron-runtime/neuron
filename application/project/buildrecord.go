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

	capabilityrt "github.com/neuron-runtime/neuron/shared/types/capabilityruntime"
	"github.com/neuron-runtime/neuron/shared/types/protocol"
)

const (
	neuronDirectory     = ".neuron"
	buildRecordFile     = "build.json"
	buildsDirectory     = "builds"
	assembliesDirectory = "assemblies"
	manifestFile        = "manifest.json"
	currentPointerFile  = "current.json"
)

// BuildRecord is the persisted outcome of a successful `neuron build`.
// It is stored immutably under .neuron/builds/<fingerprint>/build.json.
// `neuron run` reads the current build via .neuron/current.json which points
// to the fingerprint of the active build.
//
// One project build holds the complete set of its Assemblies. Each Assembly
// carries its own build identity (its per-Assembly fingerprint, registered key,
// and frozen capability runtime set) so a change to one Assembly does not
// rewrite the identity of the others.
type BuildRecord struct {
	// Fingerprint is the content hash of the project's authoring inputs at
	// build time (config file, every Assembly entry, project sources, local
	// capability runtime roots).
	Fingerprint string `json:"fingerprint"`

	// BuiltAt is when the build completed.
	BuiltAt time.Time `json:"builtAt"`

	// Assemblies is the set of Assemblies produced by this build.
	Assemblies []AssemblyBuild `json:"assemblies"`

	// LegacyKey and LegacyCapabilityRuntimes are read-only compatibility fields
	// for build records written before a project could hold multiple
	// Assemblies. A record carrying them is migrated to a single AssemblyBuild
	// by LoadBuildRecord. They exist only to read an already-persisted record;
	// they are never written (nil is omitted). Removal condition: once 0.1.x
	// migration is complete and no pre-multi-Assembly build records remain.
	LegacyKey                *protocol.InstanceKey                    `json:"key,omitempty"`
	LegacyCapabilityRuntimes []capabilityrt.ResolvedCapabilityRuntime `json:"capabilityRuntimes,omitempty"`
}

// AssemblyBuild is the build outcome of one named Assembly of a project.
type AssemblyBuild struct {
	// Name is the Assembly identity, unique within the project.
	Name string `json:"name"`

	// ManifestPath is the project-relative path of the Assembly's canonical
	// manifest artifact (.neuron/assemblies/<name>/manifest.json).
	ManifestPath string `json:"manifestPath"`

	// Fingerprint is the content hash of this Assembly's authoring inputs
	// (the project config, its own entry, and the shared project sources).
	Fingerprint string `json:"fingerprint"`

	// Key is the registered assembly key N.O.R.E. assigned.
	Key protocol.InstanceKey `json:"key"`

	// CapabilityRuntimes is the frozen capability runtime set resolved and
	// installed for this Assembly's requirements. Empty when built-in only.
	CapabilityRuntimes []capabilityrt.ResolvedCapabilityRuntime `json:"capabilityRuntimes,omitempty"`
}

// FindAssembly returns the build entry for a named Assembly.
func (r BuildRecord) FindAssembly(name string) (AssemblyBuild, bool) {
	for _, a := range r.Assemblies {
		if a.Name == name {
			return a, true
		}
	}
	return AssemblyBuild{}, false
}

// CurrentPointer points to the currently active build fingerprint.
type CurrentPointer struct {
	Fingerprint string `json:"fingerprint"`
}

// BuildDir returns the immutable build directory for a given fingerprint.
func BuildDir(projectRoot, fingerprint string) string {
	return filepath.Join(projectRoot, neuronDirectory, buildsDirectory, fingerprint)
}

// BuildRecordPath returns the path of the immutable build record for a fingerprint.
func BuildRecordPath(projectRoot, fingerprint string) string {
	return filepath.Join(BuildDir(projectRoot, fingerprint), buildRecordFile)
}

// CurrentPointerPath returns the path of the mutable current build pointer.
func CurrentPointerPath(projectRoot string) string {
	return filepath.Join(projectRoot, neuronDirectory, currentPointerFile)
}

// SaveBuildRecord persists an immutable build record for projectRoot at the
// given fingerprint. The record is written to .neuron/builds/<fingerprint>/build.json.
// The current pointer is NOT updated; call UpdateCurrentPointer separately.
func SaveBuildRecord(projectRoot, fingerprint string, record BuildRecord) error {
	dir := BuildDir(projectRoot, fingerprint)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create build directory: %w", err)
	}
	path := filepath.Join(dir, buildRecordFile)
	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return fmt.Errorf("encode build record: %w", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write build record: %w", err)
	}
	return nil
}

// LoadBuildRecord reads the immutable build record for a fingerprint.
func LoadBuildRecord(projectRoot, fingerprint string, out *BuildRecord) error {
	if out == nil {
		return fmt.Errorf("build record destination is nil")
	}
	path := BuildRecordPath(projectRoot, fingerprint)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return ErrNotBuilt
		}
		return fmt.Errorf("read build record: %w", err)
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("decode build record: %w", err)
	}
	out.migrateLegacy()
	return nil
}

// migrateLegacy upgrades a pre-multi-Assembly build record in place. Such a
// record stored a single assembly key and frozen capability runtime set and no
// `assemblies` list; it is presented as one AssemblyBuild named by the recorded
// key, so consumers do not need a second code path for old records. The legacy
// fields are cleared so a subsequent save writes only the current shape.
func (r *BuildRecord) migrateLegacy() {
	if len(r.Assemblies) > 0 || r.LegacyKey == nil {
		return
	}
	name := r.LegacyKey.AssemblyID
	r.Assemblies = []AssemblyBuild{{
		Name:               name,
		ManifestPath:       filepath.ToSlash(filepath.Join(neuronDirectory, assembliesDirectory, name, manifestFile)),
		Key:                *r.LegacyKey,
		CapabilityRuntimes: r.LegacyCapabilityRuntimes,
	}}
	r.LegacyKey = nil
	r.LegacyCapabilityRuntimes = nil
}

// SaveCurrentPointer atomically updates the current build pointer.
// Uses write-rename for atomicity.
func SaveCurrentPointer(projectRoot, fingerprint string) error {
	path := CurrentPointerPath(projectRoot)
	ptr := CurrentPointer{Fingerprint: fingerprint}
	data, err := json.MarshalIndent(ptr, "", "  ")
	if err != nil {
		return fmt.Errorf("encode current pointer: %w", err)
	}
	// Atomic write: write to temp file then rename
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("write current pointer temp: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("rename current pointer: %w", err)
	}
	return nil
}

// LoadCurrentPointer reads the current build fingerprint pointer.
func LoadCurrentPointer(projectRoot string) (string, error) {
	path := CurrentPointerPath(projectRoot)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", ErrNotBuilt
		}
		return "", fmt.Errorf("read current pointer: %w", err)
	}
	var ptr CurrentPointer
	if err := json.Unmarshal(data, &ptr); err != nil {
		return "", fmt.Errorf("decode current pointer: %w", err)
	}
	if ptr.Fingerprint == "" {
		return "", ErrNotBuilt
	}
	return ptr.Fingerprint, nil
}

// FingerprintInputs enumerates the authoring inputs a fingerprint covers. Every
// field is optional: only declared paths participate in the hash.
type FingerprintInputs struct {
	// ConfigFile is the absolute path of the effective project config file.
	ConfigFile string

	// Entry is the Assembly entry source file (assembly.ts, assembly.yaml, ...).
	Entry string

	// Sources are additional authoring inputs: capability/ directory, local
	// capability runtime roots, any file whose change invalidates the built Assembly.
	Sources []string

	// Exclude are absolute paths whose content does NOT participate in the
	// hash (for example local capability runtime build artifacts whose exact bytes are
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
