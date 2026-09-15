using Microsoft.AspNetCore.Builder;
using Microsoft.AspNetCore.Hosting;
using Microsoft.AspNetCore.Server.Kestrel.Core;
using Microsoft.Extensions.DependencyInjection;
using Microsoft.Extensions.Logging;

namespace Neuron.Executor.Grpc;

/// <summary>
/// Builds the gRPC host that listens on a Unix domain socket using HTTP/2,
/// the transport N.O.R.E. connects to for <c>neuron/executor-v1</c>.
/// </summary>
internal static class GrpcHost
{
    public static WebApplication Build(string socketPath, GrpcExecutorService service)
    {
        var builder = WebApplication.CreateBuilder(new WebApplicationOptions { Args = Array.Empty<string>() });

        // Executor output belongs on the executor's own diagnostics, not the
        // Kestrel host banner.
        builder.Logging.ClearProviders();

        builder.WebHost.ConfigureKestrel(options =>
            options.ListenUnixSocket(socketPath, listenOptions =>
                listenOptions.Protocols = HttpProtocols.Http2));

        builder.Services.AddGrpc();
        builder.Services.AddSingleton(service);

        var app = builder.Build();
        app.MapGrpcService<GrpcExecutorService>();
        return app;
    }
}