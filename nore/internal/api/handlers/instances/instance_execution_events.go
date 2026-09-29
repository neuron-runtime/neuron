package instances

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/neuron-runtime/neuron/nore/internal/api/utils"
	"github.com/neuron-runtime/neuron/shared/types/core"
	"github.com/neuron-runtime/neuron/shared/types/protocol"
)

// StreamExecutionEvents exposes the execution's event stream over
// Server-Sent Events. It replays persisted history (optionally resumed from
// ?after={event_id}) and then delivers live events as they occur.
func (h *Handler) StreamExecutionEvents(w http.ResponseWriter, r *http.Request) {
	instID := utils.PathID(r.PathValue("id"))
	execID := core.ID(utils.PathID(r.PathValue("execID")))

	if instID == "" || execID == "" {
		utils.ErrorJSON(w, http.StatusNotImplemented, fmt.Errorf("missing instance id or exec id"))
		return
	}

	i, ok := h.instances.GetByID(instID)
	if !ok {
		utils.ErrorJSON(w, http.StatusNotFound, fmt.Errorf("instance %s not found", instID))
		return
	}
	if _, ok := i.GetExecution(execID); !ok {
		utils.ErrorJSON(w, http.StatusNotFound, fmt.Errorf("execution %s not found", execID))
		return
	}

	after := core.ID(strings.TrimSpace(r.URL.Query().Get("after")))
	events, err := i.Events(r.Context(), execID, after)
	if err != nil {
		utils.ErrorJSON(w, http.StatusInternalServerError, err)
		return
	}

	flusher, _ := w.(http.Flusher)
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	for {
		select {
		case <-r.Context().Done():
			return
		case msg, ok := <-events:
			if !ok {
				return
			}
			data, err := json.Marshal(protocol.StreamEvent{
				ID:            msg.EventID,
				Type:          msg.Type.String(),
				CorrelationID: msg.CorrelationID,
				CapabilityID:  msg.CapabilityID,
				OccurredAt:    msg.OccurredAt.UnixNano(),
				Payload:       msg.Payload,
			})
			if err != nil {
				return
			}
			if _, err := fmt.Fprintf(w, "data: %s\n\n", data); err != nil {
				return
			}
			if flusher != nil {
				flusher.Flush()
			}
		}
	}
}
