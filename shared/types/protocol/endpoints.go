package protocol

const (
	HealthPath                = "/health"
	InstancesPath             = "/v1/instances"
	InstanceByIDPath          = "/v1/instances/%s"
	ExecutePath               = "/v1/instances/%s/executions"
	ExecutionEventsPath       = "/v1/instances/%s/executions/%s/events"
	ExecutionEventsStreamPath = "/v1/instances/%s/executions/%s/events/stream"

	WebSocketPath = "/v1/ws"

	RegisterPath = "/v1/register"
)
