// Renderer is the presentation boundary for `neuron run` execution streams.
// A renderer consumes fully-formed protocol events and presents them in its
// chosen mode (live terminal, plain lines, NDJSON). The renderer NEVER decides
// when an execution finished; it is closed by the caller once a terminal event
// (or an unrecoverable error) ends the stream.
package output

import (
	"context"
	"errors"
	"io"

	"github.com/neuron-runtime/neuron/shared/types/protocol"
)

// Mode selects the presentation strategy.
type Mode int

const (
	// ModeAuto picks live on a TTY and static when piped.
	ModeAuto Mode = iota
	// ModeLive forces the live terminal region.
	ModeLive
	// ModeStatic forces plain permanent lines.
	ModeStatic
	// ModeJSON emits canonical events as NDJSON.
	ModeJSON
	// ModeVerbose emits timestamped diagnostic lines with payloads.
	ModeVerbose
)

// Options configures a renderer.
type Options struct {
	// Out is where rendered output is written.
	Out io.Writer
	// Assembly is the assembly name, shown in the header.
	Assembly string
	// Mode selects the presentation strategy (ModeAuto by default).
	Mode Mode
	// Verbose renders capabilities.log events and detailed payloads.
	Verbose bool
}

// Renderer presents an execution event stream.
type Renderer interface {
	// Handle presents a single event.
	Handle(ctx context.Context, evt protocol.StreamEvent) error
	// Close flushes any pending state and resets the terminal, if any.
	Close() error
}

// ErrNoOutput is returned when a renderer cannot render (nil writer).
var ErrNoOutput = errors.New("output: nil writer")

// New builds the renderer selected by opts.Mode. ModeAuto resolves to live
// when opts.Out is a terminal and static otherwise.
func New(opts Options) (Renderer, error) {
	if opts.Out == nil {
		return nil, ErrNoOutput
	}
	mode := opts.Mode
	if mode == ModeAuto {
		if isTerminal(opts.Out) {
			mode = ModeLive
		} else {
			mode = ModeStatic
		}
	}
	switch mode {
	case ModeJSON:
		return &jsonRenderer{enc: newJSONEncoder(opts.Out)}, nil
	case ModeVerbose:
		return &staticRenderer{out: opts.Out, assembly: opts.Assembly, verbose: true}, nil
	case ModeLive:
		return newLiveRenderer(opts), nil
	default:
		return &staticRenderer{out: opts.Out, assembly: opts.Assembly, verbose: opts.Verbose}, nil
	}
}
