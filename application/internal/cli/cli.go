package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/neuron-runtime/neuron/application/config"
	"github.com/neuron-runtime/neuron/application/internal/cli/bootstrap"
	"github.com/neuron-runtime/neuron/application/internal/cli/build"
	"github.com/neuron-runtime/neuron/application/internal/cli/capabilityruntime"
	"github.com/neuron-runtime/neuron/application/internal/cli/command"
	"github.com/neuron-runtime/neuron/application/internal/cli/daemon"
	"github.com/neuron-runtime/neuron/application/internal/cli/initcmd"
	installcmd "github.com/neuron-runtime/neuron/application/internal/cli/installcmd"
	"github.com/neuron-runtime/neuron/application/internal/cli/newcmd"
	providercmd "github.com/neuron-runtime/neuron/application/internal/cli/provider"
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
	RunE:    runProjectRuntime,
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
	// Viper itself. Direct Capability Runtime invocation is deliberately
	// project-independent and must not load neuron.config.*, so it opts out.
	RootCmd.PersistentPreRunE = func(cmd *cobra.Command, args []string) error {
		if run.DirectMode(cmd) {
			return nil
		}
		cfg, err := loadConfig(cmd)
		if err != nil {
			return err
		}
		cmd.SetContext(config.NewContext(cmd.Context(), cfg))
		return nil
	}

	RootCmd.AddCommand(
		run.New(),
		daemon.New(),
		initcmd.New(),
		build.New(),
		register.New(),
		capabilityruntime.New(),
		capabilityruntime.NewAddCmd(),
		capabilityruntime.NewRemoveCmd(),
		newcmd.New(),
		versioncmd.New(),
		installcmd.New(),
		providercmd.New(),
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

// runProjectRuntime is the entry point for bare `neuron` command.
// It starts the project runtime in the foreground (like `npm run dev`).
func runProjectRuntime(cmd *cobra.Command, args []string) error {
	// Load config to get project root
	cfg, err := loadConfig(cmd)
	if err != nil {
		return err
	}

	// Check if we're in a project directory with neuron.config.*
	if cfg.ProjectDir == "" {
		return fmt.Errorf("no Neuron project found in current directory or ancestors; run `neuron init` first")
	}

	// Build the nore command
	noreBin, err := bootstrap.NoreBinaryPath(cfg)
	if err != nil {
		return fmt.Errorf("find nore binary: %w", err)
	}

	// Prepare arguments for nore --project
	noreArgs := []string{
		"--project",
		"--project-root", cfg.ProjectDir,
		"--host", cfg.Runtime.Host,
	}

	// Add port if configured
	if !cfg.Runtime.Port.IsAuto() {
		noreArgs = append(noreArgs, "--port", fmt.Sprintf("%d", cfg.Runtime.Port.Number))
	}

	// Add capability runtime store if configured
	if store := cfg.CapabilityRuntimes.StoreDir; store != "" {
		noreArgs = append(noreArgs, "--capability-runtime-store", store)
	}

	// Add detached drain timeout if configured
	if cfg.Runtime.DetachedDrainTimeout != "" {
		noreArgs = append(noreArgs, "--detached-drain-timeout", cfg.Runtime.DetachedDrainTimeout)
	}

	// Add workers
	noreArgs = append(noreArgs, "--workers", fmt.Sprintf("%d", cfg.Runtime.Workers.Max))

	// Add data dir
	if cfg.Storage.Directory != "" {
		noreArgs = append(noreArgs, "--data-dir", cfg.Storage.Directory)
	}

	// Execute nore with the project's stdout/stderr so the user sees the logs
	noreCmd := exec.CommandContext(cmd.Context(), noreBin, noreArgs...)
	noreCmd.Stdout = cmd.OutOrStdout()
	noreCmd.Stderr = cmd.ErrOrStderr()

	// Forward environment
	noreCmd.Env = os.Environ()

	// Add NEURON_API_TOKEN if set
	if token := os.Getenv("NEURON_API_TOKEN"); token != "" {
		noreCmd.Env = append(noreCmd.Env, "NEURON_API_TOKEN="+token)
	}

	// Run and wait
	if err := noreCmd.Run(); err != nil {
		// Check if it was a signal interrupt
		if exitErr, ok := err.(*exec.ExitError); ok {
			if ws := exitErr.Sys(); ws != nil {
				if signum, ok := ws.(interface{ Signal() os.Signal }); ok {
					if signum.Signal() == os.Interrupt {
						return nil // Clean shutdown on Ctrl-C
					}
				}
			}
		}
		return fmt.Errorf("project runtime stopped: %w", err)
	}

	return nil
}
