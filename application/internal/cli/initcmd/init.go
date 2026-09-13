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

// implicitExecutorRoot is the canonical project-scoped executor directory a
// scaffold advertises via executors.localRoots. The executor catalog always
// scans it, so executors placed there resolve without extra configuration.
const implicitExecutorRoot = "neuron/executors"

func New() *cobra.Command {
	cmd := &cobra.Command{
		Use:   command.Init,
		Short: "Scaffold a new neuron project",
		Long: "Create a runnable Neuron project in the given directory (or the " +
			"current one). The project is scaffolded only: install dependencies " +
			"and run `neuron register && neuron run` to see it execute.",
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

	if err := ensureImplicitExecutorRoot(abs); err != nil {
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
// languages. It declares the authoring language, the System entry file, and
// the canonical local executor root. Everything the runtime needs beyond that
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
		"executors": map[string]any{
			// The project-scoped executor root. Executors placed here are
			// resolved automatically; no registry entry is required.
			"localRoots": []string{"./" + implicitExecutorRoot},
		},
		"inspector": map[string]any{
			"enabled": true,
			"address": "127.0.0.1:7433",
		},
	}
}

// ensureImplicitExecutorRoot keeps the canonical ./neuron/executors directory
// present so locally-authored executors have an obvious home from day one. The
// .gitkeep marker keeps the empty folder tracked; it is only written when the
// directory is created fresh.
func ensureImplicitExecutorRoot(root string) error {
	dir := filepath.Join(root, implicitExecutorRoot)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create %s: %w", implicitExecutorRoot, err)
	}
	marker := filepath.Join(dir, ".gitkeep")
	if _, err := os.Stat(marker); os.IsNotExist(err) {
		if err := os.WriteFile(marker, nil, 0o644); err != nil {
			return fmt.Errorf("create %s/.gitkeep: %w", implicitExecutorRoot, err)
		}
	}
	return nil
}

func scaffoldTypeScript(root, name string) error {
	if err := writeJSON(filepath.Join(root, "neuron.config.json"), configTemplate(name, "typescript", "system.ts")); err != nil {
		return err
	}

	pkg := map[string]any{
		"name":    name,
		"version": "0.1.0",
		"private": true,
		"type":    "module",
		"scripts": map[string]any{
			"start":        "neuron run",
			"register":     "neuron register",
			"typecheck":    "tsc --noEmit",
			"executor:add": "neuron add",
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
  "include": ["system.ts"]
}
`
	if err := createFile(filepath.Join(root, "tsconfig.json"), tsconfig, 0o644); err != nil {
		return err
	}

	system := fmt.Sprintf(`import { Service, System } from "@neuron/sdk";

const sayHello = Service({
  name: "hello.say",
  version: "1.0.0",
  description: "Return a friendly greeting",
})
  .executor({ name: "neuron:core:set" })
  .inputSchema<{ name: string }>()
  .outputSchema<{ name: string; message: string }>();

const manifest = System({
  name: %q,
  version: "1.0.0",
  description: "A friendly hello system",
})
  .inputSchema<{ name: string }>()
  .withParams((input) => sayHello.withInput({ name: input.name }))
  .toManifest();

export default manifest;
`, name)

	return createFile(filepath.Join(root, "system.ts"), system, 0o644)
}

func scaffoldYAML(root, name string) error {
	if err := writeJSON(filepath.Join(root, "neuron.config.json"), configTemplate(name, "yaml", "system.yaml")); err != nil {
		return err
	}

	servicesDir := filepath.Join(root, "services")
	if err := os.MkdirAll(servicesDir, 0o755); err != nil {
		return fmt.Errorf("create services directory: %w", err)
	}

	service := `apiVersion: neuron/v1
kind: Service

metadata:
  name: say-hello
  version: 1.0.0
  description: Return a friendly greeting

spec:
  executor:
    type: neuron:core:set
`
	if err := createFile(filepath.Join(servicesDir, "say-hello.yaml"), service, 0o644); err != nil {
		return err
	}

	system := fmt.Sprintf(`apiVersion: neuron/v1
kind: System

metadata:
  name: %s
  version: 1.0.0
  description: A friendly hello system

services:
  - ref: say-hello
    entry: services/say-hello.yaml
`, name)

	return createFile(filepath.Join(root, "system.yaml"), system, 0o644)
}

func printNextSteps(dir string, lang language.Language) {
	fmt.Printf("\nInitialized %s project in %s\n", lang, dir)
	if lang == language.TypeScript {
		fmt.Println("\nNext steps:")
		fmt.Println("  npm install        # install the SDK and toolchain")
		fmt.Println("  neuron register    # build the system and register it with N.O.R.E.")
		fmt.Println("  neuron run         # create an instance and watch it execute")
	} else {
		fmt.Println("\nNext steps:")
		fmt.Println("  neuron register    # build the system and register it with N.O.R.E.")
		fmt.Println("  neuron run         # create an instance and watch it execute")
	}
}
