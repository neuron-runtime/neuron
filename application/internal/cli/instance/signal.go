package instance

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/neuron-runtime/neuron/application/connection"
	"github.com/neuron-runtime/neuron/application/internal/cli/command"
	"github.com/neuron-runtime/neuron/shared/types/protocol"
	"github.com/spf13/cobra"
)

func newSignalCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   command.InstanceSignal,
		Short: "Deliver a signal to a waiting execution",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()

			executionID := strings.TrimSpace(args[0])
			if executionID == "" {
				return fmt.Errorf("execution ID is required")
			}
			signalName := strings.TrimSpace(args[1])
			if signalName == "" {
				return fmt.Errorf("signal name is required")
			}

			deliveryID, err := cmd.Flags().GetString("delivery-id")
			if err != nil {
				return err
			}
			deliveryID = strings.TrimSpace(deliveryID)
			if deliveryID == "" {
				return fmt.Errorf("--delivery-id is required")
			}

			correlationKey, err := cmd.Flags().GetString("correlation-key")
			if err != nil {
				return err
			}

			payload, err := loadSignalPayload(cmd)
			if err != nil {
				return err
			}

			c, cleanup, err := setup(ctx)
			if err != nil {
				return err
			}
			defer cleanup()

			resp, err := c.SignalExecution(ctx, executionID, signalName, protocol.SignalDeliveryRequest{
				DeliveryID:     deliveryID,
				Payload:        payload,
				CorrelationKey: correlationKey,
			})
			if err != nil {
				return formatSignalError(executionID, signalName, err)
			}

			state := "accepted"
			if resp.Replay {
				state = "replayed"
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s signal %q for execution %s (delivery=%s)\n", state, resp.SignalName, executionID, resp.DeliveryID)
			return nil
		},
	}

	cmd.Flags().String("delivery-id", "", "idempotent delivery identifier (required)")
	cmd.Flags().String("payload", "", "JSON object payload for the signal")
	cmd.Flags().String("payload-file", "", "path to a JSON file containing the signal payload object")
	cmd.Flags().String("correlation-key", "", "correlation key to target a specific wait when multiple waits match")
	_ = cmd.MarkFlagRequired("delivery-id")
	cmd.MarkFlagsMutuallyExclusive("payload", "payload-file")

	return cmd
}

func loadSignalPayload(cmd *cobra.Command) (map[string]any, error) {
	payloadJSON, err := cmd.Flags().GetString("payload")
	if err != nil {
		return nil, err
	}
	payloadFile, err := cmd.Flags().GetString("payload-file")
	if err != nil {
		return nil, err
	}

	raw := strings.TrimSpace(payloadJSON)
	if payloadFile != "" {
		data, err := os.ReadFile(payloadFile)
		if err != nil {
			return nil, fmt.Errorf("read --payload-file: %w", err)
		}
		raw = strings.TrimSpace(string(data))
	}
	if raw == "" {
		return map[string]any{}, nil
	}

	var payload map[string]any
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return nil, fmt.Errorf("signal payload must be a JSON object: %w", err)
	}
	if payload == nil {
		return map[string]any{}, nil
	}
	return payload, nil
}

func formatSignalError(executionID, signalName string, err error) error {
	var statusErr *connection.StatusError
	if errors.As(err, &statusErr) {
		detail := statusErr.Message
		if detail == "" {
			detail = err.Error()
		}

		switch statusErr.ResponseCode {
		case protocol.SignalCodeConflict:
			return fmt.Errorf("signal delivery conflicts with an existing delivery for execution %s: %s", executionID, detail)
		case protocol.SignalCodeAmbiguousWait:
			return fmt.Errorf("signal %q matches multiple waits for execution %s; provide --correlation-key: %s", signalName, executionID, detail)
		case protocol.SignalCodeNoMatchingWait:
			return fmt.Errorf("no waiting capability in execution %s matches signal %q: %s", executionID, signalName, detail)
		case protocol.SignalCodeExpired:
			return fmt.Errorf("signal %q cannot be delivered to execution %s because the wait expired or the execution is no longer waiting: %s", signalName, executionID, detail)
		case protocol.SignalCodeNotFound:
			return fmt.Errorf("execution %s was not found: %s", executionID, detail)
		case protocol.SignalCodeInvalidRequest:
			return fmt.Errorf("invalid signal request for execution %s: %s", executionID, detail)
		case protocol.SignalCodeInvalidPayload:
			return fmt.Errorf("invalid signal payload for execution %s: %s", executionID, detail)
		case protocol.SignalCodeUnavailable:
			return fmt.Errorf("signal delivery is unavailable for execution %s: %s", executionID, detail)
		}

		if detail != "" {
			return fmt.Errorf("deliver signal %q to execution %s: %s", signalName, executionID, detail)
		}
	}

	return fmt.Errorf("deliver signal %q to execution %s: %w", signalName, executionID, err)
}
