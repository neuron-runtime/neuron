package instances

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/neuron-runtime/neuron/nore/internal/api/utils"
	"github.com/neuron-runtime/neuron/nore/internal/execution"
	"github.com/neuron-runtime/neuron/nore/internal/execution/scheduler"
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

	// A status filter lets an operator list only suspended executions; the
	// wait details below say why each one is suspended and what resumes it.
	statusFilter := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("status")))

	items := make([]protocol.ExecutionItem, 0)
	for _, exec := range executions {
		status := string(exec.Status())
		if statusFilter != "" && !strings.EqualFold(status, statusFilter) {
			continue
		}
		item := protocol.ExecutionItem{
			ID:                exec.ID,
			CorrelationID:     exec.CorrelationID,
			Status:            status,
			ParentExecutionID: exec.ParentExecutionID,
			Waits:             waitItems(exec),
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

// waitItems reports the persisted wait state of every suspended capability in
// an execution. It reads only execution accessors, never the blueprint, so
// metadata-only restored executions expose the same wait details as live ones.
func waitItems(exec *execution.Execution) []protocol.WaitItem {
	ids := exec.WaitingCapabilities()
	if len(ids) == 0 {
		return nil
	}
	items := make([]protocol.WaitItem, 0, len(ids))
	for _, id := range ids {
		state := exec.CapabilityState(id)
		item := protocol.WaitItem{
			CapabilityID:   id,
			WaitType:       string(state.WaitType),
			ExpectedSignal: state.ExpectedSignal,
			WaitReason:     state.WaitReason,
			CorrelationKey: state.CorrelationKey,
		}
		if state.WaitUntil != nil {
			ns := state.WaitUntil.UnixNano()
			item.Deadline = &ns
		}
		items = append(items, item)
	}
	return items
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
		Waits:         waitItems(exec),
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

// SignalExecution is the instance-scoped compatibility route. It resolves the
// addressed instance and delegates the delivery to the same authoritative
// boundary as the execution-scoped route. Ownership of the execution is checked
// inside DeliverSignal against the durable execution record, so an execution
// belonging to another instance is indistinguishable from a missing one.
func (h *Handler) SignalExecution(w http.ResponseWriter, r *http.Request) {
	instID := utils.PathID(r.PathValue("id"))
	i, ok := h.instances.GetByID(instID)
	if !ok {
		utils.WriteJSON(w, http.StatusNotFound, protocol.Response{
			Message: fmt.Sprintf("instance %s not found", instID),
			Status:  http.StatusNotFound,
			Code:    protocol.SignalCodeNotFound,
		})
		return
	}
	h.deliverSignal(w, r, i, r.PathValue("execID"), r.PathValue("signalName"))
}

// SignalExecutionByExecution is the primary execution-scoped route. The owning
// instance is resolved from the durable execution record, not from the URL, so
// a caller cannot address an execution through the wrong instance.
func (h *Handler) SignalExecutionByExecution(w http.ResponseWriter, r *http.Request) {
	execID := utils.PathID(r.PathValue("id"))
	i, err := h.instances.InstanceForExecution(r.Context(), core.ID(execID))
	if err != nil {
		writeSignalError(w, err)
		return
	}
	h.deliverSignal(w, r, i, execID, r.PathValue("name"))
}

// deliverSignal is the thin HTTP boundary over Instance.DeliverSignal. It
// validates the request shape, hands the delivery to the domain, and writes the
// acknowledgement only after the domain has committed it. It never mutates
// execution state itself and never traverses a blueprint to find a wait.
func (h *Handler) deliverSignal(w http.ResponseWriter, r *http.Request, i *instance.Instance, execID, signalName string) {
	execID = utils.PathID(execID)
	signalName = strings.TrimSpace(signalName)
	if execID == "" || signalName == "" {
		utils.WriteJSON(w, http.StatusBadRequest, protocol.Response{
			Message: "execution ID and signal name are required",
			Status:  http.StatusBadRequest,
			Code:    protocol.SignalCodeInvalidRequest,
		})
		return
	}

	req, err := decodeSignalRequest(r)
	if err != nil {
		utils.WriteJSON(w, http.StatusBadRequest, protocol.Response{
			Message: err.Error(),
			Status:  http.StatusBadRequest,
			Code:    protocol.SignalCodeInvalidRequest,
		})
		return
	}

	result, err := i.DeliverSignal(r.Context(), instance.SignalDeliveryRequest{
		ExecutionID:    core.ID(execID),
		DeliveryID:     req.DeliveryID,
		SignalName:     signalName,
		CorrelationKey: req.CorrelationKey,
		Payload:        req.Payload,
	})
	if err != nil {
		writeSignalError(w, err)
		return
	}

	status := "accepted"
	if result.Replay {
		status = "replay"
	}
	utils.WriteJSON(w, http.StatusOK, protocol.Response{
		Message: "execution signaled",
		Status:  http.StatusOK,
		Data: protocol.SignalDeliveryResponse{
			DeliveryID: result.DeliveryID,
			SignalName: signalName,
			Status:     status,
			Replay:     result.Replay,
		},
	})
}

// Signal requests are untrusted HTTP input. These bounds are applied before
// decoding so a client cannot make the handler allocate an arbitrary body or
// storage key. Domain payload/schema authority is unchanged.
const (
	// maxSignalBodyBytes is the largest accepted JSON request body. The decoder
	// is given one extra byte so a body exactly at the limit can still reach
	// EOF; anything larger is rejected.
	maxSignalBodyBytes int64 = 64 << 10
	// maxSignalFieldBytes bounds deliveryId and correlationKey, which are used
	// in delivery identity and wait matching.
	maxSignalFieldBytes = 256
)

// decodeSignalRequest validates the wire body at the boundary. DeliveryID is
// required, bounded, and must be safe to use as one storage key segment;
// correlationKey is bounded; payload defaults to an empty object. Body shape
// errors never reach the domain boundary.
func decodeSignalRequest(r *http.Request) (protocol.SignalDeliveryRequest, error) {
	var req protocol.SignalDeliveryRequest
	if r.Body == nil {
		return req, fmt.Errorf("signal body is required")
	}
	// A nil ResponseWriter is deliberate: oversized input is reported as a 400
	// invalid request by the caller, not as an HTTP 413 transport error.
	body := http.MaxBytesReader(nil, r.Body, maxSignalBodyBytes+1)
	dec := json.NewDecoder(body)
	if err := dec.Decode(&req); err != nil {
		return req, fmt.Errorf("invalid signal body: %w", err)
	}
	var trailing any
	if err := dec.Decode(&trailing); err != io.EOF {
		if err == nil {
			return req, fmt.Errorf("invalid signal body: unexpected trailing JSON")
		}
		return req, fmt.Errorf("invalid signal body: %w", err)
	}

	req.DeliveryID = strings.TrimSpace(req.DeliveryID)
	if req.DeliveryID == "" {
		return req, fmt.Errorf("deliveryId is required")
	}
	if len(req.DeliveryID) > maxSignalFieldBytes {
		return req, fmt.Errorf("deliveryId exceeds %d bytes", maxSignalFieldBytes)
	}
	if strings.ContainsAny(req.DeliveryID, "/\x00") {
		return req, fmt.Errorf("deliveryId contains an invalid path character")
	}
	if len(req.CorrelationKey) > maxSignalFieldBytes {
		return req, fmt.Errorf("correlationKey exceeds %d bytes", maxSignalFieldBytes)
	}
	if req.Payload == nil {
		req.Payload = map[string]any{}
	}
	return req, nil
}

// writeSignalError maps the domain's typed errors to the HTTP contract. A
// machine-readable code accompanies each case so callers can tell outcomes
// apart without inspecting the error text.
func writeSignalError(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	code := ""
	switch {
	case errors.Is(err, instance.ErrExecutionNotFound):
		status, code = http.StatusNotFound, protocol.SignalCodeNotFound
	case errors.Is(err, scheduler.ErrSignalNoMatchingWait):
		status, code = http.StatusNotFound, protocol.SignalCodeNoMatchingWait
	case errors.Is(err, scheduler.ErrSignalAmbiguousWait):
		status, code = http.StatusConflict, protocol.SignalCodeAmbiguousWait
	case errors.Is(err, execution.ErrSignalDeliveryConflict):
		status, code = http.StatusConflict, protocol.SignalCodeConflict
	case errors.Is(err, scheduler.ErrSignalWaitExpired), errors.Is(err, scheduler.ErrSignalWaitTerminal):
		status, code = http.StatusGone, protocol.SignalCodeExpired
	case errors.Is(err, scheduler.ErrSignalInvalidPayload):
		status, code = http.StatusBadRequest, protocol.SignalCodeInvalidPayload
	case errors.Is(err, scheduler.ErrWaitStoreUnavailable):
		status, code = http.StatusServiceUnavailable, protocol.SignalCodeUnavailable
	}
	utils.WriteJSON(w, status, protocol.Response{Message: err.Error(), Status: status, Code: code})
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
