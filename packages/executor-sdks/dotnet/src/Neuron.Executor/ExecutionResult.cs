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

    /// <summary>
    /// When set, asks N.O.R.E. to suspend the capability instead of completing
    /// it. <see cref="Result"/> must be empty and <see cref="Error"/> must be
    /// unset when <see cref="Suspend"/> is set.
    /// </summary>
    /// <remarks>
    /// The handler must advertise <see cref="ExecutorConstants.SuspendFeature"/>
    /// from Initialize. The SDK rejects an unnegotiated suspend explicitly
    /// rather than treating it as an empty successful result.
    /// </remarks>
    public SuspendOutcome? Suspend { get; init; }
}