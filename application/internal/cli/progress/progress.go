// Package progress adapts the executor pipeline's Observer events into the
// CLI's step-rendering ui package. It implements executor.Observer so the
// resolution pipeline stays output-free: the executor package reports events,
// and this layer decides how to draw them through ui.Runner.
//
// The package owns the domain meaning of each event (what "resolving",
// "installing", "already installed", and executor build status mean) and hands
// rendering to ui. Its sole remaining job is translating events into runner
// calls.
package progress

import (
	"io"

	"github.com/Muhammad-Jay/neuron/application/executor"
	"github.com/Muhammad-Jay/neuron/application/internal/cli/ui"
)

// Reporter renders executor.Observer events for one command invocation.
//
// A Reporter is not safe for concurrent use beyond the internal spinner
// lifecycle; resolution events are emitted sequentially by the pipeline.
type Reporter struct {
	r *ui.Runner

	// building counts executors currently running a build.command so the
	// concurrent-build display can show an aggregate instead of flickering
	// per-executor lines.
	building int
}

// New returns a Reporter writing to out (stderr for CLI commands).
func New(out io.Writer) *Reporter {
	return &Reporter{r: ui.New(out)}
}

// Resolving implements executor.Observer.
func (r *Reporter) Resolving(res executor.Requirement) {
	r.r.Step("Resolving " + display(res.Type, res.Version))
}

// AlreadyInstalled implements executor.Observer.
func (r *Reporter) AlreadyInstalled(_ executor.Requirement, installed executor.Installed) {
	r.r.StepDone(display(installed.Type, installed.Version) + " already installed")
}

// Installing implements executor.Observer.
func (r *Reporter) Installing(pkg executor.Package) {
	text := "Installing " + display(pkg.Type, pkg.Version)
	if pkg.Registry != "" {
		text += " (from " + pkg.Registry + ")"
	}
	r.r.Step(text)
}

// Installed implements executor.Observer.
func (r *Reporter) Installed(result executor.InstallResult) {
	if result.Installed == nil {
		return
	}
	verb := " installed"
	if result.AlreadyPresent {
		verb = " already installed"
	}
	r.r.StepDone(display(result.Installed.Type, result.Installed.Version) + verb)
}

// Status renders one per-executor build decision (building / cached /
// installed / failed) reported through executorctl.BuildOptions.Status. Build
// commands run concurrently under BuildLocal, so the in-flight "building"
// state drives the aggregate spinner and terminal decisions render completed
// lines.
func (r *Reporter) Status(typ, version, message string) {
	name := display(typ, version)

	switch message {
	case "building":
		r.building++
		r.r.Busy(r.building, "building executors")
		if !r.r.Live() {
			r.r.Step(name + " building")
		}
	default:
		if r.building > 0 {
			r.building--
		}
		r.r.StepDone(name + " " + message)
	}
}

// Live reports whether the runner drives a live spinner (a real terminal).
func (r *Reporter) Live() bool { return r.r.Live() }

// Stop halts any active spinner and restores the cursor. It is safe to call
// when nothing is running (e.g. from a deferred cleanup).
func (r *Reporter) Stop() {
	r.r.Stop()
}

// display renders a name@version pair, omitting the version when empty.
func display(name, version string) string {
	if version == "" {
		return name
	}
	return name + "@" + version
}
