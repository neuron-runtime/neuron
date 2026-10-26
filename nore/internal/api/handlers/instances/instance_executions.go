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
	i, ok, resolveErr := h.resolveInstance(r, id)
	if resolveErr != nil {
		utils.ErrorJSON(w, http.StatusInternalServerError, resolveErr)
		return
	}
	if !ok {
		// The target may still be addressable: a assembly key that names a
		// registered but never-instantiated assembly has no runtime, yet its
		// executions are vacuously empty. Return an empty list instead of a
		// 404 so `neuron instance list --target=<name>@<version>` stays useful
		// before the first execution. Unknown instances and assemblies 404.
		if !strings.HasPrefix(id, "inst_") {
			if key, err := protocol.ParseKey(id); err == nil {
				exists, existsErr := h.assemblies.Exists(r.Context(), key)
				if existsErr != nil {
					utils.ErrorJSON(w, http.StatusInternalServerError, existsErr)
					return
				}
				if exists {
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

	executions, err := i.ListExecutions(r.Context())
	if err != nil {
		utils.ErrorJSON(w, http.StatusInternalServerError, err)
		return
	}

	items := make([]protocol.ExecutionItem, 0)
	for _, exec := range executions {
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

	// Detach returns as soon as the execution is accepted; the client can then
	// follow progress through the event stream endpoint. It is the asynchronous
	// half of the advanced execution resource; the simple synchronous facade is
	// POST /v1/run.
	if body.Mode == core.ExecutionModeDetach {
		execution, err := i.Execute(r.Context(), input)
		if err != nil {
			utils.ErrorJSON(w, http.StatusInternalServerError, err)
			return
		}
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

	// Wait mode (the default) blocks until the execution terminates and returns
	// its final result. The execution itself still runs asynchronously; this is
	// one shared implementation with the simple POST /v1/run facade.
	result, err := h.runSynchronously(r.Context(), i, input)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return // the client went away; nothing can be written
		}
		utils.ErrorJSON(w, http.StatusInternalServerError, err)
		return
	}

	message := "execution completed"
	if result.Error != "" {
		message = "execution failed"
	}
	utils.WriteJSON(w, http.StatusOK, protocol.Response{
		Message: message,
		Status:  http.StatusOK,
		Data:    result,
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

	exec, err := i.GetExecution(r.Context(), core.ID(execID))
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, instance.ErrExecutionNotFound) {
			status = http.StatusNotFound
		}
		utils.ErrorJSON(w, status, err)
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

	exec, _ := i.GetExecution(r.Context(), core.ID(execID))
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
