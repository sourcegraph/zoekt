#!/usr/bin/env bash

cd "$(dirname "${BASH_SOURCE[0]}")/../.."
set -euo pipefail

find . -name "buf.yaml" -not -path ".git" | while read -r buf_yaml; do
  pushd "$(dirname "${buf_yaml}")" >/dev/null

  if ! buf lint .; then
    echo "running buf lint on ${buf_yaml} failed, please examine the output and fix the issues"
    exit 1
  fi

  popd >/dev/null
done
