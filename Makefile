MODULE      := github.com/Gorakhnath-R-Patil/EdgeMesh
BIN_DIR     := bin
BINARIES    := edgemesh-proxy edgemesh-controller edgemesh-cli

VERSION     := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT      := $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
DATE        := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)

LDFLAGS := -X '$(MODULE)/internal/buildinfo.Version=$(VERSION)' \
           -X '$(MODULE)/internal/buildinfo.Commit=$(COMMIT)' \
           -X '$(MODULE)/internal/buildinfo.Date=$(DATE)'

.PHONY: all build test race vet fmt fmt-check lint clean \
        run-proxy run-controller run-cli $(BINARIES)

all: fmt-check vet test build

## build: compile all EdgeMesh binaries into bin/
build: $(BINARIES)

$(BINARIES):
	go build -ldflags "$(LDFLAGS)" -o $(BIN_DIR)/$@ ./cmd/$@

## test: run unit tests
test:
	go test ./...

## race: run unit tests with the race detector
race:
	go test -race ./...

## vet: run go vet
vet:
	go vet ./...

## fmt: format source files in place
fmt:
	gofmt -w .

## fmt-check: fail if any file is not gofmt-formatted
fmt-check:
	@unformatted="$$(gofmt -l .)"; \
	if [ -n "$$unformatted" ]; then \
		echo "The following files are not gofmt-formatted:"; \
		echo "$$unformatted"; \
		exit 1; \
	fi

## clean: remove build artifacts
clean:
	rm -rf $(BIN_DIR)

## run-proxy: run edgemesh-proxy from source with the example config
run-proxy:
	go run ./cmd/edgemesh-proxy -config configs/proxy.example.yaml

## run-controller: run edgemesh-controller from source with the example config
run-controller:
	go run ./cmd/edgemesh-controller -config configs/controller.example.yaml

## run-cli: run edgemesh-cli from source (pass args via ARGS="...")
run-cli:
	go run ./cmd/edgemesh-cli $(ARGS)
