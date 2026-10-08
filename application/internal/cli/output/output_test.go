package output

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/neuron-runtime/neuron/shared/types/core"
	"github.com/neuron-runtime/neuron/shared/types/protocol"
)

func evt(typ, capabilityID string, payload string) protocol.StreamEvent {
	return protocol.StreamEvent{
		Type:         typ,
		CapabilityID: core.ID(capabilityID),
		OccurredAt:   time.Now().UnixNano(),
		Payload:      json.RawMessage(payload),
	}
}

func TestClassify(t *testing.T) {
	cases := map[string]Kind{
		"execution.started":    KindLive,
		"capability.ready":     KindLive,
		"capability.started":   KindLive,
		"capability.log":       KindLive,
		"capability.completed": KindStatic,
		"capability.failed":    KindStatic,
		"capability.cancelled": KindStatic,
		"execution.completed":  KindTerminal,
		"execution.failed":     KindTerminal,
		"execution.cancelled":  KindTerminal,
	}
	for typ, want := range cases {
		if got := Classify(evt(typ, "", "")); got != want {
			t.Errorf("Classify(%s) = %v, want %v", typ, got, want)
		}
		if got := IsTerminalEvent(evt(typ, "", "")); got != (want == KindTerminal) {
			t.Errorf("IsTerminalEvent(%s) = %v", typ, got)
		}
	}
}

func TestModelFold(t *testing.T) {
	v := NewExecutionView("hello")
	_ = v.Fold(evt("capability.ready", "hello.say", ""))
	_ = v.Fold(evt("capability.started", "hello.say", ""))
	if v.Status != StatusRunning {
		t.Fatalf("status = %v, want running", v.Status)
	}
	_ = v.Fold(evt("capability.completed", "hello.say", `{"Result":{"name":"neuron"}}`))
	if len(v.Capabilities) != 1 || v.Capabilities[0].State != CapabilityCompleted {
		t.Fatalf("capability not completed: %+v", v.Capabilities)
	}
	if v.Capabilities[0].Result["name"] != "neuron" {
		t.Fatalf("result not folded: %+v", v.Capabilities[0].Result)
	}
	_ = v.Fold(evt("execution.completed", "", ""))
	if v.Status != StatusCompleted {
		t.Fatalf("status = %v, want completed", v.Status)
	}
}

func TestModelFoldFailure(t *testing.T) {
	v := NewExecutionView("hello")
	_ = v.Fold(evt("capability.failed", "hello.say", `{"Message":"boom"}`))
	_ = v.Fold(evt("execution.failed", "", `{"Message":"execution boom"}`))
	if v.Status != StatusFailed || v.Message != "execution boom" {
		t.Fatalf("unexpected terminal state: %+v", v)
	}
	if v.Capabilities[0].Message != "boom" {
		t.Fatalf("capability failure message not folded: %+v", v.Capabilities[0])
	}
}

// TestStoppedCapabilityIsNotRenderedAsAFailure covers the point of the separate
// capability.cancelled event from the operator's side. A capability stopped
// because a sibling failed did not break, so it must not be shown with the
// failure glyph: that would send whoever reads the output to working code.
func TestStoppedCapabilityIsNotRenderedAsAFailure(t *testing.T) {
	v := NewExecutionView("hello")
	_ = v.Fold(evt("capability.ready", "hello.broken", ""))
	_ = v.Fold(evt("capability.failed", "hello.broken", `{"Message":"boom"}`))
	_ = v.Fold(evt("capability.ready", "hello.victim", ""))
	_ = v.Fold(evt("capability.cancelled", "hello.victim", `{"Message":"stopped because hello.broken failed"}`))

	states := map[string]CapabilityState{}
	for _, sv := range v.Capabilities {
		states[sv.ID] = sv.State
	}
	if states["hello.broken"] != CapabilityFailed {
		t.Errorf("the capability that actually failed is %v, want %v", states["hello.broken"], CapabilityFailed)
	}
	if states["hello.victim"] != CapabilityCancelled {
		t.Errorf("the stopped capability is %v, want %v", states["hello.victim"], CapabilityCancelled)
	}
	if got := v.Capabilities[1].Message; got != "stopped because hello.broken failed" {
		t.Errorf("cancellation message = %q, want the reason the work was stopped", got)
	}

	var buf bytes.Buffer
	r, err := New(Options{Out: &buf, Assembly: "hello", Mode: ModeStatic})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	_ = r.Handle(ctx, evt("execution.started", "", ""))
	_ = r.Handle(ctx, evt("capability.cancelled", "hello.victim", `{"Message":"stopped because hello.broken failed"}`))
	_ = r.Handle(ctx, evt("execution.failed", "", `{"Message":"boom"}`))
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}

	out := buf.String()
	if !strings.Contains(out, "stopped because hello.broken failed") {
		t.Errorf("static output does not explain why the capability stopped:\n%s", out)
	}
	victimLine := ""
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "hello.victim") {
			victimLine = line
		}
	}
	if strings.Contains(victimLine, glyphCross) {
		t.Errorf("the stopped capability was rendered with the failure glyph %q:\n%s", glyphCross, victimLine)
	}
	if !strings.Contains(victimLine, glyphCancelled) {
		t.Errorf("the stopped capability was not rendered with the cancellation glyph %q:\n%s", glyphCancelled, victimLine)
	}
}

func TestStaticRendererNoANSI(t *testing.T) {
	var buf bytes.Buffer
	r, err := New(Options{Out: &buf, Assembly: "hello", Mode: ModeStatic})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	_ = r.Handle(ctx, evt("execution.started", "", ""))
	_ = r.Handle(ctx, evt("capability.started", "hello.say", ""))
	_ = r.Handle(ctx, evt("capability.completed", "hello.say", `{"Result":{"name":"neuron"}}`))
	_ = r.Handle(ctx, evt("execution.completed", "", ""))
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if strings.Contains(out, "\033[") {
		t.Fatalf("static output contains ANSI escapes:\n%s", out)
	}
	if !strings.Contains(out, "hello.say") || !strings.Contains(out, "name: neuron") {
		t.Fatalf("static output missing content:\n%s", out)
	}
	if !strings.Contains(out, "Completed in ") {
		t.Fatalf("static output missing footer:\n%s", out)
	}
}

func TestJSONRendererEmitsNDJSON(t *testing.T) {
	var buf bytes.Buffer
	r, err := New(Options{Out: &buf, Assembly: "hello", Mode: ModeJSON})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	_ = r.Handle(ctx, evt("execution.started", "", ""))
	_ = r.Handle(ctx, evt("capability.completed", "hello.say", `{"Result":{"name":"neuron"}}`))
	_ = r.Handle(ctx, evt("execution.completed", "", ""))
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 3 {
		t.Fatalf("want 3 NDJSON lines, got %d:\n%s", len(lines), buf.String())
	}
	for _, line := range lines {
		var e protocol.StreamEvent
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			t.Fatalf("line is not valid JSON: %v (%s)", err, line)
		}
	}
}

func TestNewAutoFallsBackToStatic(t *testing.T) {
	var buf bytes.Buffer
	r, err := New(Options{Out: &buf, Assembly: "hello", Mode: ModeAuto})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := r.(*staticRenderer); !ok {
		t.Fatalf("ModeAuto with non-TTY writer = %T, want *staticRenderer", r)
	}
}

func TestNewRejectsNilWriter(t *testing.T) {
	if _, err := New(Options{}); err == nil {
		t.Fatal("expected error for nil writer")
	}
}
