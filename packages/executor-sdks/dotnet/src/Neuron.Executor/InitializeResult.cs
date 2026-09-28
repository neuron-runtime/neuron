namespace Neuron.Executor;

/// <summary>
/// Carries the executor's response to protocol negotiation.
/// </summary>
public sealed class InitializeResult
{
    /// <summary>
    /// The protocol version the executor supports. When empty, the SDK
    /// reports the canonical <c>neuron/capability-runtime-v1</c> protocol.
    /// </summary>
    public string ProtocolVersion { get; init; } = string.Empty;

    /// <summary>
    /// Declared features of the executor (for example
    /// <c>"concurrent"</c>, <c>"streaming"</c>, <c>"cancellation"</c>).
    /// </summary>
    public IReadOnlyList<string> Features { get; init; } = Array.Empty<string>();

    /// <summary>
    /// Executor-specific identity information returned to the runtime.
    /// </summary>
    public IReadOnlyDictionary<string, string> Metadata { get; init; } =
        new Dictionary<string, string>();
}