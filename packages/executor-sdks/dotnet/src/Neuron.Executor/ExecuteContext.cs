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

    /// <summary>
    /// The per-invocation attempt identifier. A resumed invocation receives a
    /// new attempt ID.
    /// </summary>
    public string AttemptId { get; init; } = string.Empty;

    /// <summary>
    /// A stable identifier for the logical operation. It is preserved across a
    /// suspend and its resume.
    /// </summary>
    public string LogicalOperationId { get; init; } = string.Empty;

    /// <summary>
    /// The resume input when this invocation resumes a suspended capability, or
    /// <c>null</c> for a first invocation.
    /// </summary>
    public ResumeInput? Resume { get; init; }
}