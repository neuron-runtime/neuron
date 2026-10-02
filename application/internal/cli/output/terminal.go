// Package output renders execution events for `neuron run`. It is the only
// layer that knows about live terminal regions, spinners, ANSI control
// sequences, and the three presentation modes (live TTY, plain lines, NDJSON).
//
// N.O.R.E. emits structured events; this package decides what humans and
// machines see. It never decides whether an execution finished — it renders
// the view described by the events it receives and stops when the caller tells
// it the stream reached a terminal event.
package output

import (
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/mattn/go-isatty"
)

// glyphs used by the renderers. They match the visual language used
// everywhere in the CLI: · in-flight, ✓ completed, ✗ failed, ○ waiting,
// ↳ handed off to a separate execution.
const (
	glyphBullet   = "\u00b7"
	glyphCheck    = "\u2713"
	glyphCross    = "\u2717"
	glyphCircle   = "\u25cb"
	glyphDetached = "\u21b3"
)

// spinnerFrames is the braille animation shown next to running capabilities on a
// live terminal. The renderer advances the frame while a capability is in flight.
var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// ruleWidth is the width of the separator rule under the assembly header.
const ruleWidth = 32

// isTerminal reports whether out is a real interactive terminal. Piped and
// file output must use the plain line renderer, which never emits control
// sequences.
func isTerminal(out io.Writer) bool {
	f, ok := out.(*os.File)
	if !ok {
		return false
	}
	return isatty.IsTerminal(f.Fd()) || isatty.IsCygwinTerminal(f.Fd())
}

// dataWriter renders a map of capability output as indented "key: value" lines.
// It recursively renders nested maps, arrays, and multi-line strings so the
// renderer can show real execution data, not just lifecycle names.
func dataWriter(b *strings.Builder, indent string, key string, val any) {
	switch v := val.(type) {
	case map[string]any:
		if len(v) == 0 {
			fmt.Fprintf(b, "%s%s: {}\n", indent, key)
			return
		}
		fmt.Fprintf(b, "%s%s:\n", indent, key)
		keys := sortedKeys(v)
		for _, k := range keys {
			dataWriter(b, indent+"  ", k, v[k])
		}
	case []any:
		if len(v) == 0 {
			fmt.Fprintf(b, "%s%s: []\n", indent, key)
			return
		}
		fmt.Fprintf(b, "%s%s:\n", indent, key)
		for i, item := range v {
			dataWriter(b, indent+"  ", fmt.Sprintf("[%d]", i), item)
		}
	case string:
		if strings.Contains(v, "\n") {
			fmt.Fprintf(b, "%s%s: |\n", indent, key)
			for _, line := range strings.Split(strings.TrimRight(v, "\n"), "\n") {
				fmt.Fprintf(b, "%s  %s\n", indent, line)
			}
			return
		}
		fmt.Fprintf(b, "%s%s: %s\n", indent, key, v)
	case nil:
		fmt.Fprintf(b, "%s%s: null\n", indent, key)
	default:
		fmt.Fprintf(b, "%s%s: %v\n", indent, key, v)
	}
}

// formatDuration renders a duration for the execution footer.
func formatDuration(d time.Duration) string {
	switch {
	case d < 0:
		return "n/a"
	case d < time.Second:
		return fmt.Sprintf("%dms", d.Milliseconds())
	case d < time.Minute:
		return fmt.Sprintf("%.2fs", d.Seconds())
	default:
		return fmt.Sprintf("%dm%02ds", int(d.Minutes()), int(d.Seconds())%60)
	}
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
