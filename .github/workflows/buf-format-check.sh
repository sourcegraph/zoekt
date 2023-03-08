#!/usr/bin/env bash

cd "$(dirname "${BASH_SOURCE[0]}")/../.."
set -euo pipefail

find . -name "*.proto" -not -path ".git" | while read -r proto_file; do
  buf format -w --path "$proto_file"
done

if ! git diff --exit-code; then
  echo "buf format produced changes, please run buf format -w and commit the changes"
  exit 1
fi
