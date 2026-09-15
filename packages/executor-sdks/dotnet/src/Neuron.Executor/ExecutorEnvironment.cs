namespace Neuron.Executor;

/// <summary>
/// Reads the executor transport environment injected by the N.O.R.E. process
/// runtime, mirroring the semantics of <see cref="ExecutorConstants"/>.
/// </summary>
internal static class ExecutorEnvironment
{
    public static string SocketPath =>
        Environment.GetEnvironmentVariable(ExecutorConstants.EnvSocket) ?? string.Empty;

    public static string ReadyFile =>
        Environment.GetEnvironmentVariable(ExecutorConstants.EnvReady) ?? string.Empty;

    public static string Protocol =>
        Environment.GetEnvironmentVariable(ExecutorConstants.EnvProtocol) ?? string.Empty;

    public static string Type =>
        Environment.GetEnvironmentVariable(ExecutorConstants.EnvType) ?? string.Empty;

    public static string Version =>
        Environment.GetEnvironmentVariable(ExecutorConstants.EnvVersion) ?? string.Empty;
}