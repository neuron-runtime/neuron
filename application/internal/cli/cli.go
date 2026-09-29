package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/neuron-runtime/neuron/application/config"
	"github.com/neuron-runtime/neuron/application/internal/cli/build"
	"github.com/neuron-runtime/neuron/application/internal/cli/capabilityruntime"
	"github.com/neuron-runtime/neuron/application/internal/cli/command"
	"github.com/neuron-runtime/neuron/application/internal/cli/daemon"
	"github.com/neuron-runtime/neuron/application/internal/cli/initcmd"
	"github.com/neuron-runtime/neuron/application/internal/cli/instance"
	"github.com/neuron-runtime/neuron/application/internal/cli/register"
	"github.com/neuron-runtime/neuron/application/internal/cli/run"
	versioncmd "github.com/neuron-runtime/neuron/application/internal/cli/version"
	buildversion "github.com/neuron-runtime/neuron/shared/version"
	"github.com/spf13/cobra"
)

var cfgFile string

var RootCmd = &cobra.Command{
	Use:     command.Neuron,
	Short:   "Neuron CLI",
	Version: buildversion.String(),
	RunE: func(cmd *cobra.Command, args []string) error {
		return cmd.Help()
	},
}

// Execute is called by main.go to start the CLI.
func Execute() error {
	// Errors bubble up to main.go so there is exactly one, uniformly styled
	// error surface. SilenceUsage keeps spurious usage dumps out of error
	// output (each command owns its usage via cmd.Help()).
	RootCmd.SilenceErrors = true
	RootCmd.SilenceUsage = true
	return RootCmd.Execute()
}

func init() {
	RootCmd.PersistentFlags().StringVar(&cfgFile, "config", "", "project config file (defaults to neuron.config.json, then neuron.config.yaml/yml)")
	RootCmd.PersistentFlags().String("log-level", "info", "Set the assemblies logging level")
	RootCmd.PersistentFlags().BoolP("verbose", "v", false, "Enable verbose output (shows N.O.R.E. daemon logs)")
	RootCmd.PersistentFlags().String("remote", "", "Remote N.O.R.E. endpoint (e.g., https://api.nore.example.com)")
	RootCmd.PersistentFlags().String("nore-path", "", "Path to the nore daemon binary")
	RootCmd.PersistentFlags().Bool("force", false, "Force replacement (clear existing state before the command acts)")

	// Load the effective configuration once flags are parsed and inject it on
	// the command context so every subcommand can read it without touching
	// Viper itself.
	RootCmd.PersistentPreRunE = func(cmd *cobra.Command, args []string) error {
		cfg, err := loadConfig(cmd)
		if err != nil {
			return err
		}
		cmd.SetContext(config.NewContext(cmd.Context(), cfg))
		return nil
	}

	RootCmd.AddCommand(
		run.New(),
		instance.New(),
		daemon.New(),
		initcmd.New(),
		build.New(),
		register.New(),
		capabilityruntime.New(),
		capabilityruntime.NewAddCmd(),
		capabilityruntime.NewRemoveCmd(),
		versioncmd.New(),
	)
}

// loadConfig assembles the effective configuration from the resolved project
// directory and the parsed command-line overrides.
func loadConfig(cmd *cobra.Command) (config.Config, error) {
	projectDir, err := os.Getwd()
	if err != nil {
		return config.Config{}, fmt.Errorf("get current directory: %w", err)
	}

	// Subcommands may redirect the project root with --root. The config is
	// then resolved from that directory so it follows the project root.
	if root, err := cmd.Flags().GetString("root"); err == nil && root != "" {
		if filepath.IsAbs(root) {
			projectDir = root
		} else {
			projectDir = filepath.Join(projectDir, root)
		}
		projectDir = filepath.Clean(projectDir)
	}

	cli := map[string]any{}

	if remote, _ := cmd.Flags().GetString("remote"); remote != "" {
		cli["daemon.endpoint"] = remote
	}
	if path, _ := cmd.Flags().GetString("nore-path"); path != "" {
		cli["daemon.norePath"] = path
	}

	opts := config.Options{
		ProjectDir:  projectDir,
		Environment: true,
		CLI:         cli,
	}

	if cfgFile != "" {
		opts.ProjectPath = cfgFile
		opts.ProjectDir = filepath.Dir(cfgFile)
	}

	return config.Load(opts)
}
