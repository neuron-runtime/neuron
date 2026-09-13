package instances

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/Muhammad-Jay/neuron/nore/internal/api/utils"
	"github.com/Muhammad-Jay/neuron/nore/internal/storage"
	"github.com/Muhammad-Jay/neuron/shared/types/core"
	"github.com/Muhammad-Jay/neuron/shared/types/protocol"
)

func (h *Handler) ListExecutions(w http.ResponseWriter, r *http.Request) {
	id := utils.PathID(r.PathValue("id"))
	i, ok := h.resolveInstance(r, id)
	if !ok {
		// The target may still be addressable: a system key that names a
		// registered but never-instantiated system has no runtime, yet its
		// executions are vacuously empty. Return an empty list instead of a
		// 404 so `neuron instance list --target=<name>@<version>` stays useful
		// before the first execution. Unknown instances and systems 404.
		if !strings.HasPrefix(id, "inst_") {
			if key, err := protocol.ParseKey(id); err == nil {
				if exists, existsErr := h.systems.Exists(r.Context(), key); existsErr == nil && exists {
					utils.WriteJSON(w, http.StatusOK, protocol.Response{
						Message: "executions",
						Status:  http.StatusOK,
						Data:    []protocol.ExecutionItem{},
					})
					return
				}
			}
		}
		utils.ErrorJSON(w, http.StatusNotFound, fmt.Errorf("instance %s not found", id))
		return
	}

	items := make([]protocol.ExecutionItem, 0)
	for _, exec := range i.ListExecutions() {
		item := protocol.ExecutionItem{
			ID:            exec.ID,
			CorrelationID: exec.CorrelationID,
			Status:        string(exec.Status()),
		}
		if started := exec.StartedAt(); !started.IsZero() {
			ns := started.UnixNano()
			item.StartedAt = &ns
		}
		if completed := exec.CompletedAt(); !completed.IsZero() {
			ns := completed.UnixNano()
			item.CompletedAt = &ns
		}
		if errMsg := exec.Error(); errMsg != "" {
			item.Error = errMsg
		}
		items = append(items, item)
	}

	utils.WriteJSON(w, http.StatusOK, protocol.Response{
		Message: "executions",
		Status:  http.StatusOK,
		Data:    items,
	})
}

func (h *Handler) Execute(w http.ResponseWriter, r *http.Request) {
	id := utils.PathID(r.PathValue("id"))

	i, ok := h.resolveInstance(r, id)
	// i, ok := h.instances.GetByID(id)
	if !ok {
		// Not an instance ID: resolve the segment as a system key
		// (systemID:version:hash[:env]) and lazily create the runtime from
		// the registered system.
		key, err := protocol.ParseKey(id)
		if err != nil {
			utils.ErrorJSON(w, http.StatusNotFound, fmt.Errorf("instance %s not found", id))
			return
		}
		i, _, err = h.instances.GetOrCreate(r.Context(), key)
		if err != nil {
			status := http.StatusInternalServerError
			if errors.Is(err, storage.ErrNotFound) {
				status = http.StatusNotFound
			}
			utils.ErrorJSON(w, status, err)
			return
		}
	}

	var body struct {
		Input map[string]any `json:"input,omitempty"`
		Mode  string         `json:"mode,omitempty"`
	}
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&body)
	}

	input := utils.MergeMaps(utils.GetQueryParams(r), body.Input)

	execution, err := i.Execute(r.Context(), input)
	if err != nil {
		utils.ErrorJSON(w, http.StatusInternalServerError, err)
		return
	}

	// Detach returns as soon as the execution is accepted; the client can then
	// follow progress through the event stream endpoint. This preserves the
	// original behavior and is what the CLI uses to stream events live.
	if body.Mode == core.ExecutionModeDetach {
		utils.WriteJSON(w, http.StatusAccepted, protocol.Response{
			Message: "execution accepted",
			Status:  http.StatusAccepted,
			Data: protocol.ExecuteResponse{
				ExecutionID: execution.ID,
				InstanceID:  i.ID,
				Status:      string(execution.Status()),
				Time:        time.Now().UTC(),
			},
		})
		return
	}

	// Wait mode (the default) blocks until the execution terminates and
	// returns its final result, turning N.O.R.E. into a synchronous runtime
	// for API callers. The execution itself still runs asynchronously.
	if err := execution.Wait(r.Context()); err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return // the client went away; nothing can be written
		}
		utils.WriteJSON(w, http.StatusOK, protocol.Response{
			Message: "execution failed",
			Status:  http.StatusOK,
			Data: protocol.ExecutionResult{
				ExecutionID: execution.ID,
				InstanceID:  i.ID,
				Status:      string(execution.Status()),
				Error:       err.Error(),
			},
		})
		return
	}

	utils.WriteJSON(w, http.StatusOK, protocol.Response{
		Message: "execution completed",
		Status:  http.StatusOK,
		Data: protocol.ExecutionResult{
			ExecutionID: execution.ID,
			InstanceID:  i.ID,
			Status:      string(execution.Status()),
			Outputs:     execution.StringKeyedOutputs(),
		},
	})
}

func (h *Handler) GetExecutionState(w http.ResponseWriter, r *http.Request) {
	instID := utils.PathID(r.PathValue("id"))
	execID := utils.PathID(r.PathValue("execID"))

	i, ok := h.instances.GetByID(instID)
	if !ok {
		utils.ErrorJSON(w, http.StatusNotFound, fmt.Errorf("instance %s not found", instID))
		return
	}

	exec, ok := i.GetExecution(core.ID(execID))
	if !ok {
		utils.ErrorJSON(w, http.StatusNotFound, fmt.Errorf("execution %s not found", execID))
		return
	}

	item := protocol.ExecutionItem{
		ID:            exec.ID,
		CorrelationID: exec.CorrelationID,
		Status:        string(exec.Status()),
	}
	if started := exec.StartedAt(); !started.IsZero() {
		ns := started.UnixNano()
		item.StartedAt = &ns
	}
	if completed := exec.CompletedAt(); !completed.IsZero() {
		ns := completed.UnixNano()
		item.CompletedAt = &ns
	}
	if errMsg := exec.Error(); errMsg != "" {
		item.Error = errMsg
	}

	utils.WriteJSON(w, http.StatusOK, protocol.Response{
		Message: "execution",
		Status:  http.StatusOK,
		Data:    item,
	})
}

func (h *Handler) GetExecutionEvents(w http.ResponseWriter, r *http.Request) {
	instID := utils.PathID(r.PathValue("id"))
	execID := utils.PathID(r.PathValue("execID"))

	i, ok := h.instances.GetByID(instID)
	if !ok {
		utils.ErrorJSON(w, http.StatusNotFound, fmt.Errorf("instance %s not found", instID))
		return
	}

	events, err := i.ListExecutionEvents(r.Context(), core.ID(execID))
	if err != nil {
		utils.ErrorJSON(w, http.StatusInternalServerError, err)
		return
	}

	items := make([]protocol.EventItem, 0, len(events))
	for _, evt := range events {
		items = append(items, protocol.EventItem{
			ID:        evt.Metadata.EventID,
			Type:      evt.Type.String(),
			ServiceID: evt.Metadata.ServiceID,
			Payload:   evt.Payload,
		})
	}

	utils.WriteJSON(w, http.StatusOK, protocol.Response{
		Message: "events",
		Status:  http.StatusOK,
		Data:    items,
	})
}
