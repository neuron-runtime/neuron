package render

import (
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/neuron-runtime/neuron/shared/types/protocol"
)

// Instances renders a slice of instances as a formatted table to stdout.
func Instances(instances []protocol.InstanceResponse) {
	columns := []Column{
		{Title: "ID"},
		{Title: "Assembly ID"},
		{Title: "Blueprint Name"},
		{Title: "Status"},
		{Title: "Version"},
		//{Title: "Hash"},
		//{Title: "Env"},
	}

	var rows [][]string
	for _, inst := range instances {
		rows = append(rows, []string{
			inst.ID,
			inst.AssemblyID,
			inst.BlueprintMetadata.Name,
			inst.Status,
			inst.Version,
			//inst.Hash,
			//inst.Env,
		})
	}

	if err := RenderTable(os.Stdout, columns, rows, DefaultTableOptions()); err != nil {
		fmt.Fprintf(os.Stderr, "render: %v\n", err)
	}
}

// Executions renders a slice of executions as a formatted table.
func Executions(out io.Writer, items []protocol.ExecutionItem) {
	columns := []Column{
		{Title: "ID"},
		{Title: "Correlation ID"},
		{Title: "Status"},
		{Title: "Started At"},
		{Title: "Completed At"},
		{Title: "Error"},
	}

	var rows [][]string
	for _, item := range items {
		rows = append(rows, []string{
			string(item.ID),
			string(item.CorrelationID),
			item.Status,
			formatExecTime(item.StartedAt),
			formatExecTime(item.CompletedAt),
			item.Error,
		})
	}

	if err := RenderTable(out, columns, rows, DefaultTableOptions()); err != nil {
		fmt.Fprintf(os.Stderr, "render: %v\n", err)
	}
}

// ExecutionWaits renders wait details for executions that are suspended.
// It is a companion to Executions: the table keeps execution identity compact,
// while wait details show why the execution is waiting and what can resume it.
func ExecutionWaits(out io.Writer, items []protocol.ExecutionItem) {
	var rows [][]string
	for _, item := range items {
		for _, wait := range item.Waits {
			rows = append(rows, []string{
				string(item.ID),
				string(wait.CapabilityID),
				fallbackDash(wait.WaitType),
				fallbackDash(wait.ExpectedSignal),
				fallbackDash(wait.CorrelationKey),
				fallbackDash(wait.WaitReason),
				formatExecTime(wait.Deadline),
			})
		}
	}
	if len(rows) == 0 {
		return
	}

	fmt.Fprintln(out)
	fmt.Fprintln(out, "Waits:")
	columns := []Column{
		{Title: "Execution"},
		{Title: "Capability"},
		{Title: "Wait Type"},
		{Title: "Expected Signal"},
		{Title: "Correlation Key"},
		{Title: "Reason"},
		{Title: "Deadline"},
	}
	if err := RenderTable(out, columns, rows, DefaultTableOptions()); err != nil {
		fmt.Fprintf(os.Stderr, "render: %v\n", err)
	}
}

func fallbackDash(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "-"
	}
	return value
}

// formatExecTime converts a Unix nanosecond timestamp into a human-readable
// local time string. A nil timestamp renders as "-".
func formatExecTime(ts *int64) string {
	if ts == nil {
		return "-"
	}
	return time.Unix(0, *ts).Local().Format(time.RFC3339)
}
