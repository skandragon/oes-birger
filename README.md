# Birger API Forwarder

> **Fork notice.** This repository is a community fork of the former
> `opsmx/oes-birger` project, which was originally published under the
> Apache License 2.0 by OpsMx, Inc. and is no longer available upstream.
> This fork is maintained independently and is diverging from the
> original: expect behavior, configuration, module path, and image
> names to change over time. Pre-existing OpsMx copyright notices are
> preserved in accordance with Apache-2.0; new contributions are
> copyright their respective authors. "OpsMx", "Spinnaker", and
> related product names are trademarks of their respective owners and
> are referenced here only descriptively.

[![Go Report Card](https://goreportcard.com/badge/github.com/opsmx/oes-birger)](https://goreportcard.com/report/github.com/opsmx/oes-birger)

# Generic HTTP proxy

This is a slightly protocol aware HTTP proxy, which securely crosses
security domains.  The primary use case is for a SaaS install of some
central software which needs to reach into a customer's cloud in some
secure way.  VPNs could be used, which requires out of band configuration
and likely more complexity and teams.

Birger runs an agent alongside the services to be exposed, configured
with the credentials those services expect.  The agent dials out to a
controller; clients contact the controller, which forwards HTTP requests
back to the agent, which in turn calls the local service.  Credentials
used by the agent never leave the customer side.

## Using the Services

The controller has a HTTPS port open which accepts service requests.
Clients authenticate with a controller-issued JWT carried as a Bearer
token in the `Authorization` header, or as a basic-auth password — both
are accepted.

Streaming (aka, "watch") requests are supported.  Data is sent back from
an API request in a streaming fashion in all cases.  Multiple simultaneous
API calls are supported.

# Components

There are two main components: a "controller" and an "agent".  The
controller runs somewhere a client can reach it, and the client is
pointed at the controller using a CA cert to verify the controller's
TLS certificate.  The controller then, based on the server name used in
the request and the JWT claims, forwards the request to a connected
agent, which uses its own credentials to call the local service.

The "agent" connects to a "controller" (which lives outside the firewall,
likely colocated with a CI/CD system) which allows access to the agent's
local services, based on permissions granted to the agent.

Running more than one agent with the same name is supported.  If more than
one agent with the same name is connected, they all receive requests, with
the specific agent chosen at random per request.

The agent is very small, and as it uses a small alpine Linux base image
with few additional packages, has a very small security footprint.

# Prerequisites

`$ go install google.golang.org/protobuf/cmd/protoc-gen-go`

`$ go install google.golang.org/grpc/cmd/protoc-gen-go-grpc`

# Building

`$ make`

# Running

See the examples in the `examples/local-deploy` directory.

# Certificates

There is a binary called `make-ca` which will generate a new certificate
authority, and an initial "control" client key.  These keys and
certificates are created in the Kubernetes secret YAML format.

The CA key and certificate are used by the controller to generate its
own server certificate on startup with all the defined server names.
Client authentication to the controller is JWT-based — the CNC API
issues `agent`, `service`, and `control` purpose JWTs (see `get-creds`).

# Service types

The `type` field on an `outgoingService` is a free-form lowercase
identifier (matching `^[a-z0-9][a-z0-9-]*$`).  It names the endpoint
for routing and appears in logs and downstream UIs.  No type name has
special code-path semantics — every endpoint is dispatched as a generic
HTTP passthrough.  Configure credentials (`basic`, `bearer`, `token`,
or `none`) under the `credentials` block on each service.

Operators commonly use descriptive labels such as `jenkins`, `argocd`,
`clouddriver`, or custom labels like `x-my-api`, but these are
conventions, not required names.

# Annotations

A list of annotations, which are `key: value` pairs in the YAML configuration, can be added to any
`outgoingService` or `agentInfo`.  See `examples/local-deploy/config/agent/config.yaml` and
`examples/local-deploy/config/agent/services.yaml`.

## Annotation Registry

While any annotataion can be listed and retrieved via the controller's API, some have special
meaning.

| Name | Context | Description |
| --- | --- | --- |
| description | any | A description of this object, perhaps to be displayed in the UI and log messages. |
| uiUrl | any | A URL used by downstream UIs to link to the underlying service.  This repository's code does not read or enforce the annotation; interpretation is up to the UI. |
