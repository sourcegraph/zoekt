#!/usr/bin/env bash

set -euo pipefail

# This is the pseudo-version that go.mod uses. We use the same version string
# so that downstream consumers can line up image versions with module versions.
version="$(TZ=UTC git --no-pager show \
	--quiet \
	--abbrev=12 \
	--date='format-local:%Y%m%d%H%M%S' \
	--format='0.0.0-%cd-%h')"

printf 'value=%s\n' "$version" >>"$GITHUB_OUTPUT"
