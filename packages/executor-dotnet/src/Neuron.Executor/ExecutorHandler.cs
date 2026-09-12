namespace Neuron.Executor;

/// <summary>
/// Business logic of an executor. It is the contract executor authors
/// implement; the SDK wraps it with the <c>neuron/executor-v1</c> gRPC
/// transport and lifecycle management.
/// </summary>
public sealed class ExecutorHandler
{
    /// <summary>
    /// Called once per process lifetime during the Initialize handshake. It
    /// negotiates the protocol version and returns the executor's identity.
    /// Implementations that reject the requested protocol should throw.
    /// </summary>
    public Func<string, IReadOnlyDictionary<string, string>, CancellationToken, ValueTask<InitializeResult>>? Initialize { get; set; }

    /// <summary>
    /// Runs one execution and returns the result. The input is the resolved
    /// execution input for the service. Returning a result with a non-empty
    /// <see cref="ExecutionResult.Error"/> records a controlled failure.
    /// </summary>
    public Func<IReadOnlyDictionary<string, object?>, ExecuteContext, CancellationToken, ValueTask<ExecutionResult>>? Execute { get; set; }

    /// <summary>
    /// Reports whether the executor is ready to accept requests. Called
    /// periodically by the runtime. When absent, the executor always reports
    /// healthy.
    /// </summary>
    public Func<CancellationToken, ValueTask>? Health { get; set; }

    /// <summary>
    /// Performs graceful cleanup before termination. Called once during the
    /// Shutdown handshake; the runtime forcefully terminates the process if
    /// shutdown exceeds its deadline.
    /// </summary>
    public Func<CancellationToken, ValueTask>? Shutdown { get; set; }
}