package instances

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/neuron-runtime/neuron/nore/internal/api/utils"
	"github.com/neuron-runtime/neuron/nore/internal/instance"
	"github.com/neuron-runtime/neuron/nore/internal/storage"
	"github.com/neuron-runtime/neuron/shared/types/core"
	"github.com/neuron-runtime/neuron/shared/types/protocol"
)

func (h *Handler) ListExecutions(w http.ResponseWriter, r *http.Request) {
	id := utils.PathID(r.PathValue("id"))
	i, ok := h.resolveInstance(r, id)
	if !ok {
		// The target may still be addressable: a assembly key that names a
		// registered but never-instantiated assembly has no runtime, yet its
		// executions are vacuously empty. Return an empty list instead of a
		// 404 so `neuron instance list --target=<name>@<version>` stays useful
		// before the first execution. Unknown instances and assemblies 404.
		if !strings.HasPrefix(id, "inst_") {
			if key, err := protocol.ParseKey(id); err == nil {
				if exists, existsErr := h.assemblies.Exists(r.Context(), key); existsErr == nil && exists {
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
			ID:                exec.ID,
			CorrelationID:     exec.CorrelationID,
			Status:            string(exec.Status()),
			ParentExecutionID: exec.ParentExecutionID,
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

	i, err := h.runningInstance(r, id)
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, storage.ErrNotFound) {
			status = http.StatusNotFound
		}
		utils.ErrorJSON(w, status, err)
		return
	}

	var body protocol.ExecuteRequest
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&body)
	}

	input := utils.MergeMaps(utils.GetQueryParams(r), body.Params)

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
			Results:     execution.StringKeyedResults(),
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

// CancelExecution stops a running execution at the caller's request.
//
// The status codes distinguish the three outcomes a client has to tell apart.
// 404 means the instance or execution does not exist and never can be cancelled.
// 409 means it exists but has already finished, so the cancellation is refused
// rather than silently ignored -- a client that asked to stop something and is
// told "ok" must be able to trust that it was still running. 200 confirms the
// execution was stopped and is now terminal.
func (h *Handler) CancelExecution(w http.ResponseWriter, r *http.Request) {
	instID := utils.PathID(r.PathValue("id"))
	execID := utils.PathID(r.PathValue("execID"))

	i, ok := h.instances.GetByID(instID)
	if !ok {
		utils.ErrorJSON(w, http.StatusNotFound, fmt.Errorf("instance %s not found", instID))
		return
	}

	var body protocol.CancelExecutionRequest
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&body)
	}
	reason := errors.New(body.Reason)
	if body.Reason == "" {
		// A cancellation with no stated reason is still legitimate; the message
		// just has to say something honest rather than nothing at all.
		reason = fmt.Errorf("execution %s was cancelled", execID)
	}

	if err := i.CancelExecution(r.Context(), core.ID(execID), reason); err != nil {
		switch {
		case errors.Is(err, instance.ErrExecutionNotFound):
			utils.ErrorJSON(w, http.StatusNotFound, err)
		case errors.Is(err, instance.ErrExecutionNotCancellable):
			utils.ErrorJSON(w, http.StatusConflict, err)
		default:
			utils.ErrorJSON(w, http.StatusInternalServerError, err)
		}
		return
	}

	exec, _ := i.GetExecution(core.ID(execID))
	item := protocol.ExecutionItem{ID: core.ID(execID)}
	if exec != nil {
		item.Status = string(exec.Status())
		item.CorrelationID = exec.CorrelationID
		if completed := exec.CompletedAt(); !completed.IsZero() {
			ns := completed.UnixNano()
			item.CompletedAt = &ns
		}
	}
	utils.WriteJSON(w, http.StatusOK, protocol.Response{
		Message: "execution cancelled",
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
			ID:           evt.Metadata.EventID,
			Type:         evt.Type.String(),
			CapabilityID: evt.Metadata.CapabilityID,
			Payload:      evt.Payload,
		})
	}

	utils.WriteJSON(w, http.StatusOK, protocol.Response{
		Message: "events",
		Status:  http.StatusOK,
		Data:    items,
	})
}
