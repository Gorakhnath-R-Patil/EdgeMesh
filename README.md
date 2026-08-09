# EdgeMesh

**Intelligent adaptive service mesh for cloud-native distributed systems.**

EdgeMesh is a service mesh that makes routing decisions based on the
**real-time health and performance of destination services**, not just
static configuration.

```
Service A
    |
    v
EdgeMesh
    |
    +------------------+
    |                  |
    v                  v
Payment-1          Payment-2
latency 25ms       latency 180ms
errors 0.1%        errors 4.8%
healthy            degraded
```

Instead of blindly splitting traffic 50/50, EdgeMesh shifts load toward
the healthier, faster endpoint — and shifts it back automatically once
the degraded endpoint recovers:

```
payment-2 latency/errors rise
        |
        v
   EdgeMesh detects degradation
        |
        v
   traffic shifts toward payment-1
        |
        v
   payment-2 recovers
        |
        v
   traffic gradually restored
```

> **Status: early-stage / foundational.** EdgeMesh is under active
> development. The routing intelligence described above is the project's
> north star, built up incrementally — see [Current status](#current-status)
> for what exists today.

## Why EdgeMesh

Most service meshes route on static weights or simple round robin.
EdgeMesh's premise is that a proxy sitting on every request already has
the signal it needs — latency, error rate, active connections, health
state — to make a better decision than a config file written last week.

```
score(endpoint) = health + latency + errors + load + locality
```

## Architecture

```
                     ┌──────────────────────┐
                     │   Control Plane       │
                     │                       │
                     │ Service Registry      │
                     │ Routing Engine        │
                     │ Policy Engine         │
                     │ Health Manager        │
                     └──────────┬────────────┘
                                │
                          xDS-style / gRPC
                                │
      ┌─────────────────────────┼─────────────────────────┐
      │                         │                          │
      ▼                         ▼                          ▼
┌───────────┐             ┌───────────┐             ┌───────────┐
│ EdgeMesh  │             │ EdgeMesh  │             │ EdgeMesh  │
│  Proxy    │             │  Proxy    │             │  Proxy    │
└─────┬─────┘             └─────┬─────┘             └─────┬─────┘
      ▼                         ▼                          ▼
  Services                  Services                   Services
```

The **data plane** (`edgemesh-proxy`) sits on the request path and stays
lightweight: no business logic, just forwarding, policy enforcement, and
telemetry. The **control plane** (`edgemesh-controller`) owns discovery,
health aggregation, and routing/policy configuration, and distributes it
to proxies — it does not sit on the per-request path.

## Repository layout

```
cmd/
  edgemesh-proxy/       data-plane binary
  edgemesh-controller/  control-plane binary
  edgemesh-cli/         operator CLI
internal/
  buildinfo/            version metadata stamped at build time
  config/                configuration loading, defaulting, validation
  logging/               structured logging (log/slog) setup
  errors/                shared error-handling conventions
  cli/                   subcommand dispatch used by edgemesh-cli
  mesh/                  validation for the core data models below
proto/                   protobuf contracts for the core data models
  edgemesh/mesh/v1alpha1/  Service, Endpoint, Route, Policy, HealthState,
                            RoutingDecision
gen/go/                  generated Go code from proto/ (checked in;
                          regenerate with `make proto-gen`, never edit by hand)
configs/                 example YAML configuration for each binary
```

## Building

Requires Go 1.26+.

```sh
make build     # compiles all binaries into bin/
make test      # unit tests
make race      # unit tests with the race detector
make vet       # go vet
make fmt-check # verify gofmt formatting
```

Run a binary directly from source:

```sh
make run-proxy        # edgemesh-proxy with configs/proxy.example.yaml
make run-controller    # edgemesh-controller with configs/controller.example.yaml
make run-cli ARGS=version
```

## Configuration

Each binary accepts an optional `-config <file>.yaml` flag. Any field
left unset falls back to a built-in default, and any field can be
overridden with an `EDGEMESH_*` environment variable at runtime — see
[configs/proxy.example.yaml](configs/proxy.example.yaml) for the shape.

```yaml
node:
  id: proxy-1
  zone: local
  region: local

logging:
  level: info   # debug | info | warn | error
  format: text  # text | json

server:
  listenAddress: 0.0.0.0:8080
```

## Core data models

EdgeMesh's domain model is defined as protobuf in
[proto/edgemesh/mesh/v1alpha1](proto/edgemesh/mesh/v1alpha1) and generated
into `gen/go/` (checked in — a normal `go build`/`go test` never needs
`protoc`/`buf` installed; they're only needed when a `.proto` file
changes, via `make proto-gen`):

| Message           | Represents                                                          |
| ------------------ | -------------------------------------------------------------------- |
| `Service`          | A logical destination clients address by name                       |
| `Endpoint`         | One network-addressable backend instance of a Service                |
| `Route`            | A request-match to weighted-destination(s) mapping                   |
| `Policy`           | A named, reusable bundle of load-balancing/health/circuit-breaker/retry/timeout settings |
| `HealthState`      | An endpoint's health: `HEALTHY`, `DEGRADED`, `UNHEALTHY`, `RECOVERING` |
| `RoutingDecision`  | An explainable record of why a specific endpoint was selected        |

[internal/mesh](internal/mesh) validates these messages (required
fields, name format, port ranges, positive durations, bounded retry
attempts, ...); the subsystems that act on them — discovery, health
checking, circuit breaking, retries, adaptive routing — are built up in
later development phases.

## Current status

EdgeMesh is being built up one deliberate layer at a time, starting from
the ground: repository foundation, core data models, a working L7 proxy,
service discovery, load balancing, health checking, circuit breaking,
retries, timeouts, and — eventually — the adaptive routing engine,
distributed tracing, Kubernetes integration, and Helm-based deployment
described above.

Target capability set:

```
✓ Service discovery
✓ L4/L7 proxying
✓ Load balancing
✓ Health checking
✓ Circuit breaking
✓ Retries
✓ Timeouts
✓ Latency-aware routing
✓ Error-aware routing
✓ Adaptive routing
✓ Traffic shifting
✓ Locality-aware routing
✓ Distributed tracing
✓ Prometheus metrics
✓ Kubernetes discovery
✓ Helm deployment
✓ Failure handling
✓ Chaos testing
✓ Optional AI-assisted analysis
```

What exists today: the repository foundation — Go module layout, the
three binaries (`edgemesh-proxy`, `edgemesh-controller`, `edgemesh-cli`)
with configuration loading, structured logging, graceful shutdown, and a
CI pipeline — plus the core data models (`Service`, `Endpoint`, `Route`,
`Policy`, `HealthState`, `RoutingDecision`) as validated protobuf
contracts. No routing, discovery, or proxying logic has landed yet.

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md).

## License

Licensed under the [Apache License, Version 2.0](LICENSE).
