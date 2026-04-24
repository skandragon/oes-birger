# HTTP-Passthrough Simplification — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Collapse the codebase to an HTTP-only reverse-tunnel proxy: every endpoint dispatches through `GenericEndpoint`, no code branches on specific service-type names, no Spinnaker-specific header rewriting. The `type` field on `outgoingService` is preserved as a free-form string.

**Architecture:** Delete-only refactor. Remove one Go package (`internal/kubeconfig`), one file per targeted special case, one config field, one CNC route, and one sub-action in `get-creds`. No new code paths. Updates to README and CLAUDE.md reflect the simpler reality.

**Tech Stack:** Go 1.26, gRPC + protobuf, `github.com/lestrrat-go/jwx/v2`, `github.com/skandragon/jwtregistry/v2`, `zap` logging, `go-resty/resty/v2` (in `get-creds`).

**Spec:** `docs/superpowers/specs/2026-04-23-http-passthrough-simplification-design.md`.

**Working branch:** `http-passthrough-simplification-spec` (already exists; spec is committed). All implementation commits land on this branch. The branch is already associated with PR #10.

---

## File structure (post-change)

| Area | State |
| --- | --- |
| `internal/serviceconfig/` | `endpoints.go` loses switch; `generic_endpoint.go` loses `unmutateURI`; `headers.go` loses X-Spinnaker-User mutation branches and the `jwtutil` import; `kubernetes.go` and `kubernetes_test.go` deleted; `generic_endpoint_test.go` loses fiat tests. |
| `internal/kubeconfig/` | **Deleted.** |
| `internal/jwtutil/` | `mutateheaders.go` + `mutateheaders_test.go` deleted. `jwt.go`, `jwt_*_test.go`, `jwt_testhelper.go` unchanged. |
| `internal/fwdapi/` | `cnc.go` loses `KubeConfigRequest`, `KubeConfigResponse`, `AwsCredentialResponse`, `KubeconfigEndpoint`. `validate.go` loses `KubeConfigRequest.Validate()`; `typeValid` regex relaxed. |
| `app/server/cncserver/` | `cnc-server.go` loses `generateKubectlComponents()` + kubectl route + `aws` credential branch. Tests updated. |
| `app/server/main.go` | `RegisterMutationKeyset` call removed; `HeaderMutationKeyName`-missing guard removed. |
| `app/server/config.go` | `HeaderMutationKeyName` field removed; kubectl log message renamed. |
| `app/get-creds/main.go` | `kubectl` action + `getKubeconfigCreds()` removed. |
| `internal/secrets/secrets.go` | Audited; any kubernetes-exclusive code removed (only if non-load-bearing). |
| `README.md`, `CLAUDE.md` | Prose updates per spec. |

---

## Global verification commands

Run these on demand during implementation. They are referenced by number in tasks below.

- **V1 (fast unit):** `make test` — runs `go test -race ./...` after regenerating protobuf stubs. Expected: all tests pass.
- **V2 (lint):** `golangci-lint run ./...` — expected: no errors.
- **V3 (build):** `make local` — expected: produces `bin/client`, `bin/server`, `bin/get-creds`.
- **V4 (dead-string sweep):** `grep -rn -E "(KubeConfig|kubeconfig|generateKubectlComponents|HeaderMutationKeyName|AwsCredentialResponse|MutationIsRegistered|unmutateURI)" app/ internal/` — expected: no matches.
- **V5 (examples):** Follow `examples/local-deploy/README.md` curl recipes end-to-end (setup.sh → run-controller.sh → run-agent.sh → curl whoami on port 8001). Expected: whoami responds.

Every commit must be preceded by V1 passing unless the task explicitly notes otherwise.

---

## Task 1: Collapse the kubernetes dispatch branch

**Files:**
- Modify: `internal/serviceconfig/endpoints.go:71-137`

Collapse the `switch service.Type` block so every endpoint is built via `MakeGenericEndpoint`. Remove the kubernetes branch and the commented-out aws branch.

- [ ] **Step 1.1: Read current file**

Read `internal/serviceconfig/endpoints.go` in full. Note that `logger` is still needed for `Infow` calls and `logger.Fatal(err)`. The imports to keep: `context`, `fmt`, `github.com/opsmx/oes-birger/internal/logging`, `github.com/opsmx/oes-birger/internal/secrets`, `pb "github.com/opsmx/oes-birger/internal/tunnel"`, `gopkg.in/yaml.v3`.

- [ ] **Step 1.2: Replace the switch**

In `ConfigureEndpoints`, replace lines 76-96 (the `var instance`, `var configured`, and `switch service.Type { … }` block) with:

```go
			var err error
			config, err := yaml.Marshal(service.Config)
			if err != nil {
				logger.Fatal(err)
			}
			instance, configured, err := MakeGenericEndpoint(ctx, service.Type, service.Name, config, secretsLoader)
```

Delete the old `var instance`, `var configured`, and `switch` lines entirely. The rest of the function (the `if err != nil { logger.Fatal(err) }` check and the `endpoints = append(...)` block) is unchanged.

- [ ] **Step 1.3: Verify compile**

Run: `go build ./internal/serviceconfig/...`
Expected: compiles clean. (Note: at this point `kubernetes.go` still exists in the package but is no longer reachable via dispatch — that is fine and expected; it will be deleted in Task 2.)

- [ ] **Step 1.4: Run V1**

Run: `make test`
Expected: pass. (Kubernetes tests in `kubernetes_test.go` still compile and still pass because `kubernetes.go` still exists; they're just no longer exercised through the dispatch path.)

- [ ] **Step 1.5: Commit**

```bash
git add internal/serviceconfig/endpoints.go
git commit -m "refactor: collapse endpoint dispatch to GenericEndpoint

Remove the kubernetes case and the commented-out aws case from the
ConfigureEndpoints switch. All endpoints now dispatch through
MakeGenericEndpoint regardless of service.Type."
```

---

## Task 2: Delete the kubernetes endpoint processor

**Files:**
- Delete: `internal/serviceconfig/kubernetes.go`
- Delete: `internal/serviceconfig/kubernetes_test.go`

- [ ] **Step 2.1: Delete both files**

```bash
rm internal/serviceconfig/kubernetes.go internal/serviceconfig/kubernetes_test.go
```

- [ ] **Step 2.2: Run V1**

Run: `make test`
Expected: pass. (No callers remained after Task 1.)

- [ ] **Step 2.3: Commit**

```bash
git add -A internal/serviceconfig/
git commit -m "refactor: remove KubernetesEndpoint processor

No callers remain after the dispatch switch collapse."
```

---

## Task 3: Delete the kubeconfig route from cncserver

**Files:**
- Modify: `app/server/cncserver/cnc-server.go:127-171,363-364`
- Modify: `app/server/cncserver/cnc-server_test.go` (remove kubectl-route tests)

- [ ] **Step 3.1: Remove the handler**

In `app/server/cncserver/cnc-server.go`, delete the entire `func (s *CNCServer) generateKubectlComponents() http.HandlerFunc` (lines ~127-171 inclusive — the whole function plus the trailing blank line).

- [ ] **Step 3.2: Remove the route registration**

In the same file, in `func (s *CNCServer) routes(mux *http.ServeMux)`, delete the `mux.HandleFunc(fwdapi.KubeconfigEndpoint, …)` registration and its two-line handler, i.e. lines ~363-364. Leave the other four routes (`ManifestEndpoint`, `ServiceEndpoint`, `ControlEndpoint`, `StatisticsEndpoint`) unchanged.

- [ ] **Step 3.3: Remove tests for the kubectl route**

Open `app/server/cncserver/cnc-server_test.go`. Find every test function that POSTs to `fwdapi.KubeconfigEndpoint` or references `fwdapi.KubeConfigRequest`/`fwdapi.KubeConfigResponse`, and delete each such function in full. Typical names: `TestGenerateKubectlComponents*`. If a helper function is used only by these deleted tests, delete it too.

- [ ] **Step 3.4: Verify compile**

Run: `go build ./app/server/cncserver/...`
Expected: compiles clean. (`fwdapi.KubeconfigEndpoint` and related types still exist — they will be removed in Task 5.)

- [ ] **Step 3.5: Run V1**

Run: `make test`
Expected: pass.

- [ ] **Step 3.6: Commit**

```bash
git add app/server/cncserver/
git commit -m "refactor: remove /kubeconfig CNC route

Drops generateKubectlComponents and its route registration. Callers
that hit this endpoint now receive 404."
```

---

## Task 4: Delete `internal/kubeconfig/`

**Files:**
- Delete: `internal/kubeconfig/` (entire package)

- [ ] **Step 4.1: Confirm zero importers**

Run: `grep -rn '"github.com/opsmx/oes-birger/internal/kubeconfig"' app/ internal/`
Expected: no matches. (If any match, stop — a prior task left dangling references; fix before continuing.)

- [ ] **Step 4.2: Delete the package**

```bash
rm -rf internal/kubeconfig
```

- [ ] **Step 4.3: Tidy modules**

Run: `go mod tidy`
Expected: `go.mod` / `go.sum` may drop dependencies that were only transitively imported via this package.

- [ ] **Step 4.4: Run V1**

Run: `make test`
Expected: pass.

- [ ] **Step 4.5: Commit**

```bash
git add -A internal/kubeconfig go.mod go.sum
git commit -m "refactor: delete internal/kubeconfig package

No remaining importers after the kubernetes processor and the
/kubeconfig CNC route were removed."
```

---

## Task 5: Remove kubeconfig + aws types from fwdapi and relax the type regex

**Files:**
- Modify: `internal/fwdapi/cnc.go:22-28,30-42,88-92`
- Modify: `internal/fwdapi/validate.go:32-42,61-72`

- [ ] **Step 5.1: Remove `KubeconfigEndpoint` constant**

In `internal/fwdapi/cnc.go`, inside the `const ( … )` block, delete the line:

```go
	KubeconfigEndpoint = "/api/v1/generateKubectlComponents"
```

Keep the other four constants.

- [ ] **Step 5.2: Remove `KubeConfigRequest` and `KubeConfigResponse`**

In the same file, delete the two struct type declarations in full (including their `// KubeConfigRequest defines …` and `// KubeConfigResponse defines …` doc comments).

- [ ] **Step 5.3: Remove `AwsCredentialResponse`**

In the same file, delete the `AwsCredentialResponse` struct and its doc comment.

- [ ] **Step 5.4: Remove `KubeConfigRequest.Validate`**

In `internal/fwdapi/validate.go`, delete the `func (req *KubeConfigRequest) Validate() error { … }` method and its doc comment (lines ~61-72).

- [ ] **Step 5.5: Relax `typeValid` regex**

In `internal/fwdapi/validate.go`, change the regex in `typeValid` from `"^[a-z0-9]+$"` to `"^[a-z0-9][a-z0-9-]*$"`. Update the doc comment above `typeValid` from:

```go
// TypeValid ensures type is valid, that is, lowercase alphanumeric only
```

to:

```go
// TypeValid ensures type is a lowercase identifier (alphanumeric plus internal hyphens).
```

- [ ] **Step 5.6: Verify compile**

Run: `go build ./internal/fwdapi/...`
Expected: compiles clean.

- [ ] **Step 5.7: Update existing validate tests if present**

Look for `internal/fwdapi/validate_test.go` or similar. Confirm whether any test asserts `typeValid("x-foo")` returns `false` (would now break) or asserts on `KubeConfigRequest.Validate()` behavior (type is gone). Delete those specific test cases; leave others.

- [ ] **Step 5.8: Run V1**

Run: `make test`
Expected: pass.

- [ ] **Step 5.9: Commit**

```bash
git add internal/fwdapi/
git commit -m "refactor: drop kubeconfig+aws types; allow hyphens in type regex

Remove KubeConfigRequest, KubeConfigResponse, AwsCredentialResponse,
and the KubeconfigEndpoint constant. Relax typeValid to allow
x-prefixed custom type names."
```

---

## Task 6: Remove the aws branch in `generateServiceCredentials`

**Files:**
- Modify: `app/server/cncserver/cnc-server.go:258-271`
- Modify: `app/server/cncserver/cnc-server_test.go` (remove aws-shape assertions)

- [ ] **Step 6.1: Collapse the switch**

In `generateServiceCredentials`, replace the `switch req.Type { case "aws": … default: … }` block (lines ~258-271) with the unconditional basic-auth shape:

```go
		ret.CredentialType = "basic"
		ret.Credential = fwdapi.BasicCredentialResponse{
			Username: username,
			Password: token,
		}
```

- [ ] **Step 6.2: Remove aws-shape tests**

In `cnc-server_test.go`, find any test that posts to `fwdapi.ServiceEndpoint` with `Type: "aws"` and asserts on `AwsCredentialResponse` / `awsAccessKey`. Delete those test cases (or convert them to assert the basic-auth shape if a corresponding basic test does not already exist for the same input path). Do not add new tests beyond what is needed to keep coverage of the remaining shape.

- [ ] **Step 6.3: Run V1**

Run: `make test`
Expected: pass.

- [ ] **Step 6.4: Commit**

```bash
git add app/server/cncserver/
git commit -m "refactor: drop aws credential shape in service CNC handler

generateServiceCredentials now always returns the basic-auth shape."
```

---

## Task 7: Remove fiat URI unmutation

**Files:**
- Modify: `internal/serviceconfig/generic_endpoint.go` (delete `unmutateURI` and its one call site)
- Modify: `internal/serviceconfig/generic_endpoint_test.go:321-436` (delete both fiat test functions)

- [ ] **Step 7.1: Remove the caller**

In `internal/serviceconfig/generic_endpoint.go`, locate `ExecuteHTTPRequest`. Find the call to `ep.unmutateURI(...)`; it looks roughly like:

```go
	uri, err := ep.unmutateURI(ep.endpointType, req.Method, req.URI, clock)
	if err != nil {
		…
	}
```

Delete the call and its error handling, and update the downstream logic to use `req.URI` directly where `uri` was used.

- [ ] **Step 7.2: Remove the method**

In the same file, delete `func (ep *GenericEndpoint) unmutateURI(...)` in full, including its doc comment.

- [ ] **Step 7.3: Remove the fiat tests**

In `internal/serviceconfig/generic_endpoint_test.go`, delete the entire `TestGenericEndpoint_unmutateURI_nokey` function and the entire `TestGenericEndpoint_unmutateURI_key` function (lines ~321-436 in the current file).

- [ ] **Step 7.4: Clean imports**

After Step 7.3, `generic_endpoint_test.go` may have unused imports (`jwtregistry`, `jwtutil` helpers, `jwt.Clock`, `require`). Remove any imports the compiler flags as unused. Do NOT also remove `jwtutil` from `generic_endpoint.go` yet if it is still used (it likely is not; confirm and remove if so).

- [ ] **Step 7.5: Verify compile**

Run: `go build ./internal/serviceconfig/...`
Expected: compiles clean.

- [ ] **Step 7.6: Run V1**

Run: `make test`
Expected: pass.

- [ ] **Step 7.7: Commit**

```bash
git add internal/serviceconfig/
git commit -m "refactor: drop fiat URI unmutation from GenericEndpoint

Removes the last type-specific branch on the proxy datapath."
```

---

## Task 8: Remove X-Spinnaker-User header mutation

**Files:**
- Modify: `internal/serviceconfig/headers.go`
- Delete: `internal/jwtutil/mutateheaders.go`
- Delete: `internal/jwtutil/mutateheaders_test.go`
- Modify: `app/server/main.go:173-176,293-295`
- Modify: `app/server/config.go:57-61`

- [ ] **Step 8.1: Simplify `PBHEadersToHTTP` and `HTTPHeadersToPB`**

Replace the entire body of `internal/serviceconfig/headers.go` with:

```go
/*
 * Copyright 2021-2023 OpsMx, Inc.
 *
 * Licensed under the Apache License, Version 2.0 (the "License")
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *   http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package serviceconfig

import (
	"net/http"
	"strings"

	pb "github.com/opsmx/oes-birger/internal/tunnel"
)

var strippedOutgoingHeaders = []string{"Authorization"}

func containsFolded(l []string, t string) bool {
	for i := 0; i < len(l); i++ {
		if strings.EqualFold(l[i], t) {
			return true
		}
	}
	return false
}

func PBHEadersToHTTP(headers []*pb.HttpHeader, out *http.Header) error {
	for _, header := range headers {
		for _, value := range header.Values {
			out.Add(header.Name, value)
		}
	}
	return nil
}

func HTTPHeadersToPB(headers map[string][]string) (ret []*pb.HttpHeader, err error) {
	ret = make([]*pb.HttpHeader, 0)
	for name, values := range headers {
		if containsFolded(strippedOutgoingHeaders, name) {
			continue
		}
		ret = append(ret, &pb.HttpHeader{Name: name, Values: values})
	}
	return ret, nil
}
```

Note: the return-error signature of `PBHEadersToHTTP` is preserved to avoid touching call sites.

- [ ] **Step 8.2: Delete `mutateheaders.go` and its test**

```bash
rm internal/jwtutil/mutateheaders.go internal/jwtutil/mutateheaders_test.go
```

- [ ] **Step 8.3: Remove `RegisterMutationKeyset` call from controller main**

In `app/server/main.go`, delete the three-line block:

```go
	if err = jwtutil.RegisterMutationKeyset(serviceKeyset, config.ServiceAuth.HeaderMutationKeyName); err != nil {
		logger.Fatal(err)
	}
```

(It currently sits around line 293, between the control-keyset registration and the agent-keyset registration.)

- [ ] **Step 8.4: Remove `HeaderMutationKeyName` fatal in `loadServiceAuthKeyset`**

Still in `app/server/main.go`, delete:

```go
	if len(config.ServiceAuth.HeaderMutationKeyName) == 0 {
		logger.Fatal("serviceAuth.headerMutationKeyName is not set")
	}
```

(lines ~173-176.)

- [ ] **Step 8.5: Remove the config field**

In `app/server/config.go`, inside `type serviceAuthConfig struct`, delete the line:

```go
	HeaderMutationKeyName string `yaml:"headerMutationKeyName,omitempty"`
```

- [ ] **Step 8.6: Verify compile**

Run: `go build ./...`
Expected: compiles clean. (If the `jwtutil` import in `app/server/main.go` becomes unused because only `RegisterMutationKeyset` was the reference — confirm first; other `jwtutil.Register*` calls likely keep it in use.)

- [ ] **Step 8.7: Run V1**

Run: `make test`
Expected: pass.

- [ ] **Step 8.8: Commit**

```bash
git add -A internal/serviceconfig/headers.go internal/jwtutil/ app/server/main.go app/server/config.go
git commit -m "refactor: drop X-Spinnaker-User header mutation

Removes jwtutil.mutateheaders, the RegisterMutationKeyset wiring, and
the headerMutationKeyName config field. Keeps Authorization-header
stripping (that is generic proxy security, not Spinnaker-specific)."
```

---

## Task 9: Remove the `kubectl` action from `get-creds`

**Files:**
- Modify: `app/get-creds/main.go`

- [ ] **Step 9.1: Delete `getKubeconfigCreds`**

In `app/get-creds/main.go`, delete the entire `func getKubeconfigCreds()` (lines ~77-94).

- [ ] **Step 9.2: Update flag help text**

Change the `action` flag default help from:

```go
action        = flag.String("action", "", "action, one of: kubectl, agent-manifest, service, or control")
```

to:

```go
action        = flag.String("action", "", "action, one of: agent-manifest, service, or control")
```

- [ ] **Step 9.3: Update usage text**

In `func usage(message string)`, delete the line:

```go
	fmt.Fprintf(os.Stderr, "  'kubectl' requires: agent, endpointName.\n")
```

- [ ] **Step 9.4: Remove `case "kubectl"`**

In `main()`, delete the `case "kubectl":` arm in full (the four lines under it: `insist(...)`, `insist(...)`, `insist(...)`, `getKubeconfigCreds()`).

- [ ] **Step 9.5: Verify compile**

Run: `go build ./app/get-creds/...`
Expected: compiles clean.

- [ ] **Step 9.6: Run V1**

Run: `make test`
Expected: pass.

- [ ] **Step 9.7: Commit**

```bash
git add app/get-creds/main.go
git commit -m "refactor: drop kubectl action from get-creds

Matches removal of the /kubeconfig CNC route."
```

---

## Task 10: Rename the kubectl log message

**Files:**
- Modify: `app/server/config.go:189`

- [ ] **Step 10.1: Rename the log line**

In `func (c *ControllerConfig) Dump(logger *zap.SugaredLogger)`, change:

```go
	logger.Infow("URL returned for kubectl components", "url", c.GetServiceURL())
```

to:

```go
	logger.Infow("Service URL", "url", c.GetServiceURL())
```

- [ ] **Step 10.2: Run V1**

Run: `make test`
Expected: pass.

- [ ] **Step 10.3: Commit**

```bash
git add app/server/config.go
git commit -m "refactor: rename kubectl log message"
```

---

## Task 11: Audit `internal/secrets/secrets.go`

**Files:**
- Potentially modify: `internal/secrets/secrets.go`

- [ ] **Step 11.1: Read the file**

Open `internal/secrets/secrets.go` in full. The earlier grep flagged a `kubernetes` reference. Identify what it is.

- [ ] **Step 11.2: Decide and act**

Two sub-cases:

- **(a) The reference is in a comment, variable name, or loader path (e.g. `/var/run/secrets/kubernetes.io/...`) that served the deleted kubernetes endpoint processor and nothing else.** Delete the relevant code, but preserve the `SecretLoader` interface and any generic-endpoint-facing entry points (`internal/serviceconfig/generic_endpoint.go:65-69,116-154` calls these).
- **(b) The reference is incidental (e.g. loading Kubernetes-native secrets for any endpoint, or an unused commented-out block).** Leave it alone, or at most delete a dead comment. Do not refactor the interface.

The spec explicitly says: keep broad `SecretLoader` functionality. Do not pre-commit which sub-case applies; read and decide.

- [ ] **Step 11.3: Verify compile**

Run: `go build ./...`
Expected: compiles clean.

- [ ] **Step 11.4: Run V1**

Run: `make test`
Expected: pass.

- [ ] **Step 11.5: Commit (only if the file changed)**

If sub-case (a) was acted on, commit:

```bash
git add internal/secrets/secrets.go
git commit -m "refactor: drop kubernetes-exclusive code in secrets loader"
```

If sub-case (b), skip this commit.

---

## Task 12: Update README.md

**Files:**
- Modify: `README.md`

- [ ] **Step 12.1: Rewrite the Service Registry section**

In `README.md`, find the "# Service Registry" heading and its table plus the sentence about `x-` prefixes. Replace the whole Service-Registry block with:

```markdown
# Service types

The `type` field on an `outgoingService` is a free-form lowercase identifier
(matching `^[a-z0-9][a-z0-9-]*$`). It is used to name the endpoint for routing
and for display in logs and downstream UIs. No type name has special
code-path semantics — every endpoint is dispatched as a generic HTTP
passthrough. Configure credentials (`basic`, `bearer`, `token`, or `none`)
under the `credentials` block on each service.

Operators commonly use descriptive labels such as `jenkins`, `argocd`,
`clouddriver`, or custom labels like `x-my-api`, but these are conventions,
not required names.
```

- [ ] **Step 12.2: Update the Annotation Registry row**

Find the `uiUrl` row in the "## Annotation Registry" table. Change its `Context` column from `service type argocd` to `any`. Change its description to:

```
A URL used by downstream UIs to link to the underlying service. This repository's code does not read or enforce the annotation.
```

- [ ] **Step 12.3: Remove kubeconfig / mTLS prose**

Search `README.md` for `kubeconfig`, `kubernetes`, `mTLS`, `cert tag`, `purpose tag`, `X-Spinnaker-User`, `headerMutationKeyName`. For any remaining references tied to removed features, delete the sentence or paragraph. Retain factual references to the embedded CA's role in generating the controller's own server cert (that still happens).

- [ ] **Step 12.4: Run V1**

Run: `make test`
Expected: pass.

- [ ] **Step 12.5: Commit**

```bash
git add README.md
git commit -m "docs: rewrite README for HTTP-passthrough-only model"
```

---

## Task 13: Update CLAUDE.md

**Files:**
- Modify: `CLAUDE.md`

- [ ] **Step 13.1: Rewrite "Service routing"**

Find the "### Service routing (`internal/serviceconfig/`)" paragraph. Replace its body with:

```markdown
Every endpoint is dispatched through `generic_endpoint.go`, the single
HTTP passthrough implementation. The `type` field on `outgoingService` is a
free-form lowercase identifier; no type name has special code-path
semantics. `service_server.go` is the HTTP listener that accepts incoming
client requests and hands them to the tunnel. `headers.go` strips the
inbound `Authorization` header on the way to the agent (to avoid leaking
the client's token to the agent-side backend) and otherwise forwards
headers unchanged.
```

- [ ] **Step 13.2: Rewrite "Certificate authority"**

Find the "### Certificate authority (`internal/ca/`)" paragraph. Replace its body with:

```markdown
The controller generates its own server certificate from a loaded CA
(seeded via a Kubernetes Secret; see README). The agent uses
`internal/ca/ValidateCACert` to verify the controller's cert on connect.
Client authentication is JWT-based (`internal/jwtutil/`) — there are no
client-side certificate purpose tags.
```

- [ ] **Step 13.3: Drop `internal/kubeconfig/` bullet**

In the "### Other internals" list, delete the bullet that mentions `internal/kubeconfig/`.

- [ ] **Step 13.4: Update Auth bullet**

In the "### Auth" subsection, delete any sentence claiming mTLS is used for Kubernetes-style clients. Keep the JWT bullets.

- [ ] **Step 13.5: Run V1**

Run: `make test`
Expected: pass.

- [ ] **Step 13.6: Commit**

```bash
git add CLAUDE.md
git commit -m "docs: update CLAUDE.md for simplified architecture"
```

---

## Task 14: Full verification sweep

**Files:** none

- [ ] **Step 14.1: Run V1**

Run: `make test`
Expected: pass.

- [ ] **Step 14.2: Run V2**

Run: `golangci-lint run ./...`
Expected: no errors. Fix any unused-import or ineffectual-assignment findings before continuing. Commit fixes with `refactor: post-simplification lint fixes` if any are needed.

- [ ] **Step 14.3: Run V3**

Run: `make local`
Expected: `bin/client`, `bin/server`, `bin/get-creds` produced.

- [ ] **Step 14.4: Run V4 (dead-string sweep)**

Run: `grep -rn -E "(KubeConfig|kubeconfig|generateKubectlComponents|HeaderMutationKeyName|AwsCredentialResponse|MutationIsRegistered|unmutateURI)" app/ internal/`
Expected: no matches. If any match, identify and either delete (if dead) or fix (if live); commit the fix.

- [ ] **Step 14.5: Run V5 (local-deploy smoke)**

Follow `examples/local-deploy/README.md`:
1. `cd examples/local-deploy && ./setup.sh`
2. In one terminal: `./run-controller.sh`
3. In another: `./run-agent.sh`
4. Exercise one curl recipe from the README (e.g. `curl -k https://localhost:8001/` with the expected headers). Confirm whoami responds.

Expected: end-to-end works.

- [ ] **Step 14.6: Commit any verification-driven fixes**

If any of Steps 14.2-14.5 required a fix, commit it as its own commit with a message describing what was fixed.

---

## Task 15: Codex review pass

**Files:** none (review only)

- [ ] **Step 15.1: Gather the diff**

Run: `git log --oneline main..HEAD` and `git diff --stat main..HEAD` to enumerate the changes on the branch since the spec commit.

- [ ] **Step 15.2: Ask Codex for an implementation review**

Use the `mcp__plugin_code-colaboration_codex__codex` tool with a prompt that includes:
- Path to the spec: `docs/superpowers/specs/2026-04-23-http-passthrough-simplification-design.md`
- Path to the plan: `docs/superpowers/plans/2026-04-23-http-passthrough-simplification-plan.md`
- The list of commits on the branch (from Step 15.1)
- Explicit ask: verify the implementation matches the spec, catch any silent failure points, flag dead code that wasn't deleted, check that the intentional breakage points (404 on `/kubeconfig`, basic shape on `type: aws`, `x-foo` accepted by `typeValid`) are realized.

Ask Codex to reply in ≤600 words with findings grouped by severity.

- [ ] **Step 15.3: Apply critical and correctness fixes**

For each Codex finding at critical or correctness severity, apply the fix. For each fix, re-run V1 (and V2 if touched by the fix). Commit each fix with a descriptive message. Skip findings that are stylistic opinions or that expand scope beyond the spec.

- [ ] **Step 15.4: Re-run verification after fixes**

Re-run V1, V2, and V4 after any Codex-driven changes. Confirm clean.

---

## Task 16: Push to PR #10

**Files:** none

- [ ] **Step 16.1: Verify branch and upstream**

Run: `git branch --show-current`
Expected: `http-passthrough-simplification-spec`.

Run: `git status`
Expected: clean working tree.

- [ ] **Step 16.2: Push**

Run: `git push`
Expected: branch updated on `origin`, PR #10 picks up the new commits automatically.

- [ ] **Step 16.3: Do not merge**

The user's durable rule (`~/.claude` memory): "branch only; open PR only when asked; merge only when asked." Do not merge PR #10 — leave it open for review.

- [ ] **Step 16.4: Summarize**

Report the commit count, the final diff stat, Codex's verdict summary, and the PR URL back to the user in a short message.

---

## Self-review

Coverage check (spec → tasks):

- Delete `internal/serviceconfig/kubernetes.go` + test → Task 2.
- Delete `internal/kubeconfig/` → Task 4.
- Delete `internal/jwtutil/mutateheaders.go` + test → Task 8.
- Collapse dispatch switch → Task 1.
- Remove `unmutateURI` and its tests → Task 7.
- Simplify `headers.go` → Task 8.
- Remove kubectl route + aws branch in cncserver → Tasks 3 + 6.
- Remove fwdapi types + relax regex → Task 5.
- Remove `HeaderMutationKeyName` field + main.go wiring → Task 8.
- Remove `kubectl` action in get-creds → Task 9.
- Rename kubectl log line → Task 10.
- Audit secrets.go → Task 11.
- README + CLAUDE.md updates → Tasks 12 + 13.
- Verification plan (V1-V5, intentional breakage spot-checks) → Task 14.
- Codex review + push to PR → Tasks 15 + 16.

Placeholder scan: no "TODO"/"TBD"/"implement later" strings introduced. Task 11's two sub-cases are explicit (not a placeholder — the branch depends on code the planner cannot read). Codex-review fixes in Task 15 are by nature unknown in advance; the task specifies the gate (apply critical + correctness; skip style/scope).

Type consistency: references to function names (`ConfigureEndpoints`, `MakeGenericEndpoint`, `generateKubectlComponents`, `unmutateURI`, `PBHEadersToHTTP`, `HTTPHeadersToPB`, `RegisterMutationKeyset`, `MutationIsRegistered`, `getKubeconfigCreds`, `typeValid`, `KubeConfigRequest`, `KubeConfigResponse`, `AwsCredentialResponse`, `BasicCredentialResponse`, `ServiceCredentialRequest`) are consistent across tasks and match the current source.
