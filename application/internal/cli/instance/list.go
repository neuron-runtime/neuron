package instance

import (
	"context"
	"fmt"

	"github.com/Muhammad-Jay/neuron/application/config"
	"github.com/Muhammad-Jay/neuron/application/internal/cli/bootstrap"
	"github.com/Muhammad-Jay/neuron/application/internal/cli/command"
	"github.com/Muhammad-Jay/neuron/application/internal/cli/render"
	"github.com/Muhammad-Jay/neuron/application/internal/cli/utils"
	"github.com/Muhammad-Jay/neuron/shared/types/protocol"
	"github.com/spf13/cobra"
)

func newListCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   command.InstanceList,
		Short: "List instances or executions of a specific instance",
		Args:  cobra.MaximumNArgs(1),
		RunE:  instanceListCmdHandler,
	}

	cmd.Flags().BoolP("all", "a", false, "List all instances including inactive ones")
	cmd.Flags().StringP("status", "s", "", "Filter instances by specific status (e.g., running, stopped)")
	cmd.Flags().StringP("target", "t", "", "List executions of the instance with the given ID")

	cmd.MarkFlagsMutuallyExclusive("all", "status", "target")

	return cmd
}

// instanceListCmdHandler resolves an optional instance ID and dispatches to
// either instance listing (no ID) or execution listing (ID present).
func instanceListCmdHandler(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()

	instanceID, err := resolveInstanceID(cmd, args)
	if err != nil {
		return err
	}

	if instanceID != "" {
		return listInstanceExecutions(ctx, instanceID)
	}

	return listInstances(ctx, cmd)
}

// resolveInstanceID returns the instance ID given either as the first
// positional argument or via the --target flag. Providing both is an error.
// Instance IDs are returned unchanged; a user-facing key (name, name:version,
// name@version, ...) is normalized to its colon-encoded form so the server
// resolves it to the same instance as the ID would.
func resolveInstanceID(cmd *cobra.Command, args []string) (string, error) {
	flagID, _ := cmd.Flags().GetString("target")

	if len(args) > 0 && flagID != "" {
		return "", fmt.Errorf("instance specified both as argument and --target; use only one")
	}

	target := flagID
	if len(args) > 0 {
		target = args[0]
	}
	return utils.NormalizeInstanceTarget(target)
}

// listInstances lists instances, honoring the --all and --status filters.
func listInstances(ctx context.Context, cmd *cobra.Command) error {
	showAll, _ := cmd.Flags().GetBool("all")
	statusFilter, _ := cmd.Flags().GetString("status")

	cfg, ok := config.FromContext(ctx)
	if !ok {
		return fmt.Errorf("configuration not loaded")
	}

	c, cleanup, err := bootstrap.SetupClient(ctx, bootstrap.Options{Config: cfg})
	if err != nil {
		return err
	}
	defer cleanup()

	var query string
	if showAll {
		query = "?all=true"
	} else if statusFilter != "" {
		query = fmt.Sprintf("?status=%s", statusFilter)
	} else {
		query = "?status=running"
	}

	instances, err := c.ListInstances(ctx, query)
	if err != nil {
		return fmt.Errorf("failed to list instances: %w", err)
	}

	if len(instances) == 0 {
		render.Instances([]protocol.InstanceResponse{})
		return nil
	}

	render.Instances(instances)
	return nil
}

// listInstanceExecutions lists all executions recorded for the given instance.
func listInstanceExecutions(ctx context.Context, instanceID string) error {
	cfg, ok := config.FromContext(ctx)
	if !ok {
		return fmt.Errorf("configuration not loaded")
	}

	c, cleanup, err := bootstrap.SetupClient(ctx, bootstrap.Options{Config: cfg})
	if err != nil {
		return err
	}
	defer cleanup()

	executions, err := c.ListExecutions(ctx, instanceID)
	if err != nil {
		return fmt.Errorf("failed to list executions for instance %s: %w", instanceID, err)
	}

	if len(executions) == 0 {
		fmt.Println("No executions found for instance", instanceID)
		return nil
	}

	render.Executions(executions)
	return nil
}
