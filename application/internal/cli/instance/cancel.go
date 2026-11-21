package instance

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/neuron-runtime/neuron/application/connection"
	"github.com/neuron-runtime/neuron/application/internal/cli/command"
	"github.com/spf13/cobra"
)

func newCancelCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   command.InstanceCancel,
		Short: "Cancel a waiting or running execution",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()

			instanceTarget, err := resolveInstanceID(cmd, []string{args[0]})
			if err != nil {
				return err
			}
			executionID := strings.TrimSpace(args[1])
			if executionID == "" {
				return fmt.Errorf("execution ID is required")
			}

			reason, err := cmd.Flags().GetString("reason")
			if err != nil {
				return err
			}

			c, cleanup, err := setup(ctx)
			if err != nil {
				return err
			}
			defer cleanup()

			item, err := c.CancelExecution(ctx, instanceTarget, executionID, reason)
			if err != nil {
				return formatCancelError(instanceTarget, executionID, err)
			}

			fmt.Fprintf(cmd.OutOrStdout(), "cancelled execution %s (status=%s)\n", item.ID, item.Status)
			return nil
		},
	}
	cmd.Flags().String("reason", "", "reason recorded with the cancellation")
	return cmd
}

func formatCancelError(instanceID, executionID string, err error) error {
	var statusErr *connection.StatusError
	if errors.As(err, &statusErr) {
		switch statusErr.Code {
		case http.StatusNotFound:
			return fmt.Errorf("execution %s was not found in instance %s: %s", executionID, instanceID, statusErr.Message)
		case http.StatusConflict:
			return fmt.Errorf("execution %s cannot be cancelled: %s", executionID, statusErr.Message)
		default:
			if statusErr.Message != "" {
				return fmt.Errorf("cancel execution %s: %s", executionID, statusErr.Message)
			}
		}
	}
	return fmt.Errorf("cancel execution %s: %w", executionID, err)
}
