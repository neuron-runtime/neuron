package instances

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/neuron-runtime/neuron/nore/internal/api/utils"
	"github.com/neuron-runtime/neuron/nore/internal/instance"
	"github.com/neuron-runtime/neuron/nore/internal/storage"
	"github.com/neuron-runtime/neuron/shared/types/protocol"
)

// Run executes an Assembly and returns its result in a single synchronous
// request.
//
// It is the simple, resource-oriented front door to execution: the request body
// carries only the execution params, and the Assembly being run is named by the
// `assembly` query parameter so identity lives in the URL rather than in the
// payload. It answers with the Assembly result or the execution's terminal
// error and never with execution history or events -- POST /v1/run is one
// request in, one result out. Callers that need to start, observe, stream, or
// cancel an execution use the advanced per-instance execution resources.
func (h *Handler) Run(w http.ResponseWriter, r *http.Request) {
	selector := strings.TrimSpace(r.URL.Query().Get(protocol.AssemblySelectorParam))
	if selector == "" {
		utils.ErrorJSON(w, http.StatusBadRequest, fmt.Errorf(
			"missing %q selector: address the Assembly as %s?%s=<name-or-key>",
			protocol.AssemblySelectorParam, protocol.RunPath, protocol.AssemblySelectorParam))
		return
	}

	i, err := h.runningInstance(r, selector)
	if err != nil {
		utils.ErrorJSON(w, runErrorStatus(err), err)
		return
	}

	var body protocol.RunRequest
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&body)
	}

	result, err := h.runSynchronously(r.Context(), i, body.Params)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return // the client went away; nothing can be written
		}
		utils.ErrorJSON(w, http.StatusInternalServerError, err)
		return
	}

	utils.WriteJSON(w, http.StatusOK, protocol.Response{
		Message: "execution completed",
		Status:  http.StatusOK,
		Data:    result,
	})
}

// runSynchronously starts a wait-mode execution and blocks until it reaches a
// terminal state, returning exactly the result a synchronous caller should
// observe.
//
// It is the single implementation of synchronous execution, shared by the simple
// POST /v1/run facade and the wait branch of the advanced per-instance Execute
// resource, so both surfaces report the same result contract. A non-nil error
// means the execution could not be started, or the request context ended before
// it did; a nil error with ExecutionResult.Error set means the execution ran and
// failed. The two are deliberately distinct: "we could not run it" and "it ran
// and failed" call for different client behavior.
func (h *Handler) runSynchronously(ctx context.Context, i *instance.Instance, params map[string]any) (protocol.ExecutionResult, error) {
	execution, err := i.Execute(ctx, params)
	if err != nil {
		return protocol.ExecutionResult{}, err
	}

	result := protocol.ExecutionResult{
		ExecutionID: execution.ID,
		InstanceID:  i.ID,
		Status:      string(execution.Status()),
	}

	if err := execution.Wait(ctx); err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return result, err
		}
		result.Status = string(execution.Status())
		result.Error = err.Error()
		return result, nil
	}

	assemblyResult, err := execution.ResolveAssemblyResult()
	if err != nil {
		result.Status = string(execution.Status())
		result.Error = fmt.Sprintf("resolve assembly result: %v", err)
		return result, nil
	}

	result.Status = string(execution.Status())
	result.Result = assemblyResult
	return result, nil
}

// runErrorStatus maps an instance-resolution failure to an HTTP status. A
// selector that names no registered Assembly is a client error; anything else is
// an unexpected server-side failure.
func runErrorStatus(err error) int {
	if errors.Is(err, storage.ErrNotFound) {
		return http.StatusNotFound
	}
	return http.StatusInternalServerError
}
