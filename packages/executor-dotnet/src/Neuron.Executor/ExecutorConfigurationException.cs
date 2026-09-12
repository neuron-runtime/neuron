namespace Neuron.Executor;

/// <summary>
/// Thrown when the executor process environment does not satisfy the
/// <c>neuron/executor-v1</c> transport contract, for example when it is
/// launched without <c>NEURON_EXECUTOR_SOCKET</c>.
/// </summary>
public sealed class ExecutorConfigurationException : Exception
{
    /// <summary>
    /// Initializes a new exception with the configuration error message.
    /// </summary>
    public ExecutorConfigurationException(string message) : base(message) { }

    /// <summary>
    /// Initializes a new exception with the configuration error message and an
    /// inner cause.
    /// </summary>
    public ExecutorConfigurationException(string message, Exception innerException)
        : base(message, innerException) { }
}