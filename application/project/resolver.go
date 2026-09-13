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
//	systems YAML
//	service YAML
//	entry references
//
// It does not know:
//
//	core.System
//	planner
//	runtime
//	executors
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

// ResolveSystem resolves the system source file at entry into a
// ResolvedProject.
//
// entry is an absolute (or project-relative) path to a `kind: System` YAML
// file. An empty entry selects the default <root>/system.yaml.
//
// This performs:
//
// System
//
//	↓
//
// Services
//
//	↓
//
// # Service entry references
//
// and produces a ResolvedProject.
func (r *Resolver) ResolveSystem(entry string) (*ResolvedProject, error) {
	if strings.TrimSpace(entry) == "" {
		entry = filepath.Join(r.root, "system.yaml")
	}

	systemPath, err := r.resolvePath(r.root, entry)
	if err != nil {
		return nil, fmt.Errorf("resolve system entry: %w", err)
	}

	system, err := r.resolveSystem(systemPath)
	if err != nil {
		return nil, err
	}

	executorRequirements := collectExecutorRequirements(
		system.Services,
	)

	sourceFiles := make([]ResolvedSourceFile, 0, len(r.files))

	for _, file := range r.files {
		sourceFiles = append(sourceFiles, file)
	}

	return &ResolvedProject{
		FormatVersion: "v1",
		ResolvedAt:    nowUTC(),

		System: *system,

		ExecutorRequirements: executorRequirements,

		SourceFiles: sourceFiles,
	}, nil
}

func (r *Resolver) resolveSystem(
	path string,
) (*ResolvedSystem, error) {

	var system SystemFile

	if err := r.readYAML(path, &system); err != nil {
		return nil, fmt.Errorf(
			"load systems %s: %w",
			displayPath(r.root, path),
			err,
		)
	}

	if system.Entry != "" {
		entryPath, err := r.resolvePath(
			filepath.Dir(path),
			system.Entry,
		)
		if err != nil {
			return nil, err
		}

		return r.resolveSystem(entryPath)
	}

	if err := validateSystemBasic(system); err != nil {
		return nil, fmt.Errorf(
			"%w: %s: %v",
			ErrInvalidSystem,
			displayPath(r.root, path),
			err,
		)
	}

	resolved := &ResolvedSystem{
		Definition: system,
		Services:   make([]ResolvedService, 0, len(system.Services)),
		Connectors: make([]ResolvedConnector, 0, len(system.Connectors)),
	}

	seenRefs := make(map[string]bool)

	for _, ref := range system.Services {
		if ref.Ref == "" {
			return nil, fmt.Errorf(
				"%w: empty service reference in %s",
				ErrInvalidSystem,
				displayPath(r.root, path),
			)
		}

		if seenRefs[ref.Ref] {
			return nil, fmt.Errorf(
				"%w: duplicate service reference %q",
				ErrInvalidSystem,
				ref.Ref,
			)
		}

		seenRefs[ref.Ref] = true

		servicePath, err := r.resolvePath(
			filepath.Dir(path),
			ref.Entry,
		)
		if err != nil {
			return nil, fmt.Errorf(
				"resolve service %q: %w",
				ref.Ref,
				err,
			)
		}

		service, err := r.resolveService(servicePath)
		if err != nil {
			return nil, fmt.Errorf(
				"resolve service %q: %w",
				ref.Ref,
				err,
			)
		}

		resolved.Services = append(
			resolved.Services,
			ResolvedService{
				Ref:        ref.Ref,
				SourcePath: displayPath(r.root, servicePath),
				Definition: *service,
			},
		)
	}

	connectors, err := r.resolveConnectors(path, system, seenRefs)
	if err != nil {
		return nil, err
	}
	resolved.Connectors = connectors

	return resolved, nil
}

func (r *Resolver) resolveConnectors(
	systemPath string,
	system SystemFile,
	serviceRefs map[string]bool,
) ([]ResolvedConnector, error) {
	var resolved []ResolvedConnector

	for _, connRef := range system.Connectors {
		var connFile ConnectorFile
		var sourcePath string

		if connRef.Entry != "" {
			entryPath, err := r.resolvePath(
				filepath.Dir(systemPath),
				connRef.Entry,
			)
			if err != nil {
				return nil, fmt.Errorf("resolve connector entry %q: %w", connRef.Entry, err)
			}

			connFile, err = r.resolveConnector(entryPath)
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
			// Inline connector
			connFile = ConnectorFile{
				APIVersion: "neuron/v1",
				Kind:       "Connector",
				Metadata: ConnectorMetadata{
					Name:    fmt.Sprintf("connector-%s-to-%s", connRef.From, connRef.To),
					Version: "1.0.0",
				},
				From:        connRef.From,
				To:          connRef.To,
				Mappings:    connRef.Mappings,
				Validations: connRef.Validations,
			}
			sourcePath = displayPath(r.root, systemPath) + " (inline)"
		}

		// Validate from/to reference existing services
		if !serviceRefs[connFile.From] {
			return nil, fmt.Errorf("connector %q references unknown service %q", connFile.Metadata.Name, connFile.From)
		}
		if !serviceRefs[connFile.To] {
			return nil, fmt.Errorf("connector %q references unknown service %q", connFile.Metadata.Name, connFile.To)
		}

		resolved = append(resolved, ResolvedConnector{
			Ref:        connFile.Metadata.Name,
			SourcePath: sourcePath,
			Definition: connFile,
		})
	}

	return resolved, nil
}

func (r *Resolver) resolveConnector(
	path string,
) (ConnectorFile, error) {

	var conn ConnectorFile

	if err := r.readYAML(path, &conn); err != nil {
		return ConnectorFile{}, fmt.Errorf(
			"load connector %s: %w",
			displayPath(r.root, path),
			err,
		)
	}

	if err := validateConnectorFile(conn); err != nil {
		return ConnectorFile{}, fmt.Errorf(
			"%w: %s: %v",
			ErrInvalidConnector,
			displayPath(r.root, path),
			err,
		)
	}

	return conn, nil
}

func (r *Resolver) resolveService(
	path string,
) (*ServiceFile, error) {

	var service ServiceFile

	if err := r.readYAML(path, &service); err != nil {
		return nil, fmt.Errorf(
			"load service %s: %w",
			displayPath(r.root, path),
			err,
		)
	}

	if service.Entry != "" {
		entryPath, err := r.resolvePath(
			filepath.Dir(path),
			service.Entry,
		)
		if err != nil {
			return nil, err
		}

		return r.resolveService(entryPath)
	}

	if err := validateServiceBasic(service); err != nil {
		return nil, fmt.Errorf(
			"%w: %s: %v",
			ErrInvalidService,
			displayPath(r.root, path),
			err,
		)
	}

	return &service, nil
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
