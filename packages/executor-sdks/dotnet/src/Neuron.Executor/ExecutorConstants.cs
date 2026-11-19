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
    public const string ProtocolV1 = "neuron/capability-runtime-v1";

    /// <summary>Legacy stdin/stdout JSON protocol (not implemented by this SDK).</summary>
    public const string ProtocolJSONV1 = "neuron/capability-runtime-v1-json";

    /// <summary>
    /// The InitializeResponse.features value an executor advertises to opt into
    /// the additive suspend/resume extension.
    /// </summary>
    public const string SuspendFeature = "suspend";

    /// <summary>A suspend wait that is resumed by a named signal.</summary>
    public const string WaitTypeSignal = "signal";

    /// <summary>A suspend wait that is resumed by a timer deadline.</summary>
    public const string WaitTypeTimer = "timer";

    /// <summary>A resume caused by a signal delivery.</summary>
    public const string ResumeCauseSignal = "signal";

    /// <summary>A resume caused by a timer firing.</summary>
    public const string ResumeCauseTimer = "timer";

    /// <summary>A resume caused by the wait deadline policy.</summary>
    public const string ResumeCauseDeadline = "deadline";

    /// <summary>Declares the protocol version the runtime expects.</summary>
    public const string EnvProtocol = "NEURON_CAPABILITY_RUNTIME_PROTOCOL";

    /// <summary>The executor type (logical name) being executed.</summary>
    public const string EnvType = "NEURON_CAPABILITY_RUNTIME_TYPE";

    /// <summary>The exact resolved version of the executor.</summary>
    public const string EnvVersion = "NEURON_CAPABILITY_RUNTIME_VERSION";

    /// <summary>Unix socket path the runtime expects the gRPC server to bind.</summary>
    public const string EnvSocket = "NEURON_CAPABILITY_RUNTIME_SOCKET";

    /// <summary>File the executor writes once its gRPC server accepts connections.</summary>
    public const string EnvReady = "NEURON_CAPABILITY_RUNTIME_READY";
}