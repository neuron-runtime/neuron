using System.Net;
using System.Net.Sockets;
using Grpc.Net.Client;
using Neuron.Executor.Conversion;
using Neuron.Executor.V1;

namespace Neuron.Executor.Tests;

public class GrpcServerTests : IAsyncLifetime
{
    private readonly string _socketPath;
    private readonly string _readyFile;
    private readonly List<GrpcChannel> _channels = new();
    private CancellationTokenSource? _serverCts;
    private Task<int>? _serverTask;

    public GrpcServerTests()
    {
        var dir = Path.Combine(Path.GetTempPath(), "neuron-executor-tests", Guid.NewGuid().ToString("N"));
        _socketPath = Path.Combine(dir, "executor.sock");
        _readyFile = Path.Combine(dir, "ready");
    }

    public Task InitializeAsync()
    {
        Environment.SetEnvironmentVariable("NEURON_EXECUTOR_SOCKET", _socketPath);
        Environment.SetEnvironmentVariable("NEURON_EXECUTOR_READY", _readyFile);
        Environment.SetEnvironmentVariable("NEURON_EXECUTOR_PROTOCOL", "neuron/executor-v1");
        return Task.CompletedTask;
    }

    public async Task DisposeAsync()
    {
        Environment.SetEnvironmentVariable("NEURON_EXECUTOR_SOCKET", null);
        Environment.SetEnvironmentVariable("NEURON_EXECUTOR_READY", null);
        Environment.SetEnvironmentVariable("NEURON_EXECUTOR_PROTOCOL", null);

        if (_channels.Count > 0)
        {
            foreach (var channel in _channels)
            {
                channel.Dispose();
            }
            _channels.Clear();
        }

        if (_serverCts is not null)
        {
            _serverCts.Cancel();
            await Task.WhenAny(_serverTask ?? Task.CompletedTask, Task.Delay(TimeSpan.FromSeconds(10)));
            _serverCts.Dispose();
        }
    }

    [Fact]
    public async Task Initialize_Defaults_To_Canonical_Protocol_Version()
    {
        StartServer(new ExecutorHandler
        {
            Initialize = (_, _, _) => new ValueTask<InitializeResult>(
                new InitializeResult { ProtocolVersion = string.Empty }),
            Execute = (input, _, _) => new ValueTask<ExecutionResult>(
                new ExecutionResult { Output = input }),
        });

        var client = await ConnectAsync();
        var response = await client.InitializeAsync(new InitializeRequest
        {
            ProtocolVersion = "neuron/executor-v1",
            Metadata = { ["executor_type"] = "example:echo", ["executor_version"] = "1.0.0" },
        });

        Assert.Equal("neuron/executor-v1", response.ProtocolVersion);

        await RequestShutdownAsync(client);
    }

    [Fact]
    public async Task Execute_Round_Trips_Dynamic_Values()
    {
        StartServer(new ExecutorHandler
        {
            Initialize = (_, _, _) => new ValueTask<InitializeResult>(new InitializeResult()),
            Execute = (input, _, _) => new ValueTask<ExecutionResult>(
                new ExecutionResult { Output = input }),
        });

        var client = await ConnectAsync();
        await client.InitializeAsync(new InitializeRequest { ProtocolVersion = "neuron/executor-v1" });

        var request = new ExecuteRequest
        {
            ExecutionId = "exec-1",
            TimeoutMs = 5000,
            CorrelationId = "corr-1",
        };
        request.Input["name"] = ValueConverter.ToValue("Ada");
        request.Input["count"] = ValueConverter.ToValue(42);
        request.Input["ratio"] = ValueConverter.ToValue(0.5);
        request.Input["flag"] = ValueConverter.ToValue(true);
        request.Input["nothing"] = ValueConverter.ToValue(null);
        request.Input["items"] = ValueConverter.ToValue(new object?[] { 1L });

        var response = await client.ExecuteAsync(request);

        Assert.Empty(response.Error);
        Assert.Equal("Ada", response.Output["name"].StringValue);
        Assert.Equal(42L, response.Output["count"].NumberValue);
        Assert.Equal(0.5, response.Output["ratio"].NumberValue);
        Assert.True(response.Output["flag"].BoolValue);
        Assert.Equal(Value.KindOneofCase.NullValue, response.Output["nothing"].KindCase);
        Assert.Equal(1.0, response.Output["items"].ListValue.Values[0].NumberValue);

        await RequestShutdownAsync(client);
    }

    [Fact]
    public async Task Execute_Controlled_Failure_Is_Not_Transport_Error()
    {
        StartServer(new ExecutorHandler
        {
            Initialize = (_, _, _) => new ValueTask<InitializeResult>(new InitializeResult()),
            Execute = (_, _, _) => new ValueTask<ExecutionResult>(
                new ExecutionResult { Error = "threshold not met" }),
        });

        var client = await ConnectAsync();
        await client.InitializeAsync(new InitializeRequest { ProtocolVersion = "neuron/executor-v1" });

        var response = await client.ExecuteAsync(new ExecuteRequest { ExecutionId = "exec-1" });

        Assert.Equal("threshold not met", response.Error);
        Assert.Empty(response.Output);

        await RequestShutdownAsync(client);
    }

    [Fact]
    public async Task Health_Reports_Ready_When_Handler_Healthy()
    {
        StartServer(new ExecutorHandler
        {
            Initialize = (_, _, _) => new ValueTask<InitializeResult>(new InitializeResult()),
            Execute = (_, _, _) => new ValueTask<ExecutionResult>(new ExecutionResult()),
            Health = _ => ValueTask.CompletedTask,
        });

        var client = await ConnectAsync();
        var response = await client.HealthAsync(new HealthRequest());

        Assert.True(response.Ready);
        await RequestShutdownAsync(client);
    }

    [Fact]
    public async Task Health_Reports_Not_Ready_When_Handler_Fails()
    {
        StartServer(new ExecutorHandler
        {
            Initialize = (_, _, _) => new ValueTask<InitializeResult>(new InitializeResult()),
            Execute = (_, _, _) => new ValueTask<ExecutionResult>(new ExecutionResult()),
            Health = _ => throw new InvalidOperationException("backend down"),
        });

        var client = await ConnectAsync();
        var response = await client.HealthAsync(new HealthRequest());

        Assert.False(response.Ready);
        Assert.Contains("backend down", response.Message);
        await RequestShutdownAsync(client);
    }

    private void StartServer(ExecutorHandler handler) => StartServerAsync(handler).GetAwaiter().GetResult();

    private async Task StartServerAsync(ExecutorHandler handler)
    {
        _serverCts = new CancellationTokenSource();
        _serverTask = Task.Run(
            () => ExecutorServer.RunAsync(handler, _serverCts.Token),
            _serverCts.Token);

        // Wait for the ready file that the SDK writes once the socket is bound.
        var deadline = DateTime.UtcNow.AddSeconds(30);
        while (!File.Exists(_readyFile))
        {
            if (_serverTask.IsCompleted)
            {
                // Surface the underlying server failure instead of timing out.
                await _serverTask;
                throw new InvalidOperationException("executor server exited before signaling readiness");
            }
            if (DateTime.UtcNow > deadline)
            {
                throw new TimeoutException("executor did not signal readiness");
            }
            Thread.Sleep(20);
        }
    }

    private async Task<ExecutorService.ExecutorServiceClient> ConnectAsync()
    {
        AppContext.SetSwitch("System.Net.Http.SocketsHttpHandler.Http2UnencryptedSupport", true);

        var handler = new SocketsHttpHandler
        {
            ConnectCallback = async (_, ct) =>
            {
                var socket = new Socket(AddressFamily.Unix, SocketType.Stream, ProtocolType.Unspecified);
                try
                {
                    await socket.ConnectAsync(new UnixDomainSocketEndPoint(_socketPath), ct);
                }
                catch
                {
                    socket.Dispose();
                    throw;
                }
                return new NetworkStream(socket, ownsSocket: true);
            },
        };

        var channel = GrpcChannel.ForAddress(
            "http://localhost",
            new GrpcChannelOptions { HttpHandler = handler });
        _channels.Add(channel);

        return new ExecutorService.ExecutorServiceClient(channel);
    }

    private async Task RequestShutdownAsync(ExecutorService.ExecutorServiceClient client)
    {
        await client.ShutdownAsync(new ShutdownRequest());
        await Task.WhenAny(_serverTask ?? Task.CompletedTask, Task.Delay(TimeSpan.FromSeconds(10)));
        Assert.Equal(0, await _serverTask!);
    }
}