# ToskaMesh Go Runtime SDK

Go client library for services joining the [ToskaMesh](https://github.com/abstractivemachines) service mesh.

## What It Does

- Auto-registers with Discovery (gRPC)
- Exposes a `/health` endpoint
- Sends heartbeat/TTL renewals
- Gracefully deregisters on shutdown
- Propagates metadata (routing strategy, weight, scheme, health endpoint)

## Install

```bash
go get github.com/abstractivemachines/toska-mesh-go
```

## Usage

```go
svc, err := runtime.New(
    runtime.WithServiceName("my-service"),
    runtime.WithPort(9090),
    runtime.WithDiscoveryAddress("discovery:8080"),
    runtime.WithRoutingStrategy(runtime.RoundRobin),
    runtime.WithMetadata("version", "1.0.0"),
)
if err != nil {
    log.Fatal(err)
}

svc.HandleFunc("GET /hello", helloHandler)

if err := svc.Run(ctx); err != nil {
    log.Fatal(err)
}
```

## Build & Test

```bash
make test       # go test -race ./...
make build      # build example binaries → bin/

# Run the example
go run ./examples/hello-mesh-service
```

## Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `MESH_SERVICE_NAME` | `mesh-service` | Service name for discovery |
| `MESH_SERVICE_PORT` | `8080` | HTTP bind port |
| `MESH_DISCOVERY_ADDRESS` | `localhost:8080` | Discovery gRPC address |

## Related

- [toska-mesh](https://github.com/abstractivemachines/toska-mesh) — Go control plane (Gateway, Discovery, HealthMonitor)
- [toska-mesh-cs](https://github.com/abstractivemachines/toska-mesh-cs) — C# runtime SDK
- [toska-mesh-proto](https://github.com/abstractivemachines/toska-mesh-proto) — Shared protobuf definitions

## License

Apache License 2.0 — see [LICENSE](LICENSE).
