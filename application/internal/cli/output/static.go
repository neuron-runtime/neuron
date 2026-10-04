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

	"github.com/neuron-runtime/neuron/shared/types/protocol"
)

type staticRenderer struct {
	out      io.Writer
	assembly string
	verbose  bool
	started  time.Time
	view     *ExecutionView
}

func (r *staticRenderer) Handle(ctx context.Context, evt protocol.StreamEvent) error {
	if r.view == nil {
		r.view = NewExecutionView(r.assembly)
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
		if evt.CapabilityID != "" {
			fmt.Fprintf(&line, " [%s]", evt.CapabilityID)
		}
		if len(evt.Payload) > 0 && evt.Type != "capability.log" {
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
		if evt.CapabilityID != "" {
			fmt.Fprintf(&line, " [%s]", evt.CapabilityID)
		}
		if evt.Type == "capability.retry" {
			var p struct {
				Attempt     int    `json:"Attempt"`
				NextAttempt int    `json:"NextAttempt"`
				Delay       string `json:"Delay"`
			}
			if err := decodePayload(evt.Payload, &p); err == nil {
				fmt.Fprintf(&line, "  attempt %d failed, retrying (#%d) in %s", p.Attempt, p.NextAttempt, p.Delay)
			}
		}
	case KindStatic, KindTerminal:
		switch evt.Type {
		case "capability.completed":
			line.WriteString(" " + glyphCheck + " ")
			line.WriteString(string(evt.CapabilityID))
		case "capability.detached":
			line.WriteString(" " + glyphDetached + " ")
			line.WriteString(string(evt.CapabilityID))
			line.WriteString("  detached")
		case "capability.failed":
			line.WriteString(" " + glyphCross + " ")
			line.WriteString(string(evt.CapabilityID))
			var p struct {
				Message string `json:"Message"`
			}
			if err := decodePayload(evt.Payload, &p); err == nil && p.Message != "" {
				fmt.Fprintf(&line, "  %s", p.Message)
			}
		case "capability.cancelled":
			// The capability did not fail; something else ended it. The message
			// says what, so the line is never a bare unexplained stop.
			line.WriteString(" " + glyphCancelled + " ")
			line.WriteString(string(evt.CapabilityID))
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
			if evt.CapabilityID != "" {
				fmt.Fprintf(&line, " [%s]", evt.CapabilityID)
			}
		}
	}

	// Apply the event to the view, then render any output payload for a
	// completed capability on the following lines.
	if err := r.view.Fold(evt); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(r.out, line.String()); err != nil {
		return err
	}
	if evt.Type == "capability.completed" {
		if sv, ok := r.view.byID[string(evt.CapabilityID)]; ok && len(sv.Output) > 0 {
			var b strings.Builder
			dataWriter(&b, "      ", r.assembly+" output", sv.Output)
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
