#!/usr/bin/env bash

# This is the psuedo-version that go.mod uses. We use the same version string
# so that sourcegraph/sourcegraph's go.mod is kept in sync with the version we
# publish.

printf "::set-output name=value::"

TZ=UTC git --no-pager show \
  --quiet \
  --abbrev=12 \
  --date='format-local:%Y%m%d%H%M%S' \
  --format="0.0.0-%cd-%h"
