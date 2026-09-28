namespace Neuron.Executor;

/// <summary>
/// The result of one executor execution.
/// </summary>
public sealed class ExecutionResult
{
    /// <summary>
    /// The execution result. Keys map to the capability's declared result fields.
    /// </summary>
    public IReadOnlyDictionary<string, object?> Result { get; init; } =
        new Dictionary<string, object?>();

    /// <summary>
    /// When non-empty, records a controlled failure. The request still returns
    /// successfully over the transport; the runtime treats a non-empty <see cref="Error"/>
    /// as an execution failure and surfaces it downstream.
    /// </summary>
    public string? Error { get; init; }
}