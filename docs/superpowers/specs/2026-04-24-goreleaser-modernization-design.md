# Build + Release Modernization via goreleaser

Date: 2026-04-24
Status: Draft (awaiting user review)

## Context

PR #11 pinned `protoc-gen-go`, `protoc-gen-go-grpc`, and `golangci-lint`
into `./bin/` via install scripts so local and CI toolchains agree. That
PR intentionally stopped at tooling — the release/build surface itself is
still hand-rolled around `make images`, `docker buildx`, and a
timestamp-file scheme that exists only to collect image tags. This spec
tracks the follow-up modernization: adopt [goreleaser](https://goreleaser.com/)
as the single source of truth for builds, images, and releases, matching
the pattern used upstream in
[cardinalhq/lakerunner](https://github.com/cardinalhq/lakerunner).

## Goal

Replace the bespoke `make images` / `buildtime/*.tstamp` /
`make image-names` flow with a goreleaser configuration that:

1. Produces local snapshot builds (for developers) and tag-triggered
   releases (from CI).
2. Emits multi-arch Linux container images for the two existing targets
   (`agent-client`, `agent-controller`) plus an optional `get-creds`
   image, pushed to the same registry the current workflow uses.
3. Ships source archives, binary tarballs, and `checksums.txt` for users
   who want to run outside Kubernetes — today there is no artifact at
   all outside the container image.
4. Keeps version ldflags consistent with what the code currently reads
   (`github.com/OpsMx/go-app-base/version.{buildType,gitHash,gitBranch}`),
   but wires them through goreleaser templating instead of ad-hoc
   `$(shell git describe)` calls in the Makefile.

## Non-goals

- Renaming the Go module or the image tags used by downstream deployers.
- Switching to a new container registry or a different fork of `go-app-base`.
- Code-signing, notarization, Homebrew taps, or SBOM generation. These
  are all possible under goreleaser and should be easy to add later; the
  first pass should match current behavior with less code.
- Replacing the Dockerfile. goreleaser builds binaries outside the
  Dockerfile, but the base image (`alpine:3`, `docker/run.sh`
  entrypoint) should be preserved.

## Current state (what goes away)

| Path | Role today | Disposition |
| --- | --- | --- |
| `Makefile` — `buildtime/*.tstamp` targets (lines ~54-56, ~93-108) | Track "was this multi-arch image built" across targets | Delete; goreleaser handles the loop. |
| `Makefile` — `set-git-info` target (lines ~61-63) | Populate `GIT_BRANCH` / `GIT_HASH` for ldflags | Delete; goreleaser exposes `.Version`, `.Commit`, `.Branch`. |
| `Makefile` — `images` target (lines ~92-108) | Wrap `docker buildx build --push` for each target | Replace with `make release` that shells to goreleaser. |
| `Makefile` — `image-names` target (lines ~110-112) | Emit image tags via deprecated `::set-output` syntax | Delete. goreleaser writes artifacts to `dist/` and we can consume them there. |
| `Dockerfile` — `buildmod` / `build-setup` / `build-*-binaries` stages | Cross-compile binaries inside Docker | Delete; goreleaser builds binaries on the runner and `COPY`s them into the final image. The `base-image` / `agent-client-image` / `agent-controller-image` stages stay, minus the `COPY --from=build-*` lines. |
| `.github/workflows/build-docker-images.yml` | `make images` on tag push | Replace with goreleaser invocation on tag push, plus a snapshot lane on `push` to non-tag refs. |

## Proposed layout

### New files

- `.goreleaser.yaml` — primary config (builds, archives, dockers, manifests, checksums, release).
- Optionally `.goreleaser.snapshot.yaml` if the snapshot lane needs a different docker registry or skip-publish behavior. Probably unnecessary; `goreleaser release --snapshot` handles most of it.

### `.goreleaser.yaml` skeleton

```yaml
version: 2
project_name: oes-birger

before:
  hooks:
    - go mod tidy
    # proto plugins are pinned in scripts/; only regenerate if
    # someone has edited tunnel.proto since the last commit.
    - make generate

builds:
  - id: client
    main: ./app/client
    binary: agent-client
    env: [CGO_ENABLED=0]
    goos: [linux, darwin]
    goarch: [amd64, arm64]
    flags: [-trimpath]
    ldflags:
      - -X github.com/OpsMx/go-app-base/version.buildType={{.Env.BUILD_TYPE}}
      - -X github.com/OpsMx/go-app-base/version.gitHash={{.FullCommit}}
      - -X github.com/OpsMx/go-app-base/version.gitBranch={{.Branch}}
  - id: server
    main: ./app/server
    binary: agent-controller
    env: [CGO_ENABLED=0]
    goos: [linux, darwin]
    goarch: [amd64, arm64]
    flags: [-trimpath]
    ldflags: *ldflags   # share with client
  - id: get-creds
    main: ./app/get-creds
    binary: get-creds
    env: [CGO_ENABLED=0]
    goos: [linux, darwin]
    goarch: [amd64, arm64]
    flags: [-trimpath]
    ldflags: *ldflags

archives:
  - id: default
    builds: [client, server, get-creds]
    name_template: "{{ .ProjectName }}_{{ .Version }}_{{ .Os }}_{{ .Arch }}"

checksum:
  name_template: "checksums.txt"

dockers:
  - id: agent-client
    image_templates:
      - "{{ .Env.IMAGE_PREFIX }}agent-client:{{ .Version }}-amd64"
    goarch: amd64
    dockerfile: docker/Dockerfile.agent-client
    use: buildx
    build_flag_templates:
      - --platform=linux/amd64
    extra_files: [docker/run.sh]
  - id: agent-client-arm64
    image_templates:
      - "{{ .Env.IMAGE_PREFIX }}agent-client:{{ .Version }}-arm64"
    goarch: arm64
    dockerfile: docker/Dockerfile.agent-client
    use: buildx
    build_flag_templates:
      - --platform=linux/arm64
    extra_files: [docker/run.sh]
  # …same pattern for agent-controller…

docker_manifests:
  - name_template: "{{ .Env.IMAGE_PREFIX }}agent-client:{{ .Version }}"
    image_templates:
      - "{{ .Env.IMAGE_PREFIX }}agent-client:{{ .Version }}-amd64"
      - "{{ .Env.IMAGE_PREFIX }}agent-client:{{ .Version }}-arm64"
  - name_template: "{{ .Env.IMAGE_PREFIX }}agent-client:latest"
    image_templates:
      - "{{ .Env.IMAGE_PREFIX }}agent-client:{{ .Version }}-amd64"
      - "{{ .Env.IMAGE_PREFIX }}agent-client:{{ .Version }}-arm64"
  # …same for agent-controller…

release:
  draft: false
  prerelease: auto
```

### Dockerfile changes

The monolithic `Dockerfile` becomes two small per-image files under
`docker/` (matching the way goreleaser's `dockers:` entries point at
specific Dockerfiles). Each one starts from the shared base image,
`COPY`s the pre-built binary from the goreleaser dist dir, and keeps
`EXPOSE` + `CMD` the same. Example:

```Dockerfile
# docker/Dockerfile.agent-client
FROM alpine:3
RUN apk update && apk upgrade --no-cache && apk add --no-cache ca-certificates curl
RUN update-ca-certificates; exit 0
RUN mkdir /local /local/ca-certificates && rm -rf /usr/local/share/ca-certificates && ln -s /local/ca-certificates /usr/local/share/ca-certificates
COPY docker/run.sh /app/run.sh
COPY agent-client /app/
WORKDIR /app
EXPOSE 9102
ENTRYPOINT ["/bin/sh", "/app/run.sh"]
CMD ["/app/agent-client"]
```

goreleaser does the cross-compile; the Dockerfile just copies the
produced binary in. No more `FROM golang:alpine` build stage per image,
no more `touch internal/tunnel/tunnel.pb.go` dance.

### Makefile changes

After this spec lands, the Makefile shrinks. Proposed end state:

- Keep: `all`, `local`, `test`, `lint`, `fmt`, `generate`, `help`, `clean`.
- Delete: `buildtime`, `set-git-info`, `images`, `buildtime/*.tstamp`
  pattern rule, `image-names`, `clean-image-names`, the `now :=`
  variable, and `BUILDX` / `IMAGE_PREFIX` / `IMAGE_TARGETS` vars.
- Add: `release` (goreleaser release) and `snapshot` (goreleaser release
  --snapshot --clean) targets. Pin goreleaser in
  `scripts/install-dev-tools.sh` and wire `bin/goreleaser` into those
  targets.

`make local` stays as today — developers who just want a dev binary
still run `go build` via the Makefile, not goreleaser.

### CI changes

`.github/workflows/build-docker-images.yml` becomes:

```yaml
name: Release
on:
  push:
    tags: ['v[0-9]+.[0-9]+.[0-9]+*']
jobs:
  release:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v6
        with: {fetch-depth: 0}
      - uses: actions/setup-go@v6
        with: {go-version: '1.26.x', cache: true, cache-dependency-path: go.sum}
      - uses: docker/setup-qemu-action@v4
      - uses: docker/setup-buildx-action@v4
      - uses: docker/login-action@v4
        with:
          registry: ${{ secrets.DOCKER_PUBLIC_HOSTNAME }}
          username: ${{ secrets.DOCKER_PUBLIC_USERNAME }}
          password: ${{ secrets.DOCKER_PUBLIC_PASSWORD }}
      - name: Release
        env:
          IMAGE_PREFIX: quay.io/opsmxpublic/
          BUILD_TYPE: release
          GITHUB_TOKEN: ${{ secrets.GITHUB_TOKEN }}
        run: make release
```

An additional `snapshot` job on non-tag push ensures the config keeps
parsing; nothing is published.

## Order of work

1. Land PR #11 (tool pinning). Done.
2. Write `.goreleaser.yaml` and two `docker/Dockerfile.agent-*` files;
   verify `goreleaser release --snapshot --clean` produces identical
   image contents to the current Dockerfile on both arches.
3. Rip out the Makefile `images` / `buildtime` / `set-git-info` /
   `image-names` machinery and the old `Dockerfile`.
4. Switch `.github/workflows/build-docker-images.yml` to the goreleaser
   form above; rename the file to `release.yml`.
5. Bump `scripts/install-dev-tools.sh` to include a pinned goreleaser
   and wire `make release` / `make snapshot` through `./bin/goreleaser`.
6. Dry-run by creating a `v5.0.2-rc1` tag on a throwaway branch pointed
   at a personal registry and verifying the manifest tags match
   `quay.io/opsmxpublic/agent-client:...` behavior.

## Risks / open questions

- **Registry secrets** (`DOCKER_PUBLIC_{HOSTNAME,USERNAME,PASSWORD}`) in
  the current workflow may need adjusting if goreleaser's docker client
  uses a different auth dance than `docker/login-action`. Expected to
  work unchanged (both talk to `$HOME/.docker/config.json`), but needs
  verification on a real tag.
- **Tag scheme drift**. Current `make images` pushes `:latest` and
  `:{{GIT_BRANCH}}` (`$(git describe --tags)` output). goreleaser
  defaults to `:{{.Version}}` (the tag). The `docker_manifests:` block
  above keeps `:latest` and adds `:{{.Version}}`; any downstream puller
  that pins to a `v5.0.1-N-gSHA` style tag will need to switch to the
  semver tag or be handled explicitly.
- **`fetch-depth: 0`** matters: goreleaser needs full history to compute
  changelogs. Double-check the runner has it.
- **No pre-existing release changelog**. First goreleaser release will
  start the changelog from scratch; acceptable.

## References

- #11 — the pinned-tools PR this builds on
- `cardinalhq/lakerunner` — `.goreleaser.build.yaml`,
  `.goreleaser.test-release.yaml`, `.goreleaser-local.yaml`,
  `.github/workflows/build-release.yml`
- goreleaser docs: https://goreleaser.com/customization/
