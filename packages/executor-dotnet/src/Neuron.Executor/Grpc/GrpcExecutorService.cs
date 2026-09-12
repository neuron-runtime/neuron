using Grpc.Core;
using Neuron.Executor.Conversion;
using Neuron.Executor.V1;

namespace Neuron.Executor.Grpc;

/// <summary>
/// Coordinates graceful shutdown. The gRPC Shutdown handler signals the
/// coordinator; the server loop observes it and stops the host gracefully,
/// mirroring the Go SDK's shutdown channel.
/// </summary>
internal sealed class ShutdownCoordinator
{
    private readonly TaskCompletionSource _requested =
        new(TaskCreationOptions.RunContinuationsAsynchronously);

    /// <summary>Completes when <see cref="RequestShutdown"/> is called.</summary>
    public Task ShutdownRequested => _requested.Task;

    /// <summary>Signals that graceful shutdown was requested.</summary>
    public void RequestShutdown() => _requested.TrySetResult();
}

/// <summary>
/// Adapts the <see cref="ExecutorHandler"/> to the generated
/// <see cref="ExecutorService.ExecutorServiceBase"/> contract. The failure
/// semantics mirror <c>packages/executor-go/executor.go</c>: an exception from
/// Execute becomes a controlled failure in the response; an exception from
/// Initialize or Shutdown becomes a transport failure.
/// </summary>
internal sealed class GrpcExecutorService : ExecutorService.ExecutorServiceBase
{
    private readonly ExecutorHandler _handler;
    private readonly ShutdownCoordinator _shutdown;

    public GrpcExecutorService(ExecutorHandler handler, ShutdownCoordinator shutdown)
    {
        _handler = handler;
        _shutdown = shutdown;
    }

    public override async Task<InitializeResponse> Initialize(InitializeRequest request, ServerCallContext context)
    {
        InitializeResult result = await _handler.Initialize!(
            request.ProtocolVersion,
            request.Metadata,
            context.CancellationToken);

        ArgumentNullException.ThrowIfNull(result);

        var response = new InitializeResponse();
        if (!string.IsNullOrEmpty(result.ProtocolVersion))
        {
            response.ProtocolVersion = result.ProtocolVersion;
        }
        else
        {
            // Default protocol version if not specified.
            response.ProtocolVersion = ExecutorConstants.ProtocolV1;
        }

        response.Capabilities.AddRange(result.Capabilities);
        foreach (var (key, value) in result.Metadata)
        {
            response.Metadata[key] = value;
        }

        return response;
    }

    public override async Task<ExecuteResponse> Execute(ExecuteRequest request, ServerCallContext context)
    {
        var input = ValueConverter.ToObjectDictionary(request.Input);
        var executionContext = new ExecuteContext
        {
            ExecutionId = request.ExecutionId,
            TimeoutMs = request.TimeoutMs,
            CorrelationId = request.CorrelationId,
        };

        ExecutionResult result = await _handler.Execute!(
            input, executionContext, context.CancellationToken);
        ArgumentNullException.ThrowIfNull(result);

        if (!string.IsNullOrEmpty(result.Error))
        {
            // Controlled failure: successful transport, non-empty error payload.
            return new ExecuteResponse { Error = result.Error };
        }

        var response = new ExecuteResponse();
        foreach (var (key, value) in result.Output)
        {
            response.Output[key] = ValueConverter.ToValue(value);
        }

        return response;
    }

    public override async Task<HealthResponse> Health(HealthRequest request, ServerCallContext context)
    {
        if (_handler.Health is not null)
        {
            try
            {
                await _handler.Health(context.CancellationToken);
            }
            catch (Exception ex)
            {
                return new HealthResponse { Ready = false, Message = ex.Message };
            }
        }

        return new HealthResponse { Ready = true };
    }

    public override async Task<ShutdownResponse> Shutdown(ShutdownRequest request, ServerCallContext context)
    {
        if (_handler.Shutdown is not null)
        {
            await _handler.Shutdown(context.CancellationToken);
        }

        _shutdown.RequestShutdown();
        return new ShutdownResponse();
    }
}