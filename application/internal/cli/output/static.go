// Static renderer: permanent, line-oriented output safe for pipes and files.
// No ANSI-control sequences, no cursor movement, no redraws. This is the
// sequential-parse fallback for non-TTY output and the verbose diagnostics
// mode.
package output

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/Muhammad-Jay/neuron/shared/types/protocol"
)

type staticRenderer struct {
	out     io.Writer
	system  string
	verbose bool
	started time.Time
	view    *ExecutionView
}

func (r *staticRenderer) Handle(ctx context.Context, evt protocol.StreamEvent) error {
	if r.view == nil {
		r.view = NewExecutionView(r.system)
	}
	if r.view.Started.IsZero() && evt.OccurredAt > 0 {
		r.view.Started = time.Unix(0, evt.OccurredAt)
	}
	if r.started.IsZero() {
		r.started = time.Now()
	}

	var line strings.Builder
	line.WriteString(fmt.Sprintf("[%s]", time.Now().Format("15:04:05.000")))

	if r.verbose {
		// Verbose: include the payload (unless it is a duplicate of the summary
		// that follows). v helps debugging without a live terminal.
		line.WriteString(" ")
		fmt.Fprintf(&line, "%s", evt.Type)
		if evt.ServiceID != "" {
			fmt.Fprintf(&line, " [%s]", evt.ServiceID)
		}
		if len(evt.Payload) > 0 && evt.Type != "service.log" {
			line.WriteString(" ")
			line.WriteString(string(evt.Payload))
		}
		_, err := fmt.Fprintln(r.out, line.String())
		return err
	}

	kind := Classify(evt)
	switch kind {
	case KindLive:
		line.WriteString(" " + glyphBullet + " ")
		line.WriteString(evt.Type)
		if evt.ServiceID != "" {
			fmt.Fprintf(&line, " [%s]", evt.ServiceID)
		}
	case KindStatic, KindTerminal:
		switch evt.Type {
		case "service.completed":
			line.WriteString(" " + glyphCheck + " ")
			line.WriteString(string(evt.ServiceID))
		case "service.failed":
			line.WriteString(" " + glyphCross + " ")
			line.WriteString(string(evt.ServiceID))
			var p struct {
				Message string `json:"Message"`
			}
			if err := decodePayload(evt.Payload, &p); err == nil && p.Message != "" {
				fmt.Fprintf(&line, "  %s", p.Message)
			}
		case "execution.completed":
			line.WriteString(" " + glyphCheck + " execution completed")
		case "execution.failed":
			line.WriteString(" " + glyphCross + " execution failed")
			var p struct {
				Message string `json:"Message"`
			}
			if err := decodePayload(evt.Payload, &p); err == nil && p.Message != "" {
				fmt.Fprintf(&line, "  %s", p.Message)
			}
		case "execution.cancelled":
			line.WriteString(" " + glyphCircle + " execution cancelled")
		default:
			line.WriteString(" " + glyphBullet + " ")
			line.WriteString(evt.Type)
			if evt.ServiceID != "" {
				fmt.Fprintf(&line, " [%s]", evt.ServiceID)
			}
		}
	}

	// Apply the event to the view, then render any output payload for a
	// completed service on the following lines.
	if err := r.view.Fold(evt); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(r.out, line.String()); err != nil {
		return err
	}
	if evt.Type == "service.completed" {
		if sv, ok := r.view.byID[string(evt.ServiceID)]; ok && len(sv.Output) > 0 {
			var b strings.Builder
			dataWriter(&b, "      ", r.system+" output", sv.Output)
			_, err := io.WriteString(r.out, b.String())
			return err
		}
	}
	return nil
}

func (r *staticRenderer) Close() error {
	ms := time.Since(r.started).Milliseconds()
	_, err := fmt.Fprintf(r.out, "Completed in %dms\n", ms)
	return err
}

func decodePayload(raw []byte, v any) error {
	return json.Unmarshal(raw, v)
}
