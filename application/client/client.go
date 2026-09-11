// Package client provides the official SDK for interacting with the N.O.R.E. API.
// It abstracts the underlying transport (local socket or remote HTTP) and
// handles protocol serialization for systems, instances, and executions.
package client

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"

	"github.com/Muhammad-Jay/neuron/application/connection"
	wsmessage "github.com/Muhammad-Jay/neuron/shared/protocol/websocket"
	"github.com/Muhammad-Jay/neuron/shared/types/core"
	"github.com/Muhammad-Jay/neuron/shared/types/protocol"
)

// Client facilitates communication with the neuron server.
// It utilizes a connection interface to allow for seamless switching
// between local socket and remote HTTP communication.
type Client struct {
	conn connection.Connection
}

// New creates a new Client using the provided Connection interface.
func New(conn connection.Connection) *Client {
	return &Client{conn: conn}
}

// NewLocal initializes a Client configured to communicate over a local Unix domain socket.
func NewLocal(socketPath string) *Client {
	return New(connection.NewLocal(socketPath))
}

// NewRemote initializes a Client configured to communicate with a remote HTTP endpoint.
func NewRemote(endpoint string) *Client {
	return New(connection.NewRemote(endpoint))
}

// Close terminates the underlying connection and releases associated resources.
func (c *Client) Close() error {
	return c.conn.Close()
}

// Health pings the server to verify it is reachable and operational.
func (c *Client) Health(ctx context.Context) error {
	return c.conn.Health(ctx)
}

// ListInstances retrieves all currently registered systems instances from the server.
func (c *Client) ListInstances(ctx context.Context, queryPath string) ([]protocol.InstanceResponse, error) {
	var response struct {
		Data []protocol.InstanceResponse `json:"data"`
	}

	var path string

	if queryPath == "" || queryPath == "/" {
		path = protocol.InstancesPath
	} else {
		path = protocol.InstancesPath + queryPath
	}

	if err := c.conn.Do(ctx, http.MethodGet, path, nil, &response); err != nil {
		return nil, err
	}

	return response.Data, nil
}

// EnsureInstance verifies the existence of a systems instance by its key,
// creating a new instance on the server if it does not already exist.
func (c *Client) EnsureInstance(ctx context.Context, key protocol.InstanceKey, system *core.System) (protocol.InstanceResponse, error) {
	if system == nil {
		return protocol.InstanceResponse{}, fmt.Errorf("systems is required")
	}

	var response struct {
		Data protocol.InstanceResponse `json:"data"`
	}

	req := protocol.CreateInstanceRequest{
		Key:    key,
		System: system,
	}

	if err := c.conn.Do(ctx, http.MethodPost, protocol.InstancesPath, req, &response); err != nil {
		return protocol.InstanceResponse{}, err
	}

	return response.Data, nil
}

// Execute triggers a workflow execution on a specific instance using the provided input data.
// If mode is "detach", it returns immediately with ExecuteResponse (HTTP 202).
// Otherwise, it waits for completion and returns ExecutionResult (HTTP 200).
func (c *Client) Execute(ctx context.Context, instanceKey protocol.InstanceKey, system *core.System, input map[string]any, mode string) (protocol.ExecutionResult, error) {
	if instanceKey.SystemID == "" {
		return protocol.ExecutionResult{}, fmt.Errorf("instance SystemID is required")
	}

	instance, err := c.EnsureInstance(ctx, instanceKey, system)
	if err != nil {
		return protocol.ExecutionResult{}, err
	}

	req := protocol.ExecuteRequest{
		Input: input,
		Mode:  mode,
	}

	endpoint := fmt.Sprintf(protocol.ExecutePath, instance.ID)

	if mode == "detach" {
		var response struct {
			Data protocol.ExecuteResponse `json:"data"`
		}
		if err := c.conn.Do(ctx, http.MethodPost, endpoint, req, &response); err != nil {
			return protocol.ExecutionResult{}, err
		}
		return protocol.ExecutionResult{
			ExecutionID: response.Data.ExecutionID,
			InstanceID:  response.Data.InstanceID,
			Status:      response.Data.Status,
		}, nil
	}

	var response struct {
		Data protocol.ExecutionResult `json:"data"`
	}
	if err := c.conn.Do(ctx, http.MethodPost, endpoint, req, &response); err != nil {
		return protocol.ExecutionResult{}, err
	}
	return response.Data, nil
}

// ExecuteByKey triggers a workflow execution on the system identified by key,
// without sending the system definition. The server lazily creates the instance
// from the durable registered system on the first execution.
func (c *Client) ExecuteByKey(ctx context.Context, key protocol.InstanceKey, input map[string]any, mode string) (protocol.ExecutionResult, error) {
	if key.SystemID == "" {
		return protocol.ExecutionResult{}, fmt.Errorf("instance SystemID is required")
	}

	req := protocol.ExecuteRequest{
		Input: input,
		Mode:  mode,
	}

	endpoint := fmt.Sprintf(protocol.ExecutePath, url.PathEscape(key.ColonString()))

	if mode == "detach" {
		var response struct {
			Data protocol.ExecuteResponse `json:"data"`
		}
		if err := c.conn.Do(ctx, http.MethodPost, endpoint, req, &response); err != nil {
			return protocol.ExecutionResult{}, err
		}
		return protocol.ExecutionResult{
			ExecutionID: response.Data.ExecutionID,
			InstanceID:  response.Data.InstanceID,
			Status:      response.Data.Status,
		}, nil
	}

	var response struct {
		Data protocol.ExecutionResult `json:"data"`
	}
	if err := c.conn.Do(ctx, http.MethodPost, endpoint, req, &response); err != nil {
		return protocol.ExecutionResult{}, err
	}
	return response.Data, nil
}

// StreamExecutionEvents connects to the SSE stream for an execution and calls
// the emit callback for each StreamEvent received. It blocks until the stream
// ends or the context is cancelled.
func (c *Client) StreamExecutionEvents(ctx context.Context, instanceID string, executionID core.ID, emit func(protocol.StreamEvent) error) error {
	endpoint := fmt.Sprintf(protocol.ExecutionEventsStreamPath, instanceID, executionID)

	return c.conn.Stream(ctx, http.MethodGet, endpoint, nil, func(data []byte) error {
		var evt protocol.StreamEvent
		if err := json.Unmarshal(data, &evt); err != nil {
			return fmt.Errorf("unmarshal stream event: %w", err)
		}
		return emit(evt)
	})
}

// StreamExecutionEventsWS connects to the WebSocket stream for an execution and
// calls the emit callback for each StreamEvent received. It subscribes to the
// execution's room and blocks until the stream ends or the context is
// cancelled. It returns connection.ErrWebSocketUnavailable when the underlying
// connection cannot open a WebSocket session.
func (c *Client) StreamExecutionEventsWS(ctx context.Context, instanceID string, executionID core.ID, emit func(protocol.StreamEvent) error) error {
	if instanceID == "" {
		return fmt.Errorf("instance ID is required")
	}
	if executionID == "" {
		return fmt.Errorf("execution ID is required")
	}

	stream, err := c.conn.OpenWebSocket(ctx, protocol.WebSocketPath)
	if err != nil {
		return err
	}
	defer stream.Close()

	room := wsmessage.ExecutionRoom(instanceID, string(executionID))
	requestID := core.NewID("req_")

	if err := stream.Subscribe(ctx, room, string(requestID)); err != nil {
		return err
	}

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		msg, err := stream.Receive(ctx)
		if err != nil {
			return err
		}

		switch msg.Type {
		case wsmessage.MessageError:
			if msg.Error != nil {
				return fmt.Errorf("nore websocket error: %s: %s", msg.Error.Code, msg.Error.Message)
			}
		case wsmessage.MessageEvent:
			var evt protocol.StreamEvent
			if err := json.Unmarshal(msg.Data, &evt); err != nil {
				return fmt.Errorf("unmarshal websocket event: %w", err)
			}
			if err := emit(evt); err != nil {
				return err
			}
		}
	}
}

// ListExecutions retrieves all executions recorded for the instance identified
// by instanceID. It returns an empty slice when the instance has no executions.
func (c *Client) ListExecutions(ctx context.Context, instanceID string) ([]protocol.ExecutionItem, error) {
	if instanceID == "" {
		return nil, fmt.Errorf("instance ID is required")
	}

	var response struct {
		Data []protocol.ExecutionItem `json:"data"`
	}

	endpoint := fmt.Sprintf(protocol.ExecutePath, instanceID)

	if err := c.conn.Do(ctx, http.MethodGet, endpoint, nil, &response); err != nil {
		return nil, err
	}

	return response.Data, nil
}

func (c *Client) GetInstanceById(ctx context.Context, instanceID string) (protocol.InstanceResponse, error) {
	if instanceID == "" {
		return protocol.InstanceResponse{}, fmt.Errorf("instance ID is required")
	}

	var response struct {
		Data protocol.InstanceResponse `json:"data"`
	}

	endpoint := fmt.Sprintf(protocol.InstanceByIDPath, instanceID)

	if err := c.conn.Do(ctx, http.MethodGet, endpoint, nil, &response); err != nil {
		return protocol.InstanceResponse{}, err
	}

	return response.Data, nil
}

// RemoveInstance stops and deletes the instance identified by target, which
// may be an instance ID (inst_*) or a colon-encoded system key.
func (c *Client) RemoveInstance(ctx context.Context, target string) error {
	if target == "" {
		return fmt.Errorf("instance target is required")
	}
	endpoint := fmt.Sprintf(protocol.InstanceByIDPath, url.PathEscape(target))
	return c.conn.Do(ctx, http.MethodDelete, endpoint, nil, nil)
}

// ClearInstances stops and deletes every tracked instance.
func (c *Client) ClearInstances(ctx context.Context) error {
	return c.conn.Do(ctx, http.MethodDelete, protocol.InstancesPath, nil, nil)
}
