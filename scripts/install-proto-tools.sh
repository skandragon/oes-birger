#!/bin/bash
#
# Copyright 2026 OpsMx, Inc.
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

set -euo pipefail

# Proto tool versions - update these to change versions across the project.
PROTOC_GEN_GO_VERSION="v1.36.11"
PROTOC_GEN_GO_GRPC_VERSION="v1.6.0"

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
BIN_DIR="$PROJECT_ROOT/bin"

echo "Installing protobuf tools to $BIN_DIR..."
mkdir -p "$BIN_DIR"

GOBIN="$BIN_DIR" go install "google.golang.org/protobuf/cmd/protoc-gen-go@$PROTOC_GEN_GO_VERSION"
GOBIN="$BIN_DIR" go install "google.golang.org/grpc/cmd/protoc-gen-go-grpc@$PROTOC_GEN_GO_GRPC_VERSION"

echo "Protobuf tools installed successfully:"
echo "  protoc-gen-go:      $PROTOC_GEN_GO_VERSION"
echo "  protoc-gen-go-grpc: $PROTOC_GEN_GO_GRPC_VERSION"
echo
echo "Note: 'protoc' itself is not a Go tool and must be installed separately"
echo "      (e.g., 'brew install protobuf' or 'apt-get install -y protobuf-compiler')."
