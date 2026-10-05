package data

import (
	"encoding/json"
	"math"
	"reflect"
	"testing"
)

// Every JSON number is a float64 once decoded, so a runtime payload must use
// float64 for every number. Anything else makes a value's Go type depend on
// which layer produced it.
func TestCanonicalValueNormalizesEveryNumberType(t *testing.T) {
	cases := []struct {
		name  string
		input any
		want  any
	}{
		{"int", 3, float64(3)},
		{"int8", int8(3), float64(3)},
		{"int16", int16(3), float64(3)},
		{"int32", int32(3), float64(3)},
		{"int64", int64(3), float64(3)},
		{"uint", uint(3), float64(3)},
		{"uint8", uint8(3), float64(3)},
		{"uint16", uint16(3), float64(3)},
		{"uint32", uint32(3), float64(3)},
		{"uint64", uint64(3), float64(3)},
		{"float32", float32(1.5), 1.5},
		{"float64", 1.5, 1.5},
		{"negative", -42, float64(-42)},
		{"zero", 0, float64(0)},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			got := CanonicalValue(testCase.input)
			if !reflect.DeepEqual(got, testCase.want) {
				t.Errorf("CanonicalValue(%#v) = %#v (%T), want %#v", testCase.input, got, got, testCase.want)
			}
		})
	}
}

func TestCanonicalValuePassesThroughNonNumbers(t *testing.T) {
	cases := []struct {
		name  string
		input any
		want  any
	}{
		{"nil", nil, nil},
		{"bool", true, true},
		{"string", "a", "a"},
		{"empty string", "", ""},
		// JSON cannot encode these, so a payload carrying one could not survive
		// persistence. Dropping them keeps the payload encodable.
		{"NaN", math.NaN(), nil},
		{"positive infinity", math.Inf(1), nil},
		{"negative infinity", math.Inf(-1), nil},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			got := CanonicalValue(testCase.input)
			if !reflect.DeepEqual(got, testCase.want) {
				t.Errorf("CanonicalValue(%#v) = %#v (%T), want %#v", testCase.input, got, got, testCase.want)
			}
		})
	}
}

func TestCanonicalValueRecurses(t *testing.T) {
	input := map[string]any{
		"order": map[string]any{
			"total": 2250,
			"items": []any{
				map[string]any{"priceCents": 100},
				map[string]any{"priceCents": int64(200)},
			},
		},
		"retries": 3,
	}

	want := map[string]any{
		"order": map[string]any{
			"total": float64(2250),
			"items": []any{
				map[string]any{"priceCents": float64(100)},
				map[string]any{"priceCents": float64(200)},
			},
		},
		"retries": float64(3),
	}

	if got := CanonicalValue(input); !reflect.DeepEqual(got, want) {
		t.Errorf("CanonicalValue\ngot:  %#v\nwant: %#v", got, want)
	}
}

// The whole point of the canonical form is that a value looks the same before
// and after a persistence round trip. Anything that changes under
// marshal/unmarshal is a value whose behavior depends on whether the execution
// has been restarted.
func TestCanonicalValueSurvivesJSONRoundTrip(t *testing.T) {
	input := map[string]any{
		"order": map[string]any{
			"total": 2250,
			"items": []any{map[string]any{"priceCents": 100}},
		},
		"retries":     3,
		"enabled":     true,
		"note":        nil,
		"ratio":       1.5,
		"temperature": float32(36.6),
	}

	canonical := CanonicalMap(input)

	encoded, err := json.Marshal(canonical)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var restored map[string]any
	if err := json.Unmarshal(encoded, &restored); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if !reflect.DeepEqual(canonical, restored) {
		t.Errorf("value changed across a persistence round trip\nbefore: %#v\nafter:  %#v", canonical, restored)
	}
}

// The input must not be mutated: the engine hands out canonical copies, and a
// canonicalizer that rewrote its argument in place would corrupt engine state.
func TestCanonicalMapDoesNotMutateInput(t *testing.T) {
	input := map[string]any{
		"order":   map[string]any{"total": 2250},
		"retries": 3,
	}

	got := CanonicalMap(input)
	got["order"].(map[string]any)["total"] = float64(9999)
	got["retries"] = float64(9)

	if input["order"].(map[string]any)["total"] != 2250 {
		t.Error("CanonicalMap mutated a nested value of its input")
	}
	if input["retries"] != 3 {
		t.Error("CanonicalMap mutated a top-level value of its input")
	}
}

func TestCanonicalMapIsIdempotent(t *testing.T) {
	input := map[string]any{"order": map[string]any{"total": 2250}, "retries": 3}
	once := CanonicalMap(input)
	twice := CanonicalMap(once)
	if !reflect.DeepEqual(once, twice) {
		t.Errorf("CanonicalMap is not idempotent\nonce:  %#v\ntwice: %#v", once, twice)
	}
}

// A nil map becomes an empty map so a caller that reads from the result never
// has to distinguish "absent" from "empty".
func TestCanonicalMapNilBecomesEmpty(t *testing.T) {
	got := CanonicalMap(nil)
	if got == nil {
		t.Fatal("CanonicalMap(nil) = nil, want an empty map")
	}
	if len(got) != 0 {
		t.Errorf("CanonicalMap(nil) = %#v, want an empty map", got)
	}
}

// Keys are left exactly as authored. Key normalization is a separate concern
// owned by DeepSnakeCase; conflating the two would silently rewrite an
// author's payload keys.
func TestCanonicalMapPreservesKeys(t *testing.T) {
	input := map[string]any{"shippingAddress": map[string]any{"zipCode": 69002}}
	want := map[string]any{"shippingAddress": map[string]any{"zipCode": float64(69002)}}

	if got := CanonicalMap(input); !reflect.DeepEqual(got, want) {
		t.Errorf("CanonicalMap\ngot:  %#v\nwant: %#v", got, want)
	}
}
