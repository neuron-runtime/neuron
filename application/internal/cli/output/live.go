// Live renderer: a redrawn terminal region. The execution view is re-rendered
// in place as events arrive (uilive tracks and clears the previous lines).
// Service completions are folded into the same region rather than printed
// permanently, so the final view is stable.
package output

import (
	"context"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/Muhammad-Jay/neuron/shared/types/protocol"
	"github.com/gosuri/uilive"
)

const (
	spinnerInterval = 90 * time.Millisecond
	liveRefresh     = 100 * time.Millisecond
)

type liveRenderer struct {
	mu      sync.Mutex
	ul      *uilive.Writer
	view    *ExecutionView
	frame   int
	closed  bool
	spin    bool
	stop    chan struct{}
	stopped chan struct{}
}

func newLiveRenderer(opts Options) *liveRenderer {
	ul := uilive.New()
	ul.Out = opts.Out
	ul.RefreshInterval = liveRefresh
	return &liveRenderer{
		ul:      ul,
		view:    NewExecutionView(opts.System),
		stop:    make(chan struct{}),
		stopped: make(chan struct{}),
	}
}

func (r *liveRenderer) Handle(ctx context.Context, evt protocol.StreamEvent) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return nil
	}
	if !r.spin {
		r.spin = true
		go r.animate()
	}
	_ = r.view.Fold(evt)
	return r.draw()
}

// draw renders the current view and flushes the live region. Callers hold r.mu.
func (r *liveRenderer) draw() error {
	if _, err := io.WriteString(r.ul, r.render()); err != nil {
		return err
	}
	return r.ul.Flush()
}

func (r *liveRenderer) spinner() string {
	return spinnerFrames[r.frame%len(spinnerFrames)]
}

func (r *liveRenderer) render() string {
	var b strings.Builder
	b.WriteString(r.view.System)
	b.WriteString("\n")
	b.WriteString(strings.Repeat("\u2500", ruleWidth))
	b.WriteString("\n")

	if len(r.view.Services) == 0 {
		fmt.Fprintf(&b, "\n  %s starting\n", r.spinner())
	}
	for _, sv := range r.view.Services {
		switch sv.State {
		case ServiceCompleted:
			fmt.Fprintf(&b, "\n  %s %s\n", glyphCheck, sv.ID)
			if len(sv.Output) > 0 {
				var ob strings.Builder
				dataWriter(&ob, "      ", "output", sv.Output)
				b.WriteString(ob.String())
			}
		case ServiceFailed:
			fmt.Fprintf(&b, "\n  %s %s  %s\n", glyphCross, sv.ID, sv.Message)
		case ServiceRunning:
			fmt.Fprintf(&b, "\n  %s %s\n", r.spinner(), sv.ID)
		default:
			fmt.Fprintf(&b, "\n  %s %s\n", glyphCircle, sv.ID)
		}
	}

	b.WriteString("\n")
	r.renderFooter(&b)
	return b.String()
}

func (r *liveRenderer) renderFooter(b *strings.Builder) {
	switch r.view.Status {
	case StatusCompleted:
		fmt.Fprintf(b, "  %s Completed in %s\n", glyphCheck, formatDuration(time.Since(r.view.Started)))
	case StatusFailed:
		fmt.Fprintf(b, "  %s Failed", glyphCross)
		if r.view.Message != "" {
			fmt.Fprintf(b, ": %s", r.view.Message)
		}
		b.WriteString("\n")
	case StatusCancelled:
		fmt.Fprintf(b, "  %s Cancelled\n", glyphCircle)
	default:
		fmt.Fprintf(b, "  %s Running  %s\n", r.spinner(), formatDuration(time.Since(r.view.Started)))
	}
}

// animate advances the spinner frame while the execution is in flight. It
// stops when Close signals the stop channel.
func (r *liveRenderer) animate() {
	ticker := time.NewTicker(spinnerInterval)
	defer ticker.Stop()
	defer close(r.stopped)
	for {
		select {
		case <-r.stop:
			return
		case <-ticker.C:
			r.mu.Lock()
			if !r.closed {
				r.frame++
				_ = r.draw()
			}
			r.mu.Unlock()
		}
	}
}

func (r *liveRenderer) Close() error {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return nil
	}
	r.closed = true
	r.frame = 0
	err := r.draw()
	if r.spin {
		close(r.stop)
	}
	r.mu.Unlock()
	if r.spin {
		<-r.stopped
	}
	return err
}
