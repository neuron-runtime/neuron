namespace Neuron.Executor;

/// <summary>
/// Asks N.O.R.E. to suspend the capability until a signal or timer fires.
/// </summary>
/// <remarks>
/// Suspend is not a stackful continuation. The invocation ends, and a later
/// fresh Execute carries a <see cref="ResumeInput"/> with the same logical
/// operation ID and a new attempt ID. A capability that suspends must
/// reconstruct its state from the input and the resume input, and must be
/// idempotent.
/// <para>
/// A handler may only return a suspend outcome when its
/// <see cref="InitializeResult.Features"/> advertises
/// <see cref="ExecutorConstants.SuspendFeature"/> (<c>"suspend"</c>). The SDK
/// rejects an unnegotiated suspend explicitly instead of returning an empty
/// success.
/// </para>
/// </remarks>
public sealed class SuspendOutcome
{
    /// <summary>
    /// Identifies this wait. It is required when more than one concurrent wait
    /// on the same signal name can exist in one execution.
    /// </summary>
    public string CorrelationKey { get; init; } = string.Empty;

    /// <summary>
    /// The kind of wait. Use <see cref="ExecutorConstants.WaitTypeSignal"/> or
    /// <see cref="ExecutorConstants.WaitTypeTimer"/>.
    /// </summary>
    public string WaitType { get; init; } = string.Empty;

    /// <summary>
    /// Names the signal that resumes a signal wait. It is empty for a timer
    /// wait.
    /// </summary>
    public string ExpectedSignal { get; init; } = string.Empty;

    /// <summary>
    /// The wait deadline in Unix milliseconds. Zero means the wait has no
    /// deadline. A timer wait requires a positive value.
    /// </summary>
    public long DeadlineUnixMs { get; init; }

    /// <summary>
    /// Optionally names the schema the resume payload must match.
    /// </summary>
    public string PayloadSchema { get; init; } = string.Empty;
}
