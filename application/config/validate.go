package config

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// configScope distinguishes which configuration file is being validated.
// The project file (neuron.config.*) is the strict, project-identity surface;
// the machine-level global file may still carry engine plumbing such as
// daemon settings until the project runtime replaces it.
type configScope int

const (
	globalScope configScope = iota
	projectScope
)

// deniedKeys lists the configuration paths each scope must never carry, with
// the reason each is refused. Paths are dotted; a dotted path denies the
// whole subtree beneath it.
type deniedKey struct {
	path   string
	reason string
}

var (
	deniedInAnyFile = []deniedKey{
		{
			path:   "storage",
			reason: "is managed by Neuron and cannot be configured here",
		},
		{
			path:   "capabilityRuntimes.storeDir",
			reason: "is managed by Neuron and cannot be configured here",
		},
		{
			path:   "providers.storeDir",
			reason: "is managed by Neuron and cannot be configured here",
		},
	}

	deniedInProjectFile = []deniedKey{
		{
			path:   "daemon",
			reason: "engine plumbing (daemon endpoints, sockets, PID files) belongs to Neuron; run `neuron` in the project to manage the runtime",
		},
	}

	deniedInGlobalFile = []deniedKey{
		{
			path:   "name",
			reason: "assembly identity is project configuration; set it in neuron.config.*",
		},
		{
			path:   "version",
			reason: "assembly identity is project configuration; set it in neuron.config.*",
		},
		{
			path:   "entry",
			reason: "assembly declarations are project configuration; set them in neuron.config.*",
		},
		{
			path:   "assemblies",
			reason: "assembly declarations are project configuration; set them in neuron.config.*",
		},
	}
)

// validateConfigFile parses one configuration file raw — JSON and YAML are
// both handled by the YAML parser — and rejects, before any merging happens:
//
//  1. keys the file's scope must never carry (engine plumbing, project
//     identity in the global file),
//  2. keys that are not part of the configuration schema at all, so a typo
//     fails loudly instead of being silently dropped by the decoder.
//
// This is the single validation gate for configuration files; environment
// and command-line overrides do not pass through it because they are not
// files.
func validateConfigFile(path string, scope configScope) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read config: %w", err)
	}

	var root map[string]any
	if err := yaml.Unmarshal(data, &root); err != nil {
		return fmt.Errorf("decode config: %w", err)
	}

	file := filepath.Base(path)

	denied := append([]deniedKey{}, deniedInAnyFile...)
	switch scope {
	case projectScope:
		denied = append(denied, deniedInProjectFile...)
	case globalScope:
		denied = append(denied, deniedInGlobalFile...)
	}
	for _, d := range denied {
		if _, ok := lookupPath(root, d.path); ok {
			return fmt.Errorf("config %s: `%s` %s", file, d.path, d.reason)
		}
	}

	// Assembly identity must be authored as strings. The decoder would
	// silently coerce an integer (`version: 1`) into "1", reinterpreting a
	// retired config-schema version as an Assembly version without saying so.
	if scope == projectScope {
		for _, key := range []string{"name", "version"} {
			value, ok := root[key]
			if !ok || value == nil {
				continue
			}
			if _, isString := value.(string); !isString {
				return fmt.Errorf("config %s: `%s` must be a quoted string (for example \"1.0.0\")", file, key)
			}
		}
	}

	if err := checkUnknownKeys(root, reflect.TypeOf(Config{}), ""); err != nil {
		return fmt.Errorf("config %s: %w", file, err)
	}

	return nil
}

// lookupPath walks a dotted path through nested raw configuration maps.
func lookupPath(node map[string]any, path string) (any, bool) {
	current := node
	parts := strings.Split(path, ".")
	for i, part := range parts {
		value, ok := current[part]
		if !ok {
			return nil, false
		}
		if i == len(parts)-1 {
			return value, true
		}
		child, ok := value.(map[string]any)
		if !ok {
			return nil, false
		}
		current = child
	}
	return nil, false
}

// checkUnknownKeys walks the raw configuration tree against the Config type
// and reports the first key that has no corresponding struct field. It
// recurses only into struct-typed nodes: maps (variables) and slices
// (registries) are free-form value containers whose contents are not
// configuration keys. Keys are visited in sorted order so a file with
// several unknown keys always reports the same one.
func checkUnknownKeys(node map[string]any, t reflect.Type, prefix string) error {
	for _, key := range sortedKeys(node) {
		value := node[key]
		path := key
		if prefix != "" {
			path = prefix + "." + key
		}

		field, ok := fieldByMapstructureTag(t, key)
		if !ok {
			return fmt.Errorf("unknown key %q", path)
		}

		if value == nil {
			continue
		}
		ft := field.Type
		for ft.Kind() == reflect.Ptr {
			ft = ft.Elem()
		}

		// Struct-typed nodes are configuration keys and are validated
		// recursively.
		if ft.Kind() == reflect.Struct {
			sub, ok := value.(map[string]any)
			if !ok {
				continue
			}
			if err := checkUnknownKeys(sub, ft, path); err != nil {
				return err
			}
			continue
		}

		// A slice of structs (for example `assemblies`) is a list of
		// configuration records, not a free-form value container, so each
		// element is validated against the element type. Slices of scalars and
		// maps stay free-form.
		if ft.Kind() == reflect.Slice {
			elem := ft.Elem()
			for elem.Kind() == reflect.Ptr {
				elem = elem.Elem()
			}
			if elem.Kind() != reflect.Struct {
				continue
			}
			items, ok := value.([]any)
			if !ok {
				continue
			}
			for i, item := range items {
				sub, ok := item.(map[string]any)
				if !ok {
					continue
				}
				if err := checkUnknownKeys(sub, elem, fmt.Sprintf("%s[%d]", path, i)); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// sortedKeys returns a map's keys in lexicographic order.
func sortedKeys(node map[string]any) []string {
	keys := make([]string, 0, len(node))
	for key := range node {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// fieldByMapstructureTag finds the struct field whose mapstructure tag
// matches key.
func fieldByMapstructureTag(t reflect.Type, key string) (reflect.StructField, bool) {
	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		if field.PkgPath != "" { // unexported
			continue
		}
		tag := field.Tag.Get("mapstructure")
		if tag == "-" {
			continue
		}
		if name, _, _ := strings.Cut(tag, ","); name == key {
			return field, true
		}
	}
	return reflect.StructField{}, false
}
