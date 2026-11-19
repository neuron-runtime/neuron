package core

import (
	"strings"
	"testing"
)

func TestNormalizeDurability(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{in: "", want: DurabilityHistory},
		{in: "none", want: DurabilityNone},
		{in: "history", want: DurabilityHistory},
		{in: "durable", want: DurabilityDurable},
		{in: " DURABLE ", want: DurabilityDurable},
	}
	for _, tc := range cases {
		got, err := NormalizeDurability(tc.in)
		if err != nil {
			t.Fatalf("NormalizeDurability(%q): %v", tc.in, err)
		}
		if got != tc.want {
			t.Errorf("NormalizeDurability(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestNormalizeDurabilityRejectsUnknownMode(t *testing.T) {
	_, err := NormalizeDurability("sometimes")
	if err == nil {
		t.Fatal("expected an error for an unknown durability mode")
	}
	for _, want := range []string{"sometimes", DurabilityNone, DurabilityHistory, DurabilityDurable} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
}
