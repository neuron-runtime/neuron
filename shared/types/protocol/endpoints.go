package protocol

const (
	HealthPath                = "/health"
	InstancesPath             = "/v1/instances"
	InstanceByIDPath          = "/v1/instances/%s"
	ExecutePath               = "/v1/instances/%s/executions"
	ExecutionEventsStreamPath = "/v1/instances/%s/executions/%s/events/stream"
	CancelExecutionPath       = "/v1/instances/%s/executions/%s/cancel"

	WebSocketPath = "/v1/ws"

	RegisterPath = "/v1/register"
)
