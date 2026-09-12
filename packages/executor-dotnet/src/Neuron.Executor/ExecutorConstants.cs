namespace Neuron.Executor;

/// <summary>
/// Protocol identifiers and environment variables of the executor wire
/// contract. These mirror <c>shared/types/executor</c> in the Go module and
/// MUST NOT drift from it: the runtime injects the environment described here
/// and speaks the protocol named here.
/// </summary>
public static class ExecutorConstants
{
    /// <summary>Canonical wire protocol spoken over gRPC (Unix domain sockets).</summary>
    public const string ProtocolV1 = "neuron/executor-v1";

    /// <summary>Legacy stdin/stdout JSON protocol (not implemented by this SDK).</summary>
    public const string ProtocolJSONV1 = "neuron/executor-v1-json";

    /// <summary>Declares the protocol version the runtime expects.</summary>
    public const string EnvProtocol = "NEURON_EXECUTOR_PROTOCOL";

    /// <summary>The executor type (logical name) being executed.</summary>
    public const string EnvType = "NEURON_EXECUTOR_TYPE";

    /// <summary>The exact resolved version of the executor.</summary>
    public const string EnvVersion = "NEURON_EXECUTOR_VERSION";

    /// <summary>Unix socket path the runtime expects the gRPC server to bind.</summary>
    public const string EnvSocket = "NEURON_EXECUTOR_SOCKET";

    /// <summary>File the executor writes once its gRPC server accepts connections.</summary>
    public const string EnvReady = "NEURON_EXECUTOR_READY";
}