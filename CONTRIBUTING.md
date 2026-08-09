# Contributing to EdgeMesh

Thanks for your interest in EdgeMesh. This document covers the practical
expectations for submitting changes.

## Development setup

Requires Go 1.26+.

```sh
git clone https://github.com/Gorakhnath-R-Patil/EdgeMesh.git
cd EdgeMesh
make build
make test
```

Generated protobuf code (`gen/go/`) is checked in, so the steps above
never require `protoc`/`buf`. Only install them if you're changing a
`.proto` file under `proto/`:

```sh
make proto-tools   # one-time: installs buf + protoc-gen-go
make proto-lint
make proto-gen     # regenerates gen/go/ — commit the result
```

## Before opening a pull request

Run the full local quality gate:

```sh
make fmt-check   # gofmt formatting
make vet         # go vet
make test        # unit tests
make race        # unit tests with -race
make build        # all binaries compile
```

A pull request that fails any of these will not be merged as-is.

## Code style

* Follow standard Go conventions (`gofmt`, `go vet` clean, no unused
  code paths).
* Prefer the standard library over third-party dependencies. Add a new
  dependency only when it removes real complexity, not because it looks
  more sophisticated.
* Keep the data plane (`cmd/edgemesh-proxy` and anything it imports)
  free of business logic. Routing/policy decisions belong in clearly
  named, independently testable packages.
* Every exported type and function needs a doc comment explaining *why*
  it exists, not just what it does.
* Error handling: wrap errors with context using `internal/errors.Wrap`
  (or `fmt.Errorf("...: %w", err)`), and prefer the sentinel errors in
  `internal/errors` so callers can branch with `errors.Is`.
* No global mutable state. No unbounded queues or uncontrolled retries.
* Never hand-edit anything under `gen/`. Change the `.proto` source
  under `proto/` and run `make proto-gen`.

## Testing

* New behavior needs unit tests. Concurrency-sensitive code needs a test
  run under `-race`.
* Don't assert on timing-sensitive behavior without an explicit,
  documented tolerance.
* Don't fabricate or hand-wave performance numbers — if a change claims
  a performance characteristic, it should be backed by a benchmark.

## Commit messages

EdgeMesh uses [Conventional Commits](https://www.conventionalcommits.org/):

```
<type>(<scope>): <short summary>
```

Common types: `feat`, `fix`, `docs`, `test`, `refactor`, `perf`, `chore`,
`ci`. Scope is typically the subsystem touched, e.g. `feat(proxy): add
connection pooling`.

Avoid non-descriptive messages such as `update`, `changes`, `fix stuff`,
or `wip`. Each commit should represent one coherent, reviewable unit of
work with a message that explains what changed and, where it isn't
obvious, why.

## Pull requests

* Keep PRs scoped to a single concern.
* Describe what changed and why in the PR description; link any related
  issue.
* Include test output (or a summary of it) for non-trivial changes.
* Be responsive to review feedback — EdgeMesh favors correctness and
  predictable behavior over speed of merge.

## Reporting issues

Open a GitHub issue with:

* what you expected to happen
* what actually happened
* steps to reproduce
* EdgeMesh version / commit, Go version, and OS

For security issues, do not open a public issue — contact the maintainer
directly instead.
