namespace Neuron.Executor;

/// <summary>
/// Per-execution context passed alongside the input of an execution.
/// </summary>
public sealed class ExecuteContext
{
    /// <summary>
    /// Unique identifier for this execution, useful for logging and correlation.
    /// </summary>
    public string ExecutionId { get; init; } = string.Empty;

    /// <summary>
    /// Maximum time the executor should spend on this execution in milliseconds.
    /// A value of zero means no timeout.
    /// </summary>
    public long TimeoutMs { get; init; }

    /// <summary>
    /// Links this execution to a parent execution or request chain.
    /// </summary>
    public string CorrelationId { get; init; } = string.Empty;
}