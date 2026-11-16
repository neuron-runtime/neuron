using Grpc.Core;
using Neuron.Executor.Conversion;
using Neuron.Executor.Protocol.V1;
using ProtoResumeInput = Neuron.Executor.Protocol.V1.ResumeInput;
using ProtoSuspendOutcome = Neuron.Executor.Protocol.V1.SuspendOutcome;
using SdkResumeInput = Neuron.Executor.ResumeInput;
using SdkSuspendOutcome = Neuron.Executor.SuspendOutcome;

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
/// <see cref="CapabilityRuntimeService.CapabilityRuntimeServiceBase"/> contract. The failure
/// semantics mirror <c>packages/executor-sdks/golang/executor.go</c>: an exception from
/// Execute becomes a controlled failure in the response; an exception from
/// Initialize or Shutdown becomes a transport failure.
/// </summary>
internal sealed class GrpcExecutorService : CapabilityRuntimeService.CapabilityRuntimeServiceBase
{
    private readonly ExecutorHandler _handler;
    private readonly ShutdownCoordinator _shutdown;
    private volatile bool _suspendNegotiated;

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

        response.Features.AddRange(result.Features);
        foreach (var (key, value) in result.Metadata)
        {
            response.Metadata[key] = value;
        }

        // Remember the negotiated feature so Execute can reject a suspend the
        // runtime did not opt into. Suspend negotiation is monotonic across
        // repeated Initialize calls so a previously advertised capability is
        // not revoked.
        _suspendNegotiated |= result.Features.Contains(
            ExecutorConstants.SuspendFeature, StringComparer.Ordinal);

        return response;
    }

    public override async Task<ExecuteResponse> Execute(ExecuteRequest request, ServerCallContext context)
    {
        var input = ValueConverter.ToObjectDictionary(request.Params);
        var executionContext = new ExecuteContext
        {
            ExecutionId = request.ExecutionId,
            TimeoutMs = request.TimeoutMs,
            CorrelationId = request.CorrelationId,
            AttemptId = request.AttemptId,
            LogicalOperationId = request.LogicalOperationId,
            Resume = request.Resume is null ? null : FromProto(request.Resume),
        };

        ExecutionResult result = await _handler.Execute!(
            input, executionContext, context.CancellationToken);
        ArgumentNullException.ThrowIfNull(result);

        string? validationError = ValidateSuspendOutcome(result);
        if (validationError is not null)
        {
            return new ExecuteResponse { Error = validationError };
        }

        if (!string.IsNullOrEmpty(result.Error))
        {
            // Controlled failure: successful transport, non-empty error payload.
            return new ExecuteResponse { Error = result.Error };
        }

        if (result.Suspend is not null)
        {
            if (!_suspendNegotiated)
            {
                return new ExecuteResponse
                {
                    Error = "unsupported_suspend_outcome: capability runtime did not advertise the \"suspend\" feature during Initialize",
                };
            }

            return new ExecuteResponse { Suspend = ToProto(result.Suspend) };
        }

        var response = new ExecuteResponse();
        foreach (var (key, value) in result.Result)
        {
            response.Result[key] = ValueConverter.ToValue(value);
        }

        return response;
    }

    private static string? ValidateSuspendOutcome(ExecutionResult result)
    {
        if (result.Suspend is null)
        {
            return null;
        }

        if (result.Result.Count != 0 || !string.IsNullOrEmpty(result.Error))
        {
            return "unsupported_suspend_outcome: suspend must not be combined with a non-empty result or error";
        }

        if (result.Suspend.WaitType == ExecutorConstants.WaitTypeSignal)
        {
            if (string.IsNullOrEmpty(result.Suspend.ExpectedSignal))
            {
                return "unsupported_suspend_outcome: signal wait requires a non-empty expected_signal";
            }

            return null;
        }

        if (result.Suspend.WaitType == ExecutorConstants.WaitTypeTimer)
        {
            if (result.Suspend.DeadlineUnixMs <= 0)
            {
                return "unsupported_suspend_outcome: timer wait requires deadline_unix_ms > 0";
            }

            return null;
        }

        return "unsupported_suspend_outcome: wait_type must be \"signal\" or \"timer\"";
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

    private static SdkResumeInput FromProto(ProtoResumeInput input)
    {
        return new SdkResumeInput
        {
            Cause = input.Cause,
            SignalName = input.SignalName,
            DeliveryId = input.DeliveryId,
            Payload = ValueConverter.ToObjectDictionary(input.Payload),
        };
    }

    private static ProtoSuspendOutcome ToProto(SdkSuspendOutcome outcome)
    {
        return new ProtoSuspendOutcome
        {
            CorrelationKey = outcome.CorrelationKey,
            WaitType = outcome.WaitType,
            ExpectedSignal = outcome.ExpectedSignal,
            DeadlineUnixMs = outcome.DeadlineUnixMs,
            PayloadSchema = outcome.PayloadSchema,
        };
    }
}