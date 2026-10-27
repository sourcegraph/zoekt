# Optional RE2 regexp engine (experimental)

Zoekt matches regexps with [grafana/regexp](https://github.com/grafana/regexp),
a fork of Go's `regexp`. Go's engine has no lazy DFA, so complex patterns can
be slow on large files. Zoekt can optionally use [RE2](https://github.com/google/re2)
for the content of large files. This is experimental and off by default.

## How it works

When RE2 is enabled, each content regexp is compiled for both engines. At
match time, zoekt uses RE2 when the file content is at least `ZOEKT_RE2_THRESHOLD_BYTES` bytes long,
and grafana/regexp otherwise. Small inputs stay on grafana/regexp because the
call overhead of RE2 outweighs its speedup there. File name matching always
uses grafana/regexp.

## Enabling it

RE2 is only available in binaries built with `CGO_ENABLED=1` and the
`re2_cgo` build tag. This needs the `libre2` and Abseil development packages,
`pkg-config` and a C++ compiler, for example `libre2-dev`, `libabsl-dev`,
`pkg-config` and `g++` on Debian:

```
CGO_ENABLED=1 go build -tags re2_cgo ./cmd/zoekt-webserver
```

The published Dockerfile builds with `CGO_ENABLED=0`, so images built from it
ignore the setting.

Then set `ZOEKT_RE2_THRESHOLD_BYTES` for the process. It is read once, on the
first regexp compile:

| Value | Behavior |
|-------|----------|
| unset, `-1` | RE2 is off. |
| `0` | Always use RE2. Useful for testing. |
| `N` > 0 | Use RE2 for content of at least `N` bytes. |

In a binary built without the tag, a non-negative value has no effect and
zoekt logs a warning on the first content regexp search (file name only
regexps don't trigger it). `-1` and unset values don't log. The `zoekt`
command discards log output unless you pass `-v`.

## Known tradeoffs

- RE2 stops at invalid UTF-8, while grafana/regexp replaces invalid bytes with
  U+FFFD and continues, so results can differ on binary content.
- RE2 can reject a pattern that grafana/regexp accepts. Compiling then
  returns an error instead of falling back.
- Each compiled regexp keeps RE2's lazy DFA, which can grow up to go-re2's
  memory limit (128 MiB by default) and stays in memory for as long as the
  regexp is alive. Monitor RSS when you first enable the threshold.
