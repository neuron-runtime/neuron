using System.Collections.Concurrent;
using System.Net;
using System.Net.Sockets;
using Grpc.Net.Client;
using Neuron.Executor.Conversion;
using Neuron.Executor.Protocol.V1;
using ProtoResumeInput = Neuron.Executor.Protocol.V1.ResumeInput;
using SdkSuspendOutcome = Neuron.Executor.SuspendOutcome;

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
        Environment.SetEnvironmentVariable(ExecutorConstants.EnvSocket, _socketPath);
        Environment.SetEnvironmentVariable(ExecutorConstants.EnvReady, _readyFile);
        Environment.SetEnvironmentVariable(ExecutorConstants.EnvProtocol, ExecutorConstants.ProtocolV1);
        return Task.CompletedTask;
    }

    public async Task DisposeAsync()
    {
        Environment.SetEnvironmentVariable(ExecutorConstants.EnvSocket, null);
        Environment.SetEnvironmentVariable(ExecutorConstants.EnvReady, null);
        Environment.SetEnvironmentVariable(ExecutorConstants.EnvProtocol, null);

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
                new ExecutionResult { Result = input }),
        });

        var client = await ConnectAsync();
        var response = await client.InitializeAsync(new InitializeRequest
        {
            ProtocolVersion = "neuron/capability-runtime-v1",
            Metadata = { ["executor_type"] = "example:echo", ["executor_version"] = "1.0.0" },
        });

        Assert.Equal("neuron/capability-runtime-v1", response.ProtocolVersion);

        await RequestShutdownAsync(client);
    }

    [Fact]
    public async Task Execute_Round_Trips_Dynamic_Values()
    {
        StartServer(new ExecutorHandler
        {
            Initialize = (_, _, _) => new ValueTask<InitializeResult>(new InitializeResult()),
            Execute = (input, _, _) => new ValueTask<ExecutionResult>(
                new ExecutionResult { Result = input }),
        });

        var client = await ConnectAsync();
        await client.InitializeAsync(new InitializeRequest { ProtocolVersion = "neuron/capability-runtime-v1" });

        var request = new ExecuteRequest
        {
            ExecutionId = "exec-1",
            TimeoutMs = 5000,
            CorrelationId = "corr-1",
        };
        request.Params["name"] = ValueConverter.ToValue("Ada");
        request.Params["count"] = ValueConverter.ToValue(42);
        request.Params["ratio"] = ValueConverter.ToValue(0.5);
        request.Params["flag"] = ValueConverter.ToValue(true);
        request.Params["nothing"] = ValueConverter.ToValue(null);
        request.Params["items"] = ValueConverter.ToValue(new object?[] { 1L });

        var response = await client.ExecuteAsync(request);

        Assert.Empty(response.Error);
        Assert.Equal("Ada", response.Result["name"].StringValue);
        Assert.Equal(42L, response.Result["count"].NumberValue);
        Assert.Equal(0.5, response.Result["ratio"].NumberValue);
        Assert.True(response.Result["flag"].BoolValue);
        Assert.Equal(Value.KindOneofCase.NullValue, response.Result["nothing"].KindCase);
        Assert.Equal(1.0, response.Result["items"].ListValue.Values[0].NumberValue);

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
        await client.InitializeAsync(new InitializeRequest { ProtocolVersion = "neuron/capability-runtime-v1" });

        var response = await client.ExecuteAsync(new ExecuteRequest { ExecutionId = "exec-1" });

        Assert.Equal("threshold not met", response.Error);
        Assert.Empty(response.Result);

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

    [Fact]
    public async Task Suspend_OldClient_NoFeature_Returns_Normal_Result()
    {
        StartServer(new ExecutorHandler
        {
            Initialize = (_, _, _) => new ValueTask<InitializeResult>(new InitializeResult()),
            Execute = (input, context, _) => new ValueTask<ExecutionResult>(new ExecutionResult
            {
                Result = new Dictionary<string, object?>
                {
                    ["echo"] = input["value"],
                    ["resume"] = context.Resume is not null,
                },
            }),
        });

        var client = await ConnectAsync();
        var init = await client.InitializeAsync(new InitializeRequest
        {
            ProtocolVersion = ExecutorConstants.ProtocolV1,
        });
        Assert.DoesNotContain(ExecutorConstants.SuspendFeature, init.Features);

        var request = new ExecuteRequest();
        request.Params["value"] = ValueConverter.ToValue("old-client");
        var response = await client.ExecuteAsync(request);

        Assert.Null(response.Suspend);
        Assert.Empty(response.Error);
        Assert.Equal("old-client", response.Result["echo"].StringValue);
        Assert.False(response.Result["resume"].BoolValue);

        await RequestShutdownAsync(client);
    }

    [Fact]
    public async Task Suspend_Negotiated_Returns_Suspend_Outcome()
    {
        StartServer(new ExecutorHandler
        {
            Initialize = (_, _, _) => new ValueTask<InitializeResult>(new InitializeResult
            {
                Features = new[] { ExecutorConstants.SuspendFeature },
            }),
            Execute = (_, _, _) => new ValueTask<ExecutionResult>(new ExecutionResult
            {
                Suspend = new SdkSuspendOutcome
                {
                    CorrelationKey = "corr-1",
                    WaitType = ExecutorConstants.WaitTypeSignal,
                    ExpectedSignal = "approval",
                    DeadlineUnixMs = 4242,
                    PayloadSchema = "approval/v1",
                },
            }),
        });

        var client = await ConnectAsync();
        var init = await client.InitializeAsync(new InitializeRequest
        {
            ProtocolVersion = ExecutorConstants.ProtocolV1,
        });
        Assert.Contains(ExecutorConstants.SuspendFeature, init.Features);

        var response = await client.ExecuteAsync(new ExecuteRequest());

        Assert.Empty(response.Error);
        Assert.NotNull(response.Suspend);
        Assert.Equal("corr-1", response.Suspend.CorrelationKey);
        Assert.Equal(ExecutorConstants.WaitTypeSignal, response.Suspend.WaitType);
        Assert.Equal("approval", response.Suspend.ExpectedSignal);
        Assert.Equal(4242, response.Suspend.DeadlineUnixMs);
        Assert.Equal("approval/v1", response.Suspend.PayloadSchema);
        Assert.Empty(response.Result);

        await RequestShutdownAsync(client);
    }

    [Fact]
    public async Task Suspend_Unnegotiated_Is_Rejected()
    {
        StartServer(new ExecutorHandler
        {
            Initialize = (_, _, _) => new ValueTask<InitializeResult>(new InitializeResult()),
            Execute = (_, _, _) => new ValueTask<ExecutionResult>(new ExecutionResult
            {
                Suspend = new SdkSuspendOutcome
                {
                    WaitType = ExecutorConstants.WaitTypeSignal,
                    ExpectedSignal = "approval",
                },
            }),
        });

        var client = await ConnectAsync();
        var init = await client.InitializeAsync(new InitializeRequest
        {
            ProtocolVersion = ExecutorConstants.ProtocolV1,
        });
        Assert.DoesNotContain(ExecutorConstants.SuspendFeature, init.Features);

        var response = await client.ExecuteAsync(new ExecuteRequest());

        Assert.Null(response.Suspend);
        Assert.Contains("unsupported_suspend_outcome", response.Error);
        Assert.Empty(response.Result);

        await RequestShutdownAsync(client);
    }

    [Fact]
    public async Task Suspend_Signal_Resume_Uses_Stable_Logical_Operation_And_New_Attempt()
    {
        int calls = 0;
        StartServer(new ExecutorHandler
        {
            Initialize = (_, _, _) => new ValueTask<InitializeResult>(new InitializeResult
            {
                Features = new[] { ExecutorConstants.SuspendFeature },
            }),
            Execute = (_, context, _) =>
            {
                if (Interlocked.Increment(ref calls) == 1)
                {
                    return new ValueTask<ExecutionResult>(new ExecutionResult
                    {
                        Suspend = new SdkSuspendOutcome
                        {
                            CorrelationKey = "corr-1",
                            WaitType = ExecutorConstants.WaitTypeSignal,
                            ExpectedSignal = "approval",
                        },
                    });
                }

                if (context.Resume is null)
                {
                    return new ValueTask<ExecutionResult>(
                        new ExecutionResult { Error = "missing resume input" });
                }

                return new ValueTask<ExecutionResult>(new ExecutionResult
                {
                    Result = new Dictionary<string, object?>
                    {
                        ["cause"] = context.Resume.Cause,
                        ["signal"] = context.Resume.SignalName,
                        ["delivery"] = context.Resume.DeliveryId,
                        ["by"] = context.Resume.Payload.TryGetValue("by", out var by) ? by : null,
                        ["logical"] = context.LogicalOperationId,
                        ["attempt"] = context.AttemptId,
                    },
                });
            },
        });

        var client = await ConnectAsync();
        await client.InitializeAsync(new InitializeRequest
        {
            ProtocolVersion = ExecutorConstants.ProtocolV1,
        });

        var suspended = await client.ExecuteAsync(new ExecuteRequest
        {
            LogicalOperationId = "logical-1",
            AttemptId = "attempt-1",
        });
        Assert.NotNull(suspended.Suspend);
        Assert.Empty(suspended.Error);

        var resumed = await client.ExecuteAsync(new ExecuteRequest
        {
            LogicalOperationId = "logical-1",
            AttemptId = "attempt-2",
            Resume = new ProtoResumeInput
            {
                Cause = ExecutorConstants.ResumeCauseSignal,
                SignalName = "approval",
                DeliveryId = "delivery-1",
                Payload = { ["by"] = ValueConverter.ToValue("ada") },
            },
        });

        Assert.Empty(resumed.Error);
        Assert.Equal(ExecutorConstants.ResumeCauseSignal, resumed.Result["cause"].StringValue);
        Assert.Equal("approval", resumed.Result["signal"].StringValue);
        Assert.Equal("delivery-1", resumed.Result["delivery"].StringValue);
        Assert.Equal("ada", resumed.Result["by"].StringValue);
        Assert.Equal("logical-1", resumed.Result["logical"].StringValue);
        Assert.Equal("attempt-2", resumed.Result["attempt"].StringValue);

        await RequestShutdownAsync(client);
    }

    [Fact]
    public async Task Suspend_Timer_Resume_Carries_Deadline()
    {
        int calls = 0;
        StartServer(new ExecutorHandler
        {
            Initialize = (_, _, _) => new ValueTask<InitializeResult>(new InitializeResult
            {
                Features = new[] { ExecutorConstants.SuspendFeature },
            }),
            Execute = (_, context, _) =>
            {
                if (Interlocked.Increment(ref calls) == 1)
                {
                    return new ValueTask<ExecutionResult>(new ExecutionResult
                    {
                        Suspend = new SdkSuspendOutcome
                        {
                            WaitType = ExecutorConstants.WaitTypeTimer,
                            DeadlineUnixMs = 1720000000000,
                        },
                    });
                }

                if (context.Resume is null)
                {
                    return new ValueTask<ExecutionResult>(
                        new ExecutionResult { Error = "missing resume input" });
                }

                return new ValueTask<ExecutionResult>(new ExecutionResult
                {
                    Result = new Dictionary<string, object?>
                    {
                        ["cause"] = context.Resume.Cause,
                    },
                });
            },
        });

        var client = await ConnectAsync();
        await client.InitializeAsync(new InitializeRequest
        {
            ProtocolVersion = ExecutorConstants.ProtocolV1,
        });

        var suspended = await client.ExecuteAsync(new ExecuteRequest
        {
            LogicalOperationId = "timer-op",
        });
        Assert.NotNull(suspended.Suspend);
        Assert.Equal(ExecutorConstants.WaitTypeTimer, suspended.Suspend.WaitType);
        Assert.True(suspended.Suspend.DeadlineUnixMs > 0);

        var resumed = await client.ExecuteAsync(new ExecuteRequest
        {
            LogicalOperationId = "timer-op",
            Resume = new ProtoResumeInput { Cause = ExecutorConstants.ResumeCauseTimer },
        });

        Assert.Empty(resumed.Error);
        Assert.Equal(ExecutorConstants.ResumeCauseTimer, resumed.Result["cause"].StringValue);

        await RequestShutdownAsync(client);
    }

    [Fact]
    public async Task Suspend_Duplicate_Resume_Delivery_Is_Delivered()
    {
        var seen = new ConcurrentDictionary<string, int>();
        StartServer(new ExecutorHandler
        {
            Initialize = (_, _, _) => new ValueTask<InitializeResult>(new InitializeResult
            {
                Features = new[] { ExecutorConstants.SuspendFeature },
            }),
            Execute = (_, context, _) =>
            {
                if (context.Resume is null)
                {
                    return new ValueTask<ExecutionResult>(
                        new ExecutionResult { Error = "missing resume input" });
                }

                var count = seen.AddOrUpdate(context.Resume.DeliveryId, 1, (_, current) => current + 1);
                return new ValueTask<ExecutionResult>(new ExecutionResult
                {
                    Result = new Dictionary<string, object?>
                    {
                        ["delivery"] = context.Resume.DeliveryId,
                        ["count"] = count,
                        ["logical"] = context.LogicalOperationId,
                    },
                });
            },
        });

        var client = await ConnectAsync();
        await client.InitializeAsync(new InitializeRequest
        {
            ProtocolVersion = ExecutorConstants.ProtocolV1,
        });

        ExecuteRequest Duplicate(string attempt) => new()
        {
            LogicalOperationId = "dup-op",
            AttemptId = attempt,
            Resume = new ProtoResumeInput
            {
                Cause = ExecutorConstants.ResumeCauseSignal,
                SignalName = "approval",
                DeliveryId = "duplicate-1",
                Payload = { ["by"] = ValueConverter.ToValue("ada") },
            },
        };

        var first = await client.ExecuteAsync(Duplicate("attempt-1"));
        var second = await client.ExecuteAsync(Duplicate("attempt-2"));

        Assert.Empty(first.Error);
        Assert.Empty(second.Error);
        Assert.Equal("duplicate-1", first.Result["delivery"].StringValue);
        Assert.Equal("duplicate-1", second.Result["delivery"].StringValue);
        Assert.Equal("dup-op", first.Result["logical"].StringValue);
        Assert.Equal("dup-op", second.Result["logical"].StringValue);
        Assert.Equal(1d, first.Result["count"].NumberValue);
        Assert.Equal(2d, second.Result["count"].NumberValue);

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

    private async Task<CapabilityRuntimeService.CapabilityRuntimeServiceClient> ConnectAsync()
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

        return new CapabilityRuntimeService.CapabilityRuntimeServiceClient(channel);
    }

    private async Task RequestShutdownAsync(CapabilityRuntimeService.CapabilityRuntimeServiceClient client)
    {
        await client.ShutdownAsync(new ShutdownRequest());
        await Task.WhenAny(_serverTask ?? Task.CompletedTask, Task.Delay(TimeSpan.FromSeconds(10)));
        Assert.Equal(0, await _serverTask!);
    }
}