package initcmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Muhammad-Jay/neuron/application/internal/cli/command"
	"github.com/Muhammad-Jay/neuron/application/language"
	"github.com/spf13/cobra"
)

// implicitCapabilityRuntimeRoot is the canonical project-scoped capability runtime directory a
// scaffold advertises via capabilityRuntimes.localRoots. The capability runtime catalog always
// scans it, so capability runtimes placed there resolve without extra configuration.
const implicitCapabilityRuntimeRoot = "neuron/capabilityRuntimes"

func New() *cobra.Command {
	cmd := &cobra.Command{
		Use:   command.Init,
		Short: "Scaffold a new neuron project",
		Long: "Create a runnable Neuron project in the given directory (or the " +
			"current one). The project is scaffolded only: install dependencies " +
			"and run `neuron build && neuron run` to see it execute.",
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
// languages. It declares the authoring language, the Assembly entry file, and
// the canonical local capability runtime root. Everything the runtime needs beyond that
// has sane defaults inside N.O.R.E.
func configTemplate(name, lang, entry string) map[string]any {
	return map[string]any{
		"lang":  lang,
		"entry": entry,
		"runtime": map[string]any{
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
			"@neuron/sdk": "^0.1.0",
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

	assembly := fmt.Sprintf(`import { Capability, Assembly } from "@neuron/sdk";

const sayHello = Capability({
  name: "hello.say",
  version: "1.0.0",
  description: "Return a friendly greeting",
})
  .capabilityRuntime({ name: "neuron:core:set" })
  .inputSchema<{ name: string }>()
  .outputSchema<{ name: string; message: string }>();

const manifest = Assembly({
  name: %q,
  version: "1.0.0",
  description: "A friendly hello assembly",
})
  .inputSchema<{ name: string }>()
  .withParams((input) => sayHello.withInput({ name: input.name }))
  .toManifest();

export default manifest;
`, name)

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

	assembly := fmt.Sprintf(`apiVersion: neuron/v1
kind: Assembly

metadata:
  name: %s
  version: 1.0.0
  description: A friendly hello assembly

capabilities:
  - ref: say-hello
    entry: capabilities/say-hello.yaml
`, name)

	return createFile(filepath.Join(root, "assembly.yaml"), assembly, 0o644)
}

func printNextSteps(dir string, lang language.Language) {
	fmt.Printf("\nInitialized %s project in %s\n", lang, dir)
	if lang == language.TypeScript {
		fmt.Println("\nNext steps:")
		fmt.Println("  npm install        # install the SDK and toolchain")
		fmt.Println("  neuron build       # build the assembly and register it with N.O.R.E.")
		fmt.Println("  neuron run         # create an instance and watch it execute")
	} else {
		fmt.Println("\nNext steps:")
		fmt.Println("  neuron build       # build the assembly and register it with N.O.R.E.")
		fmt.Println("  neuron run         # create an instance and watch it execute")
	}
}
