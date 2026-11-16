namespace Neuron.Executor;

/// <summary>
/// Carries the reason a suspended capability is being resumed.
/// </summary>
/// <remarks>
/// A resumed invocation is a fresh Execute with the same logical operation ID
/// as the suspended invocation and a new attempt ID. Capabilities must dedupe
/// on <see cref="DeliveryId"/>: the same delivery can be presented more than
/// once.
/// </remarks>
public sealed class ResumeInput
{
    /// <summary>
    /// The resume cause. Use <see cref="ExecutorConstants.ResumeCauseSignal"/>,
    /// <see cref="ExecutorConstants.ResumeCauseTimer"/>, or
    /// <see cref="ExecutorConstants.ResumeCauseDeadline"/>.
    /// </summary>
    public string Cause { get; init; } = string.Empty;

    /// <summary>
    /// Names the signal that resumed a signal wait.
    /// </summary>
    public string SignalName { get; init; } = string.Empty;

    /// <summary>
    /// The client-supplied idempotency key for the delivery. The same delivery
    /// can be presented more than once; capabilities must dedupe on it.
    /// </summary>
    public string DeliveryId { get; init; } = string.Empty;

    /// <summary>
    /// The signal payload. It is empty for timer and deadline resumes.
    /// </summary>
    public IReadOnlyDictionary<string, object?> Payload { get; init; } =
        new Dictionary<string, object?>();
}
