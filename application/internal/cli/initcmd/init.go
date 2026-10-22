package initcmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/neuron-runtime/neuron/application/internal/cli/command"
	"github.com/neuron-runtime/neuron/application/language"
	"github.com/spf13/cobra"
)

// implicitCapabilityRuntimeRoot is the canonical project-scoped capability runtime directory a
// scaffold advertises via capabilityRuntimes.localRoots. The capability runtime catalog always
// scans it, so capability runtimes placed there resolve without extra configuration.
const implicitCapabilityRuntimeRoot = "neuron/capabilityRuntimes"

// scaffoldAssemblyVersion is the Assembly version every scaffold writes into
// the project configuration (neuron.config.*), which owns the Assembly
// identity for every language. No authored Assembly surface repeats it: the
// TypeScript and YAML scaffolds both author no identity, and `neuron build`
// injects the configured name/version at the shared build seam.
const scaffoldAssemblyVersion = "1.0.0"

func New() *cobra.Command {
	cmd := &cobra.Command{
		Use:   command.Init,
		Short: "Scaffold a new neuron project",
		Long: "Create a runnable Neuron project in the given directory (or the " +
			"current one). The project is scaffolded only: install dependencies, " +
			"run `neuron build`, then start the project runtime with `neuron` and " +
			"execute `neuron run`.",
		RunE: initCmdHandler,
	}

	f := cmd.Flags()
	f.StringP("lang", "l", "ts", "project authoring language (ts, typescript, yaml, yml)")

	return cmd
}

func initCmdHandler(cmd *cobra.Command, args []string) error {
	target := "."
	if len(args) >= 1 && strings.TrimSpace(args[0]) != "" {
		target = args[0]
	}

	dir := filepath.Clean(target)
	abs, err := filepath.Abs(dir)
	if err != nil {
		return fmt.Errorf("resolve target directory %q: %w", dir, err)
	}
	name := filepath.Base(abs)
	if name == "." || name == string(filepath.Separator) || name == "" {
		name = "neuron-project"
	}

	langFlag, _ := cmd.Flags().GetString("lang")
	lang, err := language.Normalize(langFlag)
	if err != nil {
		return err
	}

	if err := createDirectory(abs); err != nil {
		return fmt.Errorf("create project directory: %w", err)
	}

	switch lang {
	case language.TypeScript:
		if err := scaffoldTypeScript(abs, name); err != nil {
			return err
		}
	case language.YAML:
		if err := scaffoldYAML(abs, name); err != nil {
			return err
		}
	default:
		return fmt.Errorf("unsupported scaffold language %q", lang)
	}

	if err := ensureImplicitCapabilityRuntimeRoot(abs); err != nil {
		return err
	}

	printNextSteps(abs, lang)
	return nil
}

// createDirectory ensures the target path exists.
func createDirectory(path string) error {
	if path == "" {
		return fmt.Errorf("expected a directory path, but received an empty string")
	}
	return os.MkdirAll(path, 0o755)
}

func createFile(path, content string, mode os.FileMode) error {
	if content == "" {
		return nil
	}
	if _, err := os.Stat(path); err == nil {
		fmt.Printf("skipping %s (already exists)\n", path)
		return nil
	}
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	fmt.Printf("created %s\n", path)
	return nil
}

// writeJSON writes data as indented JSON into path.
func writeJSON(path string, data any) error {
	raw, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return err
	}
	return createFile(path, string(raw)+"\n", 0o644)
}

// configTemplate is the scaffolded neuron.config.json shared by both
// languages. It declares the project identity (name/version), which is the
// single source of the Assembly identity for YAML projects and the
// authoritative value the TypeScript scaffold's authored metadata must match,
// plus the authoring language, the single declared Assembly, the loopback runtime
// endpoint, and the canonical local capability runtime root. Everything the
// runtime needs beyond that has sane defaults inside N.O.R.E. The keys are
// exactly the strict project configuration schema: unknown keys are rejected
// at load time.
func configTemplate(name, lang, entry string) map[string]any {
	return map[string]any{
		"name":    name,
		"version": scaffoldAssemblyVersion,
		"lang":    lang,
		// A project declares one or more named Assemblies. The scaffold
		// declares one, named after the project (so its registered identity is
		// the project name) and marked as the default for `neuron run`.
		"assemblies": []map[string]any{
			{
				"name":    name,
				"entry":   entry,
				"default": true,
			},
		},
		"runtime": map[string]any{
			"host": "localhost",
			"port": "auto",
			"execution": map[string]any{
				"mode":    "wait",
				"timeout": "30m",
			},
		},
		"capabilityRuntimes": map[string]any{
			// The project-scoped capability runtime root. CapabilityRuntimes placed here are
			// resolved automatically; no registry entry is required.
			"localRoots": []string{"./" + implicitCapabilityRuntimeRoot},
		},
		"inspector": map[string]any{
			"enabled": true,
			"address": "127.0.0.1:7433",
		},
	}
}

// ensureImplicitCapabilityRuntimeRoot keeps the canonical ./neuron/capabilityRuntimes directory
// present so locally-authored capability runtimes have an obvious home from day one. The
// .gitkeep marker keeps the empty folder tracked; it is only written when the
// directory is created fresh.
func ensureImplicitCapabilityRuntimeRoot(root string) error {
	dir := filepath.Join(root, implicitCapabilityRuntimeRoot)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create %s: %w", implicitCapabilityRuntimeRoot, err)
	}
	marker := filepath.Join(dir, ".gitkeep")
	if _, err := os.Stat(marker); os.IsNotExist(err) {
		if err := os.WriteFile(marker, nil, 0o644); err != nil {
			return fmt.Errorf("create %s/.gitkeep: %w", implicitCapabilityRuntimeRoot, err)
		}
	}
	return nil
}

func scaffoldTypeScript(root, name string) error {
	if err := writeJSON(filepath.Join(root, "neuron.config.json"), configTemplate(name, "typescript", "assembly.ts")); err != nil {
		return err
	}

	pkg := map[string]any{
		"name":    name,
		"version": "0.1.0",
		"private": true,
		"type":    "module",
		"scripts": map[string]any{
			"start":                  "neuron run",
			"register":               "neuron register",
			"typecheck":              "tsc --noEmit",
			"capability runtime:add": "neuron add",
		},
		"dependencies": map[string]any{
			"@neuronruntime/sdk": "^0.1.0",
		},
		"devDependencies": map[string]any{
			"tsx":        "^4.19.2",
			"typescript": "^5.9.2",
		},
	}
	if err := writeJSON(filepath.Join(root, "package.json"), pkg); err != nil {
		return err
	}

	tsconfig := `{
  "compilerOptions": {
    "target": "ES2022",
    "module": "ESNext",
    "moduleResolution": "Bundler",
    "strict": true,
    "esModuleInterop": true,
    "isolatedModules": true,
    "skipLibCheck": true,
    "noEmit": true
  },
  "include": ["assembly.ts"]
}
`
	if err := createFile(filepath.Join(root, "tsconfig.json"), tsconfig, 0o644); err != nil {
		return err
	}

	// The TypeScript assembly authors no identity and no description:
	// neuron.config.json owns the Assembly name/version, and `neuron build`
	// injects them at the shared build seam. Scaffolding them here would
	// duplicate the configuration.
	assembly := `import { Capability, Assembly } from "@neuronruntime/sdk";

const sayHello = Capability("hello.say")
  .runtime({ name: "neuron:core:set" })
  .paramsSchema<{ name: string }>()
  .resultSchema<{ name: string; message: string }>();

const manifest = Assembly()
  .paramsSchema<{ name: string }>()
  .withParams((data) => sayHello.withParams({ name: data.name }))
  .toManifest();

export default manifest;
`

	return createFile(filepath.Join(root, "assembly.ts"), assembly, 0o644)
}

func scaffoldYAML(root, name string) error {
	if err := writeJSON(filepath.Join(root, "neuron.config.json"), configTemplate(name, "yaml", "assembly.yaml")); err != nil {
		return err
	}

	capabilitiesDir := filepath.Join(root, "capabilities")
	if err := os.MkdirAll(capabilitiesDir, 0o755); err != nil {
		return fmt.Errorf("create capabilities directory: %w", err)
	}

	capability := `apiVersion: neuron/v1
kind: Capability

metadata:
  name: say-hello
  version: 1.0.0
  description: Return a friendly greeting

spec:
  capability runtime:
    type: neuron:core:set
`
	if err := createFile(filepath.Join(capabilitiesDir, "say-hello.yaml"), capability, 0o644); err != nil {
		return err
	}

	// The YAML assembly authors no identity and no description: neuron.config.json
	// owns the Assembly name/version, and `neuron build` injects them at the
	// shared build seam. Scaffolding them here would duplicate the configuration.
	assembly := `apiVersion: neuron/v1
kind: Assembly

capabilities:
  - ref: say-hello
    entry: capabilities/say-hello.yaml
`

	return createFile(filepath.Join(root, "assembly.yaml"), assembly, 0o644)
}

func printNextSteps(dir string, lang language.Language) {
	fmt.Printf("\nInitialized %s project in %s\n", lang, dir)
	if lang == language.TypeScript {
		fmt.Println("\nNext steps:")
		fmt.Println("  npm install        # install the SDK and toolchain")
		fmt.Println("  neuron build       # build the Assembly and register it with the runtime")
		fmt.Println("  neuron             # start the project runtime (terminal 1)")
		fmt.Println("  neuron run         # execute the built Assembly (terminal 2)")
	} else {
		fmt.Println("\nNext steps:")
		fmt.Println("  neuron build       # build the Assembly and register it with the runtime")
		fmt.Println("  neuron             # start the project runtime (terminal 1)")
		fmt.Println("  neuron run         # execute the built Assembly (terminal 2)")
	}
}
