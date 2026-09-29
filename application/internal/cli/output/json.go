// JSON renderer: canonical events emitted as NDJSON (one event per line).
// Designed for machine consumption (`neuron run --json | jq`). No decoration,
// no timestamps beyond what the event already carries.
package output

import (
	"context"
	"encoding/json"
	"io"

	"github.com/neuron-runtime/neuron/shared/types/protocol"
)

type jsonRenderer struct {
	enc *json.Encoder
}

func newJSONEncoder(out io.Writer) *json.Encoder {
	enc := json.NewEncoder(out)
	enc.SetEscapeHTML(false)
	return enc
}

func (r *jsonRenderer) Handle(ctx context.Context, evt protocol.StreamEvent) error {
	return r.enc.Encode(evt)
}

func (r *jsonRenderer) Close() error { return nil }
