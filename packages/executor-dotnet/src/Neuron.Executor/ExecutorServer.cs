using Microsoft.Extensions.Hosting;
using Neuron.Executor.Grpc;

namespace Neuron.Executor;

/// <summary>
/// Starts an executor and runs it until the runtime requests shutdown.
/// </summary>
/// <remarks>
/// <para>
/// Executors run as long-lived worker processes. The N.O.R.E. process runtime
/// launches the binary with a Unix socket path in <c>NEURON_EXECUTOR_SOCKET</c>,
/// waits for the ready file named in <c>NEURON_EXECUTOR_READY</c>, performs the
/// Initialize handshake, leases executions, and finally requests graceful
/// termination with Shutdown.
/// </para>
/// <para>
/// This SDK implements the <c>neuron/executor-v1</c> gRPC transport only. The
/// legacy <c>neuron/executor-v1-json</c> stdin/stdout transport is deliberately
/// not provided; attempting to run without <c>NEURON_EXECUTOR_SOCKET</c> or with
/// an unsupported declared protocol fails fast with an explicit error.
/// </para>
/// </remarks>
public static class ExecutorServer
{
    private static readonly TimeSpan ShutdownGracePeriod = TimeSpan.FromSeconds(10);

    /// <summary>
    /// Starts the executor gRPC server for <paramref name="handler"/> and blocks
    /// until the runtime requests shutdown or <paramref name="cancellationToken"/>
    /// is cancelled. Returns the process exit code: zero for a graceful
    /// termination, one on configuration failure.
    /// </summary>
    public static async Task<int> RunAsync(
        ExecutorHandler handler,
        CancellationToken cancellationToken = default)
    {
        ArgumentNullException.ThrowIfNull(handler);
        if (handler.Initialize is null || handler.Execute is null)
        {
            throw new ArgumentException(
                "ExecutorHandler.Initialize and ExecutorHandler.Execute are required.",
                nameof(handler));
        }

        var protocol = ExecutorEnvironment.Protocol;
        if (protocol.Length > 0 && protocol != ExecutorConstants.ProtocolV1)
        {
            throw new ExecutorConfigurationException(
                $"unsupported executor protocol {protocol}; " +
                $"this SDK implements {ExecutorConstants.ProtocolV1} only");
        }

        var socketPath = ExecutorEnvironment.SocketPath;
        if (socketPath.Length == 0)
        {
            throw new ExecutorConfigurationException(
                $"{ExecutorConstants.EnvSocket} is not set; the {ExecutorConstants.ProtocolV1} " +
                "transport requires a Unix domain socket. This executor must be launched by " +
                "the N.O.R.E. process runtime.");
        }

        PrepareSocketPath(socketPath);

        var shutdown = new ShutdownCoordinator();
        using var host = GrpcHost.Build(socketPath, new GrpcExecutorService(handler, shutdown));

        await host.StartAsync(cancellationToken);
        TouchReadyFile(ExecutorEnvironment.ReadyFile);

        // External cancellation and the gRPC Shutdown handshake both signal the
        // coordinator; either path ends in the same graceful host stop.
        using (cancellationToken.Register(shutdown.RequestShutdown))
        {
            await shutdown.ShutdownRequested;
        }

        await host.StopAsync(ShutdownGracePeriod);
        return 0;
    }

    /// <summary>
    /// Cleans a stale socket file so a previous crash does not block startup,
    /// and ensures the socket directory exists. Mirrors the Go SDK listener.
    /// </summary>
    private static void PrepareSocketPath(string socketPath)
    {
        var directory = Path.GetDirectoryName(socketPath);
        if (!string.IsNullOrEmpty(directory))
        {
            Directory.CreateDirectory(directory);
        }

        if (File.Exists(socketPath))
        {
            try
            {
                File.Delete(socketPath);
            }
            catch (Exception ex)
            {
                throw new ExecutorConfigurationException(
                    $"remove stale socket {socketPath}: {ex.Message}", ex);
            }
        }
    }

    /// <summary>
    /// Signals readiness to the runtime by creating the ready file, mirroring
    /// the Go SDK. Called only after the server is accepting connections.
    /// </summary>
    private static void TouchReadyFile(string readyFile)
    {
        if (readyFile.Length == 0)
        {
            return;
        }

        var directory = Path.GetDirectoryName(readyFile);
        if (!string.IsNullOrEmpty(directory))
        {
            Directory.CreateDirectory(directory);
        }

        using var stream = File.Open(readyFile, FileMode.Create, FileAccess.Write, FileShare.Read);
    }
}