# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Build and Test

All development goes through the `Makefile`.

- `make` (or `make all`) — runs `test` and `local` (builds the three binaries into `bin/`).
- `make local` — build only. Produces `bin/client`, `bin/server`, `bin/get-creds`.
- `make test` — `go test -race ./...`. Generates the protobuf stubs first if missing.
- `make images` — multi-arch Docker images via `docker buildx` (targets `agent-client-image`, `agent-controller-image` in the `Dockerfile`). Pushes by default.
- `make clean` / `make really-clean` — the latter also removes generated `tunnel.pb.go` / `tunnel_grpc.pb.go`.

**Protobuf prerequisites** (needed to regenerate stubs):

```
go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest
```

Stubs live at `internal/tunnel/tunnel.pb.go` and `internal/tunnel/tunnel_grpc.pb.go`; the Makefile rebuilds them from `internal/tunnel/tunnel.proto` when needed. The `make local` / `make test` targets touch these automatically — if you edit the `.proto`, run `make really-clean` to force regeneration.

### Running a single test

Standard Go invocation, e.g. `go test -race -run TestFoo ./internal/serviceconfig/...`. The generated protobuf files must exist first — run `make ${pb_deps}` or just `make test` once.

### Lint

CI runs `golangci-lint` (`.github/workflows/golangci-lint.yml`, `--timeout 5m`). There is no repo-local config; it uses the default ruleset. Run locally with `golangci-lint run ./...`.

### Local end-to-end

`examples/local-deploy/` has scripts (`setup.sh`, `run-controller.sh`, `run-agent.sh`) that spin up a controller + agent + `traefik/whoami` backend on ports 8001-8006 and 8300. See `examples/local-deploy/README.md` for the full curl recipes.

## Architecture

This is a **reverse-tunnel HTTP proxy** that crosses security domains. A SaaS-side **controller** accepts HTTPS requests from clients (kubectl, Spinnaker, etc.); customer-side **agents** dial *out* to the controller, establish a long-lived gRPC stream, and the controller forwards per-request work over that stream for the agent to execute against local services (Kubernetes API, Jenkins, Argo, etc.). Credentials used by the agent never leave the customer side.

### Binaries (`app/`)

- **`app/server`** — the controller. Terminates client TLS, authenticates via mTLS (for Kubernetes) or JWT bearer (for generic HTTP services), picks an agent by SNI / cert tag, forwards the request.
- **`app/client`** — the agent. Connects outbound to the controller's gRPC endpoint, registers its configured services, executes requests the controller sends back, streams responses.
- **`app/get-creds`** — CLI helper for issuing controller-signed credentials (user certs, JWTs) to clients.

Note: in `Makefile` terms the binaries are called `client` / `server`, but operationally these are **agent** (client) and **controller** (server). The Docker image targets `agent-client` and `agent-controller` reflect this. Don't be confused by the naming — when you see `app/client`, think "agent running in customer cluster."

### The tunnel (`internal/tunnel/`)

Bidirectional tunneling lives in `tunnel.proto`. The `TunnelService` exposes:

- `Hello` / `Ping` — registration + keepalive.
- `WaitForRequest` — server-streamed: the agent parks here and receives `TunnelRequest` messages as clients hit the controller.
- `RunRequest` — server-streamed: used for the reverse direction (controller-side services invoked by the agent).
- `DataFlowAgentToController` — client-streamed: response body chunks flowing back for a given request id.

`streamflow.go` encodes the chunking/framing for HTTP bodies over the gRPC stream.

### Service routing (`internal/serviceconfig/`)

Each endpoint type (kubernetes, jenkins, argocd, clouddriver, front50, fiat, aws, and user-defined `x-*`) is a "service" that can live on either the agent or the controller side (see README's Service Registry table). `generic_endpoint.go` is the default HTTP passthrough; `kubernetes.go` handles kubeconfig-style cert auth; `headers.go` does per-service header mutation (e.g. `X-Spinnaker-User`). The `service_server.go` is the HTTP listener that accepts incoming client requests and hands them to the tunnel.

### Certificate authority (`internal/ca/`)

The controller runs its own embedded CA. On startup it generates a server cert for its configured SANs from the CA key. The `make-ca`-generated Kubernetes Secret YAML (see README) seeds the CA. Issued certs carry a **purpose tag** (control / kubernetes / agent) that the controller checks on every inbound connection — requests must use a cert whose tag matches the endpoint.

### Auth (`internal/jwtutil/`, `internal/fwdapi/`)

- mTLS is used for Kubernetes-style clients (cert tag = `kubernetes`).
- JWTs (via `github.com/lestrrat-go/jwx/v2` and `github.com/skandragon/jwtregistry/v2`) are used for generic HTTP services — issued by the controller, carried as `Authorization: Bearer` or inside Basic auth.
- `internal/fwdapi/` is the controller's "CNC" (command-and-control) HTTP API that issues these credentials; `app/server/cncserver/` hosts it.

### Other internals

- `internal/kubeconfig/` — parses/generates kubeconfigs for agents to talk to their target cluster, and for clients to talk to the controller-as-kube-endpoint.
- `internal/secrets/` — reads agent-side secrets (tokens, creds) from disk/env.
- `internal/logging/` — zap-based structured logging, shared across binaries.
- `internal/ulid/`, `internal/util/` — request IDs and small helpers.

### Annotations

Endpoints and agent metadata carry free-form `key: value` annotations that propagate through `EndpointHealth` in `Hello`. A few have UI meaning (see README's Annotation Registry). When adding a new service type, decide whether annotations need special handling or are purely informational.

### Multi-agent fan-out

Multiple agents may register with the same name; the controller picks one at random per request. There is no sticky session beyond a single request/response pair. Watch/streaming requests (e.g. `kubectl -w`) are tied to the agent that received the initial request and stay on that stream.

## Repo conventions

- **Go module path** is `github.com/opsmx/oes-birger` (the repo is a fork; the path has not been renamed).
- **Apache-2.0** license headers are present on every source file. Preserve existing `Copyright ... OpsMx, Inc.` headers on any file you modify.
- **Generated code** (`internal/tunnel/*.pb.go`) is committed. Don't hand-edit; regenerate via the Makefile.
- The observability path uses **OTLP** for traces and Prometheus for metrics — this is recent (commit `657e26f` migrated off Jaeger). New instrumentation should go through `go.opentelemetry.io/otel`.
