package input

import (
	"strings"
	"testing"
	"time"
)

func TestNewParamsOverrideConfiguration(t *testing.T) {
	values := New("neuron:core:test",
		map[string]any{"mode": "config", "only_config": "kept"},
		map[string]any{"mode": "param"},
	)

	mode, present, err := values.String("mode")
	if err != nil {
		t.Fatalf("String(mode): %v", err)
	}
	if !present {
		t.Fatal("String(mode) reported no value")
	}
	if mode != "param" {
		t.Errorf("mode = %q, want %q", mode, "param")
	}

	kept, present, err := values.String("only_config")
	if err != nil {
		t.Fatalf("String(only_config): %v", err)
	}
	if !present || kept != "kept" {
		t.Errorf("only_config = %q (present %v), want %q", kept, present, "kept")
	}
}

func TestNewDoesNotMutateItsInputs(t *testing.T) {
	configuration := map[string]any{"mode": "config"}
	params := map[string]any{"mode": "param"}

	values := New("neuron:core:test", configuration, params)
	if _, _, err := values.String("mode"); err != nil {
		t.Fatalf("String(mode): %v", err)
	}

	if configuration["mode"] != "config" {
		t.Errorf("configuration was mutated: %v", configuration)
	}
	if params["mode"] != "param" {
		t.Errorf("params were mutated: %v", params)
	}
}

func TestRequiredStringReportsWhatWasReceived(t *testing.T) {
	values := New("neuron:core:http", map[string]any{"method": "GET"}, nil)

	_, err := values.RequiredString("url")
	if err == nil {
		t.Fatal("RequiredString(url) succeeded, want an error")
	}
	if !strings.Contains(err.Error(), "neuron:core:http") {
		t.Errorf("error %q does not name the runtime", err)
	}
	if !strings.Contains(err.Error(), `"url"`) {
		t.Errorf("error %q does not name the parameter", err)
	}
	if !strings.Contains(err.Error(), "method") {
		t.Errorf("error %q does not report the received input", err)
	}
}

func TestRequiredStringRejectsEmptyValue(t *testing.T) {
	values := New("neuron:core:http", nil, map[string]any{"url": "   "})

	if _, err := values.RequiredString("url"); err == nil {
		t.Fatal("RequiredString(url) succeeded for a blank value")
	} else if !strings.Contains(err.Error(), "empty") {
		t.Errorf("error %q does not report the value as empty", err)
	}
}

func TestStringRejectsNonStringValue(t *testing.T) {
	values := New("neuron:core:http", nil, map[string]any{"url": 42})

	_, _, err := values.String("url")
	if err == nil {
		t.Fatal("String(url) succeeded for a number")
	}
	if !strings.Contains(err.Error(), "must be a string") {
		t.Errorf("error %q does not state the expected type", err)
	}
	if !strings.Contains(err.Error(), "a number") {
		t.Errorf("error %q does not report the received type", err)
	}
}

func TestBoolRejectsStringValue(t *testing.T) {
	values := New("neuron:core:http", nil, map[string]any{"followRedirects": "true"})

	if _, err := values.Bool("followRedirects", false); err == nil {
		t.Fatal("Bool(followRedirects) succeeded for the string \"true\"")
	}
}

func TestIntAcceptsJSONNumbersAndRejectsFractions(t *testing.T) {
	values := New("neuron:core:test", nil, map[string]any{"count": 7.0, "ratio": 1.5})

	count, err := values.Int("count", 0)
	if err != nil {
		t.Fatalf("Int(count): %v", err)
	}
	if count != 7 {
		t.Errorf("count = %d, want 7", count)
	}

	if _, err := values.Int("ratio", 0); err == nil {
		t.Fatal("Int(ratio) succeeded for a fractional number")
	}
}

func TestIntFallsBackWhenAbsent(t *testing.T) {
	values := New("neuron:core:test", nil, nil)

	count, err := values.Int("count", 3)
	if err != nil {
		t.Fatalf("Int(count): %v", err)
	}
	if count != 3 {
		t.Errorf("count = %d, want the fallback 3", count)
	}
}

func TestDurationParsesDurationStrings(t *testing.T) {
	values := New("neuron:core:http", nil, map[string]any{"timeout": "90s"})

	timeout, err := values.Duration("timeout", time.Second)
	if err != nil {
		t.Fatalf("Duration(timeout): %v", err)
	}
	if timeout != 90*time.Second {
		t.Errorf("timeout = %s, want 90s", timeout)
	}
}

func TestDurationRejectsInvalidValue(t *testing.T) {
	values := New("neuron:core:http", nil, map[string]any{"timeout": "soon"})

	if _, err := values.Duration("timeout", time.Second); err == nil {
		t.Fatal("Duration(timeout) succeeded for an unparseable duration")
	} else if !strings.Contains(err.Error(), "30s") {
		t.Errorf("error %q does not show an example of a valid duration", err)
	}
}

func TestStringMapRejectsNonStringEntries(t *testing.T) {
	values := New("neuron:core:http", nil, map[string]any{"headers": map[string]any{"X-Count": 3}})

	if _, _, err := values.StringMap("headers"); err == nil {
		t.Fatal("StringMap(headers) succeeded with a numeric entry")
	}
}

func TestStringSliceRejectsNonStringEntries(t *testing.T) {
	values := New("neuron:core:command", nil, map[string]any{"args": []any{"--flag", 2}})

	if _, _, err := values.StringSlice("args"); err == nil {
		t.Fatal("StringSlice(args) succeeded with a numeric entry")
	}
}

func TestParseFormatDefaultsAndRejectsUnknown(t *testing.T) {
	format, err := ParseFormat("", FormatJSON)
	if err != nil {
		t.Fatalf("ParseFormat(\"\"): %v", err)
	}
	if format != FormatJSON {
		t.Errorf("format = %q, want %q", format, FormatJSON)
	}

	if _, err := ParseFormat("yaml", FormatJSON); err == nil {
		t.Fatal("ParseFormat(yaml) succeeded, want an error")
	} else if !strings.Contains(err.Error(), "json") || !strings.Contains(err.Error(), "text") {
		t.Errorf("error %q does not list the supported formats", err)
	}
}

func TestFormatReadsTheSelectedFormat(t *testing.T) {
	values := New("neuron:core:http", nil, map[string]any{"format": "text"})

	format, err := values.Format("format", FormatJSON)
	if err != nil {
		t.Fatalf("Format(format): %v", err)
	}
	if format != FormatText {
		t.Errorf("format = %q, want %q", format, FormatText)
	}
}

// A nested object is read with the same rules as the outer values, so a
// built-in does not have to know how the fields of a nested object are typed.
func TestNestedReadsAnObjectWithTheSameRules(t *testing.T) {
	values := New("neuron:core:http", nil, map[string]any{
		"retry": map[string]any{"attempts": float64(3), "backoff": "200ms"},
	})

	nested, present, err := values.Nested("retry")
	if err != nil {
		t.Fatalf("Nested: %v", err)
	}
	if !present {
		t.Fatal("Nested did not find the object")
	}

	attempts, err := nested.Int("attempts", 1)
	if err != nil {
		t.Fatalf("Int: %v", err)
	}
	if attempts != 3 {
		t.Errorf("attempts = %d, want 3", attempts)
	}

	backoff, err := nested.Duration("backoff", 0)
	if err != nil {
		t.Fatalf("Duration: %v", err)
	}
	if backoff.String() != "200ms" {
		t.Errorf("backoff = %s, want 200ms", backoff)
	}
}

// A value that is not an object is refused rather than read as one.
func TestNestedRejectsANonObjectValue(t *testing.T) {
	values := New("neuron:core:http", nil, map[string]any{"retry": "often"})

	_, _, err := values.Nested("retry")
	if err == nil {
		t.Fatal("Nested accepted a value that is not an object")
	}
}

// A nested object that is absent is reported as absent.
func TestNestedReportsAnAbsentObject(t *testing.T) {
	values := New("neuron:core:http", nil, nil)

	_, present, err := values.Nested("retry")
	if err != nil {
		t.Fatalf("Nested: %v", err)
	}
	if present {
		t.Error("Nested found an object that is not there")
	}
}
