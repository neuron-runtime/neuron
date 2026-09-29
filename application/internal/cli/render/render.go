package render

import (
	"fmt"
	"os"
	"time"

	"github.com/neuron-runtime/neuron/shared/types/protocol"
	"github.com/neuron-runtime/neuron/shared/utils"
)

// Instances renders a slice of instances as a formatted table to stdout.
func Instances(instances []protocol.InstanceResponse) {
	columns := []utils.Column{
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

	if err := utils.RenderTable(os.Stdout, columns, rows, utils.DefaultTableOptions()); err != nil {
		fmt.Printf("failed to render table: %v\n", err)
	}
}

// Executions renders a slice of executions as a formatted table to stdout.
func Executions(items []protocol.ExecutionItem) {
	columns := []utils.Column{
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

	if err := utils.RenderTable(os.Stdout, columns, rows, utils.DefaultTableOptions()); err != nil {
		fmt.Printf("failed to render table: %v\n", err)
	}
}

// formatExecTime converts a Unix nanosecond timestamp into a human-readable
// local time string. A nil timestamp renders as "-".
func formatExecTime(ts *int64) string {
	if ts == nil {
		return "-"
	}
	return time.Unix(0, *ts).Local().Format(time.RFC3339)
}
