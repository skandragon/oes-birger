# HTTP-Passthrough Simplification

Date: 2026-04-23
Status: Draft (awaiting user review)

## Goal

Simplify oes-birger so that every proxied service is handled as a generic HTTP passthrough. Remove all code paths that branch on a specific service-type name. Preserve the `type` field on `outgoingService` as a free-form identifier so the concept of a service type survives for naming, matching, annotations, and UI purposes.

## Non-goals

- Changing the tunnel protocol, streamflow, or gRPC session model.
- Changing the JWT-based authentication model for agents and control clients.
- Renaming packages, collapsing the `type`/`name` pair into a single identifier, or other structural refactors.
- Supporting backwards compatibility for configs that set `type: kubernetes` with a kubeconfig block. Those become hard errors (the generic endpoint does not understand kubeconfig blocks).

## Architecture after the change

oes-birger remains a reverse-tunnel HTTP proxy: SaaS-side controller, customer-side agent dialing out over gRPC, per-request forwarding. The difference is that endpoint dispatch becomes a straight line — `ConfigureEndpoints` always builds a `*GenericEndpoint`, regardless of `service.Type`. The string value of `Type` is advertised to the controller in `Hello` / `EndpointHealth`, used to look endpoints up by `(type, name)` on the agent side, and surfaced in the CNC API, but it never influences request handling.

JWT auth with purposes `agent` and `control` (see `internal/jwtutil/`) is unchanged. The controller presents a server cert for its client-facing and agent-facing listeners as before; there is no mTLS-to-client (the current code already sets `tls.NoClientCert` at `app/server/server.go:269`, so nothing to remove there).

## Changes

### Deletions

| Path | Reason |
| --- | --- |
| `internal/serviceconfig/kubernetes.go` | Kubernetes-specific endpoint processor. No caller after dispatch switch is removed. |
| `internal/serviceconfig/kubernetes_test.go` | Tests for the deleted file. |
| `internal/kubeconfig/` (entire package) | Only consumers are the deleted kubernetes processor and the `KubeconfigEndpoint` in cncserver. |

### In-place modifications

**`internal/serviceconfig/endpoints.go`**
Replace the `switch service.Type` block in `ConfigureEndpoints` (lines ~84-96) with a direct call to `MakeGenericEndpoint`. Drop the commented-out `aws` case. `Type` is still stored on the resulting `ConfiguredEndpoint` and propagated into `EndpointsToPB` — unchanged.

**`internal/serviceconfig/generic_endpoint.go`**
Remove `unmutateURI()` (lines ~191-209) and its call site inside `ExecuteHTTPRequest`. No other type-specific branches exist in this file.

**`internal/jwtutil/`**
Audit for symbols used only by `unmutateURI` (candidates: `MutationIsRegistered`, `UnmutateHeader`, the mutation registry itself). Remove any that become unused after the fiat deletion. If they are used elsewhere, leave alone. Resolve during implementation.

**`app/server/cncserver/cnc-server.go`**
- Delete `generateKubectlComponents()` (lines ~127-171).
- Delete the corresponding `mux.HandleFunc(fwdapi.KubeconfigEndpoint, …)` registration (~line 363).
- In `generateServiceCredentials()` (lines ~225-287), remove the `case "aws":` branch. Always emit `CredentialType: "basic"` with `BasicCredentialResponse`.

**`app/server/cncserver/cnc-server_test.go`**
Drop tests that exercise the deleted kubectl route and the aws credential shape.

**`internal/fwdapi/cnc.go`**
Remove `KubeConfigRequest`, `KubeConfigResponse`, `AwsCredentialResponse`, and the `KubeconfigEndpoint` route constant.

**`internal/fwdapi/validate.go`**
Remove any validation wired up for `KubeConfigRequest`.

**`app/get-creds/main.go`**
- Remove the `kubectl` option from the `action` flag help text.
- Delete `getKubeconfigCreds()`.
- Delete the `case "kubectl":` dispatch arm in `main()`.

**`internal/secrets/secrets.go`**
Earlier scan showed a `kubernetes` reference here. If it is loading the in-pod kube service-account token solely for the deleted kubernetes processor, simplify. If it has other consumers, leave. Resolve during implementation.

**`README.md`**
- Rewrite the "Service Registry" section to state that `type` is a free-form string used for endpoint naming, matching, and annotation-driven UI behavior; no type name has special code-path semantics.
- Update the `uiUrl` annotation row in the Annotation Registry: its `Context` column currently reads `service type argocd`. Change this to `any` and rewrite the description to note that interpretation is up to downstream UIs — this repo's code does not read or enforce the annotation.
- Remove any remaining references to mTLS-to-client auth, kubernetes cert tags, or kubeconfig issuance.

**`CLAUDE.md`**
- Rewrite the "Service routing" paragraph to say: every endpoint dispatches through `GenericEndpoint`; `type` is a free-form string used only for naming/matching. Remove the enumeration of special types.
- Rewrite the "Certificate authority" paragraph to say: the controller runs an embedded CA that generates its own server cert for configured SANs. Client-side authentication is JWT-based (`internal/jwtutil/`) with `agent` and `control` purpose claims — the controller does not use mTLS client auth and does not tag issued certs for purpose matching. Remove the "purpose tag (control / kubernetes / agent)" sentence.
- Drop the `internal/kubeconfig/` bullet from "Other internals".

### Untouched

- `internal/tunnel/` (proto, stubs, streamflow).
- `internal/serviceconfig/generic_endpoint.go` (minus fiat snippet), `headers.go`, `service_server.go`, `config.go`, `endpoints.go` skeleton, `echo.go`.
- `internal/ca/ca.go`. Still used by the agent (`app/client/main.go:258`) to validate the controller's CA.
- `internal/jwtutil/` JWT issuance/verification for `agent` + `control` purposes.
- `internal/secrets/` (modulo the audit above), `internal/logging/`, `internal/ulid/`, `internal/util/`.
- `app/server/` gRPC server, session management, echo plumbing.
- `app/client/` (the agent). Changes come transitively through `ConfigureEndpoints`.
- CNC routes: `ManifestEndpoint`, `ServiceEndpoint`, `ControlEndpoint`, `StatisticsEndpoint`.
- `examples/local-deploy/` (already uses `x-*` service types only).
- `Makefile`, `Dockerfile`, CI workflows.

## Data flow after the change

### Proxy request path (client → backend)

1. Client sends HTTPS request to the controller, authenticated with a service JWT (bearer or basic-auth wrapped).
2. Controller parses the JWT claims + URL to derive `(agentName, type, name)`.
3. Controller picks a matching agent session at random and looks up the endpoint by `(type, name)` in the advertised list.
4. Controller packs the request into a `TunnelRequest` and writes it to that agent's `WaitForRequest` stream.
5. Agent resolves `(type, name)` to a local `ConfiguredEndpoint.Instance` — **always a `*GenericEndpoint`** — and calls `ExecuteHTTPRequest`.
6. `ExecuteHTTPRequest` builds an `http.Request` against `ep.config.URL`, applies configured credentials (`none` / `basic` / `bearer` / `token`), applies header mutations, dispatches via `http.Client`, and streams the response body back through `DataFlowAgentToController`.
7. Controller reassembles chunks and writes them to the original client.

Step 5 is the only behavioral change point: previously it could route to a kubernetes-specific processor, now it never does. Step 6 no longer calls `unmutateURI`.

### CNC credential-issuance path (admin → controller)

- `/manifest` → `{agentName, JWT(purpose=agent), URL}`.
- `/service` → `{username, password}` where `username = name.agentName`, `password = service-scoped JWT`. Always this shape.
- `/control` → fresh `control`-purpose JWT.
- `/statistics` → connected-agents stats.
- `/kubeconfig` → gone. Callers get 404.

## Error handling and compatibility

- Configs with `type: kubernetes` plus a kubeconfig config block will fail at agent startup because the generic endpoint config unmarshaler does not recognize those fields and `url` will be missing. This is intentional — Q1 answer was "kubernetes fully gone, intentional breaking change."
- Configs with `type: kubernetes` that happen to also provide a valid generic-endpoint config (`url`, credentials) will silently work as a plain HTTP passthrough. This is acceptable: `type` is free-form, and nothing prevents reusing the string.
- The CNC API loses one route. Existing `get-creds kubectl` invocations will fail with a usage error (the action is removed from the flag help). Callers of the HTTP endpoint directly will get 404.
- `generateServiceCredentials` with `Type: "aws"` will return the standard basic-auth shape instead of the legacy `{awsAccessKey, awsSecretAccessKey}` shape. Any client that parses the response by the old shape will break. This is intentional per Q2 answer.

## Testing

- Delete tests for removed code (`kubernetes_test.go`, kubeconfig tests, kubectl and aws paths in `cnc-server_test.go`).
- Do not add new tests. The existing generic-endpoint tests already cover the only remaining datapath. The simplification reduces test surface area, not features.
- Run `make test` (which is `go test -race ./...`) as verification.
- Run `golangci-lint run ./...` to catch unused imports / dead symbols the deletions may leave behind.

## Open sub-decisions (to resolve during implementation, not pre-committed here)

1. `httpRequestProcessor` interface (`endpoints.go:40-42`) will have a single implementer after the change. **Default: keep.** It is a useful test seam at near-zero cost. Reverse only if it creates friction.
2. `jwtutil` mutation registry (`MutationIsRegistered`, `UnmutateHeader`, the registration function): remove iff fiat was the sole caller. Verify with grep during implementation.
3. `internal/secrets/secrets.go` kubernetes reference: remove iff it was loading kube service-account tokens only for the deleted kubernetes processor. Verify during implementation.
4. After deletions, `internal/serviceconfig/` may have unused imports / no-longer-needed yaml types. Clean up as the compiler flags them.

## Verification plan

1. `make test` passes (race-enabled).
2. `golangci-lint run ./...` passes.
3. `make local` builds all three binaries.
4. `examples/local-deploy/` end-to-end scripts still work (setup → controller → agent → whoami curl recipes).
5. Grep for `"kubernetes"`, `"kubeconfig"`, `"fiat"`, `"aws"` across `app/` and `internal/` returns only incidental references (e.g. in README prose), not code branches.
