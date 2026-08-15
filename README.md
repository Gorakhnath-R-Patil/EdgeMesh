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
  proxy/                 the data-plane HTTP forwarding handler
  registry/               in-memory service registry (service -> endpoint list)
  lb/                     load-balancing strategies (round robin, ...)
  health/                 active + passive health checking (HEALTHY/UNHEALTHY)
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

# edgemesh-proxy only: required, the single backend it forwards to.
upstream:
  address: http://127.0.0.1:9000
  dialTimeout: 5s
  requestTimeout: 15s
```

## Running the proxy

`edgemesh-proxy` forwards every request it receives to the one backend
named by `upstream.address` — connection pooling, a request timeout, and
structured per-request logging, but no endpoint selection yet (that
needs the service registry and routing engine, built up in later
development phases):

```sh
EDGEMESH_UPSTREAM_ADDRESS=http://127.0.0.1:9000 make run-proxy

curl http://127.0.0.1:8080/anything   # forwarded to the backend above
```

A backend that's down or times out gets translated into `502 Bad
Gateway` / `504 Gateway Timeout` respectively, both logged with the
underlying error.

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

## Service registry

[internal/registry](internal/registry) is the in-memory "service ->
endpoint list" store shown in the architecture diagram above:
`RegisterService`/`DeregisterService` (deregistering cascades to that
service's endpoints), `RegisterEndpoint`/`UpdateEndpoint`/
`DeregisterEndpoint`, and `Lookup`. A Service must be registered before
endpoints can be registered under it, so `ListServices` always reflects
real declarations rather than names inferred from endpoint traffic.
Safe for concurrent use — every stored/returned message is a defensive
copy, so callers can never mutate registry state through a pointer.

Not wired into `edgemesh-proxy` yet — that lands with the routing
strategies (later development phases) that pick one endpoint from a
`Lookup` result. Kubernetes-backed discovery (informers/watch) replaces
manual registration once that integration exists.

## Load balancing

[internal/lb](internal/lb) picks one endpoint from a candidate set —
deliberately independent of routing policy (*which* destination a
request matches) and of health/circuit-breaker state (*which*
candidates are even eligible), both later development phases. The
`Balancer` interface is the extension point for the load-balancing
strategies planned overall (round robin, weighted, least-connections,
latency-aware, adaptive); two exist today:

- **`RoundRobin`** — a lock-free, atomic-counter rotation
  (`A, B, C, A, B, C, ...`) that indexes into whatever candidate list
  it's given on each call, so it naturally tolerates the registry's
  endpoint list changing between requests without needing to track
  individual endpoint identity across calls.
- **`Weighted`** — random selection in proportion to each endpoint's
  relative `Weight` (e.g. `v1=90, v2=10` sends ~90%/~10% of traffic to
  each, converging over many requests, not exactly per request); an
  endpoint with `Weight` unset (0) gets the routing engine's default
  share (1 — a neutral unit, since weights are relative, not absolute
  percentages). Verified statistically in
  [weighted_test.go](internal/lb/weighted_test.go), which documents the
  standard-deviation math behind its tolerance.

Not wired into `edgemesh-proxy` yet — the proxy still forwards to one
statically configured backend (Day 3); connecting registry lookup ->
load balancing -> forwarding is a later development phase.

## Health checking

[internal/health](internal/health) tracks the two-state
`HEALTHY`/`UNHEALTHY` model from two independent signal sources — active
probes and real request outcomes — both writing to the same registry:

- **`Checker`** — pluggable active-probe interface (`HTTPChecker` GETs a
  configurable path, e.g. `/healthz`, and treats any 2xx as healthy);
  `CheckerFunc` adapts a plain function for tests or simple cases.
- **`Monitor`** — checks every endpoint of every registered service on
  an interval. `CheckOnce` runs a single synchronous pass (what the
  tests drive, deterministically); `Run` calls it on a timer until its
  context is canceled, the same shutdown pattern used by
  `edgemesh-proxy`. Only `Monitor` can detect recovery, since a
  routing layer that stops sending an `UNHEALTHY` endpoint traffic also
  stops generating passive observations for it.
- **`PassiveTracker`** — turns real request outcomes into the same
  signal: a future integration calls `Observe` once per proxied
  request. `ClassifyHTTPStatus` treats any 5xx as a failure (4xx
  reflects the request, not the backend, so it never counts);
  `ClassifyError` treats a transport-level failure (connection refused,
  DNS failure, timeout, ...) as a failure, except `context.Canceled` —
  the client gave up, not the backend.
- **`FilterHealthy`** — the concrete "remove unhealthy endpoints from
  normal routing" mechanism: excludes only `UNHEALTHY` endpoints from a
  candidate list, ready to sit between a registry `Lookup` and a
  `Balancer` once they're wired together.

Both `Monitor` and `PassiveTracker` share the same
`FailureThreshold`/`SuccessThreshold` consecutive-result hysteresis
(from the `HealthCheckPolicy` message defined back in Day 2) so a
single blip — active or passive — never flaps an endpoint's state, but
each keeps its **own** counters: an active probe and a live client
request are different kinds of sample, so one succeeding doesn't erase
the other's failure streak. Unifying every signal into one
authoritative per-endpoint state machine is the circuit breaker's job
(a later development phase); today they're independent observers
converging on the same registry. `DEGRADED`/`RECOVERING` are also
reserved for that later phase and are never produced here.

Not wired into a binary yet (no scheduler owns a `Monitor.Run` loop, no
proxy integration calls `PassiveTracker.Observe`) — later development
phases, following the same "build the subsystem, prove it with tests,
wire it up once there's something real to wire it into" approach as the
registry and load-balancing packages.

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
three binaries with configuration loading, structured logging, graceful
shutdown, and a CI pipeline; the core data models (`Service`, `Endpoint`,
`Route`, `Policy`, `HealthState`, `RoutingDecision`) as validated
protobuf contracts; a working `edgemesh-proxy` that forwards every
request to one statically configured backend, with connection pooling,
a request timeout, and structured per-request logging; and an in-memory
service registry; two load-balancing strategies (round robin,
weighted); and health checking, both active (`Monitor`, with recovery
detection) and passive (`PassiveTracker`, from real request outcomes).
None of these are connected to each other yet — `edgemesh-proxy` still
only knows about the single backend named in its config. Wiring
registry lookup -> health filtering -> load balancing -> forwarding ->
passive observation, with something actually running the health
`Monitor`, is a later development phase.

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md).

## License

Licensed under the [Apache License, Version 2.0](LICENSE).
