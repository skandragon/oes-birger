#
# Copyright 2021-2023 OpsMx, Inc.
#
# Licensed under the Apache License, Version 2.0 (the "License")
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#   http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.
#

TARGETS=test local
PLATFORM=linux/amd64,linux/arm64
BUILDX=docker buildx build --pull --platform ${PLATFORM}
IMAGE_PREFIX=docker.flame.org/library/

#
# Build targets.  Adding to these will cause magic to occur.
#

# These are targets for "make local"
BINARIES = client server get-creds

# These are the targets for Docker images, used both for the multi-arch and
# single (local) Docker builds.
# Dockerfiles should have a target that ends in -image, e.g. agent-image.
IMAGE_TARGETS = agent-client agent-controller

#
# Below here lies magic...
#

# Due to the way we build, we will make the universe no matter which files
# actually change.  With the many targets, this is just so much easier,
# and it also ensures the Docker images have identical timestamp-based tags.
pb_deps = internal/tunnel/tunnel.pb.go internal/tunnel/tunnel_grpc.pb.go
all_deps := ${pb_deps} $(shell find app internal -name '*.go' | grep -v _test) Makefile

now := $(shell date -u +%Y%m%dT%H%M%S)

#
# Default target.
#

.PHONY: all
all: ${TARGETS}

#
# Help target - list the common entry points.
#
.PHONY: help
help:
	@echo "oes-birger Makefile targets"
	@echo "==========================="
	@echo ""
	@echo "Building:"
	@echo "  make              - Run tests and build binaries (default)"
	@echo "  make local        - Build binaries into ./bin"
	@echo "  make images       - Build and push multi-arch Docker images"
	@echo ""
	@echo "Testing / quality:"
	@echo "  make test         - go test -race ./..."
	@echo "  make lint         - Run pinned golangci-lint"
	@echo "  make fmt          - gofmt -s -w ."
	@echo ""
	@echo "Code generation:"
	@echo "  make generate     - Install pinned protoc plugins and regenerate .pb.go"
	@echo ""
	@echo "Tool installation (also invoked on demand):"
	@echo "  ./scripts/install-proto-tools.sh  - protoc-gen-go, protoc-gen-go-grpc"
	@echo "  ./scripts/install-dev-tools.sh    - golangci-lint"
	@echo ""
	@echo "Cleanup:"
	@echo "  make clean        - Remove bin/ and build timestamps"
	@echo "  make really-clean - Also remove generated .pb.go files"

#
# make a buildtime directory to hold the build timestamp files
buildtime:
	[ ! -d buildtime ] && mkdir buildtime

#
# set git info details
#
set-git-info:
	@$(eval GIT_BRANCH=$(shell git describe --tags))
	@$(eval GIT_HASH=$(shell git rev-parse ${GIT_BRANCH}))

#
# Local development tooling.  Versions are pinned in the install scripts and
# everything lands in ./bin so builds stay reproducible across machines and CI.
#

bin/protoc-gen-go bin/protoc-gen-go-grpc:
	./scripts/install-proto-tools.sh

bin/golangci-lint:
	./scripts/install-dev-tools.sh

#
# Common components, like GRPC client code generation.  The generated files
# are committed, so CI and plain `make test` never need protoc; only the
# explicit `make generate` path installs the pinned plugins and regenerates.
#

.PHONY: generate
generate: bin/protoc-gen-go bin/protoc-gen-go-grpc
	PATH="$(CURDIR)/bin:$$PATH" protoc --go_out=. \
		--go_opt=paths=source_relative \
		--go-grpc_out=. \
		--go-grpc_opt=paths=source_relative \
		internal/tunnel/tunnel.proto

internal/tunnel/tunnel.pb.go: go.mod internal/tunnel/tunnel.proto
	$(MAKE) generate

#
# Build locally, mostly for development speed.
#

.PHONY: local
local: $(addprefix bin/,$(BINARIES))

$(addprefix bin/,$(BINARIES)): bin/%: set-git-info ${all_deps}
	@[ -d bin ] || mkdir bin
	go build -o $@ \
		-ldflags="-X 'github.com/OpsMx/go-app-base/version.buildType=dev' -X 'github.com/OpsMx/go-app-base/version.gitHash=${GIT_HASH}' -X 'github.com/OpsMx/go-app-base/version.gitBranch=${GIT_BRANCH}'" \
		app/$*/*.go

#
# Multi-architecture image builds
#
.PHONY: images
images: buildtime clean-image-names set-git-info $(addsuffix .tstamp, $(addprefix buildtime/,$(IMAGE_TARGETS)))

buildtime/%.tstamp:: ${all_deps} Dockerfile
	touch ${pb_deps}
	${BUILDX} \
		--tag ${IMAGE_PREFIX}$(patsubst %.tstamp,%,$(@F)):latest \
		--tag ${IMAGE_PREFIX}$(patsubst %.tstamp,%,$(@F)):${GIT_BRANCH} \
		--target $(patsubst %.tstamp,%,$(@F))-image \
		--build-arg GIT_HASH=${GIT_HASH} \
		--build-arg GIT_BRANCH=${GIT_BRANCH} \
		--build-arg BUILD_TYPE=release \
		-f Dockerfile \
		--push .
	echo >> buildtime/image-names.txt ${IMAGE_PREFIX}$(patsubst %.tstamp,%,$(@F)):latest
	echo >> buildtime/image-names.txt ${IMAGE_PREFIX}$(patsubst %.tstamp,%,$(@F)):${GIT_BRANCH}
	@touch $@

.PHONY: image-names
image-names:
	@echo ::set-output name=imageNames::$(shell echo `cat buildtime/image-names.txt` | sed 's/\ /,\ /g')

#
# Test targets
#

.PHONY: test
test: ${pb_deps}
	go test -race ./...

#
# Lint target - uses the pinned golangci-lint from ./bin.
#
.PHONY: lint
lint: bin/golangci-lint
	./bin/golangci-lint run --timeout 5m ./...

#
# Format target.
#
.PHONY: fmt
fmt:
	gofmt -s -w .

#
# Clean the world.
#

.PHONY: clean
clean: clean-image-names
	rm -f buildtime/*.tstamp
	rm -f bin/*

.PHONY: really-clean
really-clean: clean
	rm -f ${pb_deps}

.PHONY: clean-image-names
clean-image-names:
	rm -f buildtime/image-names.txt
