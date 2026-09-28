package project

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Resolver resolves source YAML definitions into a ResolvedProject.
//
// Resolver is intentionally independent of N.O.R.E.
//
// It knows:
//
//	assemblies YAML
//	capability YAML
//	entry references
//
// It does not know:
//
//	core.Assembly
//	planner
//	runtime
//	capability runtimes
//	instances
type Resolver struct {
	root string

	// cache avoids reading the same file repeatedly.
	cache map[string][]byte

	// stack represents the current recursive resolution path.
	//
	// This is different from a global "visited" set.
	//
	// A file can legitimately be referenced by multiple things.
	// It is only a cycle when it appears in the CURRENT recursion
	// chain.
	stack map[string]bool

	files map[string]ResolvedSourceFile
}

// NewResolver creates a resolver rooted at projectRoot.
func NewResolver(projectRoot string) (*Resolver, error) {
	root, err := filepath.Abs(projectRoot)
	if err != nil {
		return nil, fmt.Errorf("resolve project root: %w", err)
	}

	info, err := os.Stat(root)
	if err != nil {
		return nil, fmt.Errorf("stat project root: %w", err)
	}

	if !info.IsDir() {
		return nil, fmt.Errorf("project root is not a directory: %s", root)
	}

	return &Resolver{
		root:  root,
		cache: make(map[string][]byte),
		stack: make(map[string]bool),
		files: make(map[string]ResolvedSourceFile),
	}, nil
}

// Root returns the absolute project root.
func (r *Resolver) Root() string {
	return r.root
}

// ResolveAssembly resolves the assembly source file at entry into a
// ResolvedProject.
//
// entry is an absolute (or project-relative) path to a `kind: Assembly` YAML
// file. An empty entry selects the default <root>/assembly.yaml.
//
// This performs:
//
// Assembly
//
//	↓
//
// Capabilities
//
//	↓
//
// # Capability entry references
//
// and produces a ResolvedProject.
func (r *Resolver) ResolveAssembly(entry string) (*ResolvedProject, error) {
	if strings.TrimSpace(entry) == "" {
		entry = filepath.Join(r.root, "assembly.yaml")
	}

	assemblyPath, err := r.resolvePath(r.root, entry)
	if err != nil {
		return nil, fmt.Errorf("resolve assembly entry: %w", err)
	}

	assembly, err := r.resolveAssembly(assemblyPath)
	if err != nil {
		return nil, err
	}

	capabilityRuntimeRequirements := collectCapabilityRuntimeRequirements(
		assembly.Capabilities,
	)

	sourceFiles := make([]ResolvedSourceFile, 0, len(r.files))

	for _, file := range r.files {
		sourceFiles = append(sourceFiles, file)
	}

	return &ResolvedProject{
		FormatVersion: "v1",
		ResolvedAt:    nowUTC(),

		Assembly: *assembly,

		CapabilityRuntimeRequirements: capabilityRuntimeRequirements,

		SourceFiles: sourceFiles,
	}, nil
}

func (r *Resolver) resolveAssembly(
	path string,
) (*ResolvedAssembly, error) {

	var assembly AssemblyFile

	if err := r.readYAML(path, &assembly); err != nil {
		return nil, fmt.Errorf(
			"load assemblies %s: %w",
			displayPath(r.root, path),
			err,
		)
	}

	if assembly.Entry != "" {
		entryPath, err := r.resolvePath(
			filepath.Dir(path),
			assembly.Entry,
		)
		if err != nil {
			return nil, err
		}

		return r.resolveAssembly(entryPath)
	}

	if err := validateAssemblyBasic(assembly); err != nil {
		return nil, fmt.Errorf(
			"%w: %s: %v",
			ErrInvalidAssembly,
			displayPath(r.root, path),
			err,
		)
	}

	resolved := &ResolvedAssembly{
		Definition:   assembly,
		Capabilities: make([]ResolvedCapability, 0, len(assembly.Capabilities)),
		Bindings:     make([]ResolvedBinding, 0, len(assembly.Bindings)),
	}

	seenRefs := make(map[string]bool)

	for _, ref := range assembly.Capabilities {
		if ref.Ref == "" {
			return nil, fmt.Errorf(
				"%w: empty capability reference in %s",
				ErrInvalidAssembly,
				displayPath(r.root, path),
			)
		}

		if seenRefs[ref.Ref] {
			return nil, fmt.Errorf(
				"%w: duplicate capability reference %q",
				ErrInvalidAssembly,
				ref.Ref,
			)
		}

		seenRefs[ref.Ref] = true

		capabilityPath, err := r.resolvePath(
			filepath.Dir(path),
			ref.Entry,
		)
		if err != nil {
			return nil, fmt.Errorf(
				"resolve capability %q: %w",
				ref.Ref,
				err,
			)
		}

		capability, err := r.resolveCapability(capabilityPath)
		if err != nil {
			return nil, fmt.Errorf(
				"resolve capability %q: %w",
				ref.Ref,
				err,
			)
		}

		resolved.Capabilities = append(
			resolved.Capabilities,
			ResolvedCapability{
				Ref:        ref.Ref,
				SourcePath: displayPath(r.root, capabilityPath),
				Definition: *capability,
			},
		)
	}

	bindings, err := r.resolveBindings(path, assembly, seenRefs)
	if err != nil {
		return nil, err
	}
	resolved.Bindings = bindings

	return resolved, nil
}

func (r *Resolver) resolveBindings(
	assemblyPath string,
	assembly AssemblyFile,
	capabilityRefs map[string]bool,
) ([]ResolvedBinding, error) {
	var resolved []ResolvedBinding

	for _, connRef := range assembly.Bindings {
		var connFile BindingFile
		var sourcePath string

		if connRef.Entry != "" {
			entryPath, err := r.resolvePath(
				filepath.Dir(assemblyPath),
				connRef.Entry,
			)
			if err != nil {
				return nil, fmt.Errorf("resolve binding entry %q: %w", connRef.Entry, err)
			}

			connFile, err = r.resolveBinding(entryPath)
			if err != nil {
				return nil, err
			}
			sourcePath = displayPath(r.root, entryPath)

			// Inline mappings/validations override the entry file
			if len(connRef.Mappings) > 0 {
				connFile.Mappings = connRef.Mappings
			}
			if len(connRef.Validations) > 0 {
				connFile.Validations = connRef.Validations
			}
			// Inline from/to override entry file
			if connRef.From != "" {
				connFile.From = connRef.From
			}
			if connRef.To != "" {
				connFile.To = connRef.To
			}
		} else {
			// Inline binding
			connFile = BindingFile{
				APIVersion: "neuron/v1",
				Kind:       "Binding",
				Metadata: BindingMetadata{
					Name:    fmt.Sprintf("binding-%s-to-%s", connRef.From, connRef.To),
					Version: "1.0.0",
				},
				From:        connRef.From,
				To:          connRef.To,
				Mappings:    connRef.Mappings,
				Validations: connRef.Validations,
			}
			sourcePath = displayPath(r.root, assemblyPath) + " (inline)"
		}

		// Validate from/to reference existing capabilities
		if !capabilityRefs[connFile.From] {
			return nil, fmt.Errorf("binding %q references unknown capability %q", connFile.Metadata.Name, connFile.From)
		}
		if !capabilityRefs[connFile.To] {
			return nil, fmt.Errorf("binding %q references unknown capability %q", connFile.Metadata.Name, connFile.To)
		}

		resolved = append(resolved, ResolvedBinding{
			Ref:        connFile.Metadata.Name,
			SourcePath: sourcePath,
			Definition: connFile,
		})
	}

	return resolved, nil
}

func (r *Resolver) resolveBinding(
	path string,
) (BindingFile, error) {

	var conn BindingFile

	if err := r.readYAML(path, &conn); err != nil {
		return BindingFile{}, fmt.Errorf(
			"load binding %s: %w",
			displayPath(r.root, path),
			err,
		)
	}

	if err := validateBindingFile(conn); err != nil {
		return BindingFile{}, fmt.Errorf(
			"%w: %s: %v",
			ErrInvalidBinding,
			displayPath(r.root, path),
			err,
		)
	}

	return conn, nil
}

func (r *Resolver) resolveCapability(
	path string,
) (*CapabilityFile, error) {

	var capability CapabilityFile

	if err := r.readYAML(path, &capability); err != nil {
		return nil, fmt.Errorf(
			"load capability %s: %w",
			displayPath(r.root, path),
			err,
		)
	}

	if capability.Entry != "" {
		entryPath, err := r.resolvePath(
			filepath.Dir(path),
			capability.Entry,
		)
		if err != nil {
			return nil, err
		}

		return r.resolveCapability(entryPath)
	}

	if err := validateCapabilityBasic(capability); err != nil {
		return nil, fmt.Errorf(
			"%w: %s: %v",
			ErrInvalidCapability,
			displayPath(r.root, path),
			err,
		)
	}

	return &capability, nil
}

func (r *Resolver) readYAML(
	path string,
	target any,
) error {

	path, err := filepath.Abs(path)
	if err != nil {
		return err
	}

	path = filepath.Clean(path)

	// Cycle detection is based on the current recursive stack.
	if r.stack[path] {
		return fmt.Errorf(
			"%w: %s",
			ErrCircularReference,
			displayPath(r.root, path),
		)
	}

	r.stack[path] = true
	defer delete(r.stack, path)

	data, ok := r.cache[path]

	if !ok {
		data, err = os.ReadFile(path)
		if err != nil {
			return fmt.Errorf(
				"read %s: %w",
				displayPath(r.root, path),
				err,
			)
		}

		r.cache[path] = data

		r.recordSource(path, data)
	}

	if err := yaml.Unmarshal(data, target); err != nil {
		return fmt.Errorf(
			"parse %s: %w",
			displayPath(r.root, path),
			err,
		)
	}

	return nil
}

func (r *Resolver) resolvePath(
	baseDir string,
	raw string,
) (string, error) {

	raw = strings.TrimSpace(raw)

	if raw == "" {
		return "", fmt.Errorf("empty entry path")
	}

	var path string

	if filepath.IsAbs(raw) {
		path = filepath.Clean(raw)
	} else {
		path = filepath.Join(baseDir, raw)
	}

	path = filepath.Clean(path)

	// Prevent definitions from escaping the project.
	relative, err := filepath.Rel(r.root, path)
	if err != nil {
		return "", err
	}

	if relative == ".." ||
		strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf(
			"path escapes project root: %s",
			raw,
		)
	}

	info, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf(
			"file %q not found: %w",
			raw,
			err,
		)
	}

	if info.IsDir() {
		return "", fmt.Errorf(
			"entry path is a directory: %s",
			raw,
		)
	}

	return path, nil
}

func (r *Resolver) recordSource(
	path string,
	data []byte,
) {

	hash := sha256.Sum256(data)

	r.files[path] = ResolvedSourceFile{
		Path: displayPath(r.root, path),
		SHA256: hex.EncodeToString(
			hash[:],
		),
		Kind: detectKind(data),
	}
}

func detectKind(data []byte) string {
	var header struct {
		Kind string `yaml:"kind"`
	}

	if err := yaml.Unmarshal(data, &header); err != nil {
		return "unknown"
	}

	if header.Kind == "" {
		return "unknown"
	}

	return header.Kind
}

func displayPath(root, path string) string {
	relative, err := filepath.Rel(root, path)
	if err != nil {
		return filepath.ToSlash(path)
	}

	return filepath.ToSlash(relative)
}

func nowUTC() time.Time {
	return time.Now().UTC()
}
