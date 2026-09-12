namespace Neuron.Executor.Tests;

public class HandlerValidationTests : IDisposable
{
    private static readonly string[] EscapedVariables =
    {
        "NEURON_EXECUTOR_SOCKET",
        "NEURON_EXECUTOR_READY",
        "NEURON_EXECUTOR_PROTOCOL",
    };

    public void Dispose()
    {
        foreach (var variable in EscapedVariables)
        {
            Environment.SetEnvironmentVariable(variable, null);
        }
    }

    [Fact]
    public async Task RunAsync_Rejects_Null_Handler()
    {
        await Assert.ThrowsAsync<ArgumentNullException>(() => ExecutorServer.RunAsync(null!));
    }

    [Fact]
    public async Task RunAsync_Requires_Initialize_And_Execute()
    {
        var handler = new ExecutorHandler();
        await Assert.ThrowsAsync<ArgumentException>(() => ExecutorServer.RunAsync(handler));
    }

    [Fact]
    public async Task RunAsync_Fails_Without_Socket_Environment()
    {
        var handler = new ExecutorHandler
        {
            Initialize = (_, _, _) => new ValueTask<InitializeResult>(new InitializeResult()),
            Execute = (_, _, _) => new ValueTask<ExecutionResult>(new ExecutionResult()),
        };

        Environment.SetEnvironmentVariable("NEURON_EXECUTOR_SOCKET", null);

        await Assert.ThrowsAsync<ExecutorConfigurationException>(() => ExecutorServer.RunAsync(handler));
    }

    [Fact]
    public async Task RunAsync_Rejects_Unsupported_Declared_Protocol()
    {
        var handler = new ExecutorHandler
        {
            Initialize = (_, _, _) => new ValueTask<InitializeResult>(new InitializeResult()),
            Execute = (_, _, _) => new ValueTask<ExecutionResult>(new ExecutionResult()),
        };

        Environment.SetEnvironmentVariable("NEURON_EXECUTOR_SOCKET", "/tmp/neuron-test.sock");
        Environment.SetEnvironmentVariable("NEURON_EXECUTOR_PROTOCOL", "neuron/executor-v1-json");

        await Assert.ThrowsAsync<ExecutorConfigurationException>(() => ExecutorServer.RunAsync(handler));
    }

    [Fact]
    public async Task RunAsync_Accepts_Unset_Protocol_As_Canonical()
    {
        var dir = Path.Combine(Path.GetTempPath(), "neuron-executor-host", Guid.NewGuid().ToString("N"));
        var socket = Path.Combine(dir, "executor.sock");
        var ready = Path.Combine(dir, "ready");

        var handler = new ExecutorHandler
        {
            Initialize = (_, _, _) => new ValueTask<InitializeResult>(new InitializeResult()),
            Execute = (_, _, _) => new ValueTask<ExecutionResult>(new ExecutionResult()),
        };

        Environment.SetEnvironmentVariable("NEURON_EXECUTOR_SOCKET", socket);
        Environment.SetEnvironmentVariable("NEURON_EXECUTOR_READY", ready);
        Environment.SetEnvironmentVariable("NEURON_EXECUTOR_PROTOCOL", null);

        using var cts = new CancellationTokenSource();
        var task = ExecutorServer.RunAsync(handler, cts.Token);
        try
        {
            // The server should start and signal readiness.
            var deadline = DateTime.UtcNow.AddSeconds(30);
            while (!File.Exists(ready))
            {
                Assert.True(DateTime.UtcNow < deadline, "executor did not signal readiness");
                await Task.Delay(20);
            }
        }
        finally
        {
            cts.Cancel();
        }

        // Cancellation degrades into a graceful shutdown with exit code zero.
        Assert.Equal(0, await task);
    }
}