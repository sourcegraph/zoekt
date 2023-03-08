#!/usr/bin/env bash

# This script updates the JSON schemas in this directory by cloning the
# relevant protos from Google and gRPC, and then running protoc-gen-jsonschema
# on them.

tmpdir="$(mktemp -d)"
function cleanup() {
  rm -rf "$tmpdir"
}

trap cleanup EXIT

cd "$(dirname "${BASH_SOURCE[0]}")"
set -euo pipefail

output_dir="$(pwd)"

if ! command -v protoc-gen-jsonschema &>/dev/null; then
  go install "github.com/chrusty/protoc-gen-jsonschema/cmd/protoc-gen-jsonschema@latest"
fi

# Delete all existing JSON schemas.
find . -name '*.json' -print0 | xargs -0 rm -f

git_clones_dir="${tmpdir}/clones"

mkdir -p "$git_clones_dir"
cd "$git_clones_dir"

function clone_at_commit() {
  local repo="$1"
  local commit="$2"
  local dir="$3"

  mkdir -p "$dir"

  pushd "$dir"

  git init
  git remote add origin "$repo"
  git fetch --depth 1 origin "$commit"
  git checkout FETCH_HEAD

  popd
}

# clone well-known protos from Google and gRPC protos
clone_at_commit "git@github.com:googleapis/googleapis.git" "c959f4214cb3947aa42ded4a14610d0607fcd57a" "${git_clones_dir}/googleapis"
clone_at_commit "git@github.com:grpc/grpc-proto.git" "6956c0ef3b8c21efb44992edc858fbae9414aa05" "${git_clones_dir}/grpc-proto"

cd "$tmpdir"

# prepare protos in a single directory
cp -r "${git_clones_dir}/googleapis/google" .
cp -r "${git_clones_dir}/grpc-proto/grpc" .
cp "${git_clones_dir}/grpc-proto/grpc/service_config/service_config.proto" .

# Generate JSON schemas from protos.

protoc \
  --jsonschema_opt=json_fieldnames \
  --jsonschema_out="$output_dir" \
  -I. \
  service_config.proto
