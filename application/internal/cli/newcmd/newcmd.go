package newcmd

import (
	"context"
	"embed"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/template"

	"github.com/neuron-runtime/neuron/application/internal/cli/command"
	"github.com/spf13/cobra"
)

//go:embed templates
var templateFS embed.FS

// New returns the `neuron new` command.
func New() *cobra.Command {
	cmd := &cobra.Command{
		Use:   command.New,
		Short: "Create new Neuron projects and capability runtimes",
		Long:  "Create new Neuron projects or capability runtimes from templates.",
	}

	cmd.AddCommand(newCapabilityRuntimeCmd())

	return cmd
}

// newCapabilityRuntimeCmd creates the `neuron new capability-runtime` subcommand.
func newCapabilityRuntimeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "capability-runtime <name>",
		Short: "Generate a new capability runtime project",
		Long: "Generate a new capability runtime project from a template.\n\n" +
			"The generated capability runtime is dependency-free and uses the stdio JSON\n" +
			"protocol (neuron/capability-runtime-v1-json). It can be built with `neuron build`\n" +
			"and executed by the Neuron runtime without any external dependencies.\n\n" +
			"Example:\n" +
			"  neuron new capability-runtime my-runtime --lang go",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			lang, _ := cmd.Flags().GetString("lang")
			dir, _ := cmd.Flags().GetString("dir")
			description, _ := cmd.Flags().GetString("description")
			capabilityName, _ := cmd.Flags().GetString("capability")

			if lang != "go" {
				return fmt.Errorf("unsupported language: %s (only 'go' is supported)", lang)
			}

			if dir == "" {
				dir = "."
			}

			return generateCapabilityRuntime(cmd.Context(), name, dir, description, capabilityName)
		},
	}

	cmd.Flags().StringP("lang", "l", "go", "capability runtime language (go)")
	cmd.Flags().StringP("dir", "d", "", "target directory (default: current)")
	cmd.Flags().String("description", "", "capability runtime description")
	cmd.Flags().String("capability", "", "capability name (defaults to name)")

	return cmd
}

// generateCapabilityRuntime generates a capability runtime from the go template.
func generateCapabilityRuntime(ctx context.Context, name, dir, description, capabilityName string) error {
	if capabilityName == "" {
		capabilityName = name
	}

	// Parse type: split by first colon, owner/name -> type
	// e.g. "example:echo" -> type="example:echo", owner="example", name="echo"
	parts := strings.SplitN(name, ":", 2)
	if len(parts) != 2 {
		return fmt.Errorf("name must be in format 'owner:name' (e.g., 'example:echo')")
	}
	owner := parts[0]
	runtimeName := parts[1]

	// The directory structure is: <dir>/neuron/capabilityRuntimes/<owner>/<runtimeName>/<version>/
	version := "1.0.0"
	targetDir := filepath.Join(dir, "neuron", "capabilityRuntimes", owner, runtimeName, version)

	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		return fmt.Errorf("create target directory: %w", err)
	}

	data := map[string]string{
		"Name":           name,
		"Type":           name,
		"Owner":          owner,
		"RuntimeName":    runtimeName,
		"Version":        version,
		"Description":    description,
		"CapabilityName": capabilityName,
		"Module":         "github.com/" + owner + "/" + runtimeName,
		"BinaryName":     runtimeName,
	}

	// Template files to generate
	files := []struct {
		templatePath string
		outputPath   string
		mode         os.FileMode
	}{
		{"templates/go/main.go.tmpl", filepath.Join(targetDir, runtimeName+".go"), 0o644},
		{"templates/go/go.mod.tmpl", filepath.Join(targetDir, "go.mod"), 0o644},
		{"templates/go/runtime.json.tmpl", filepath.Join(targetDir, "runtime.json"), 0o644},
		{"templates/go/build.sh.tmpl", filepath.Join(targetDir, "build.sh"), 0o755},
	}

	for _, f := range files {
		content, err := templateFS.ReadFile(f.templatePath)
		if err != nil {
			return fmt.Errorf("read template %s: %w", f.templatePath, err)
		}

		tmpl, err := template.New(filepath.Base(f.templatePath)).Parse(string(content))
		if err != nil {
			return fmt.Errorf("parse template %s: %w", f.templatePath, err)
		}

		var buf strings.Builder
		if err := tmpl.Execute(&buf, data); err != nil {
			return fmt.Errorf("execute template %s: %w", f.templatePath, err)
		}

		outputPath := f.outputPath
		if _, err := os.Stat(outputPath); err == nil {
			fmt.Printf("skipping %s (already exists)\n", outputPath)
			continue
		}

		if err := os.WriteFile(outputPath, []byte(buf.String()), f.mode); err != nil {
			return fmt.Errorf("write %s: %w", outputPath, err)
		}
		fmt.Printf("created %s\n", outputPath)
	}

	fmt.Printf("\nGenerated %s capability runtime in %s\n", name, targetDir)
	fmt.Println("\nNext steps:")
	fmt.Printf("  cd %s\n", targetDir)
	fmt.Println("  go build -o " + runtimeName + " .")
	fmt.Println("  neuron build   # build and install the capability runtime")

	return nil
}
