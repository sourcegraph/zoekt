# Zoekt Coding Agent Guidelines

## Build & Test Commands
- Build: `go build ./cmd/...`
- Run all tests: `go test ./... -short`
- Run a single test: `go test -run=TestName ./path/to/package`
- Run specific test with verbose output: `go test -v -run=TestName ./path/to/package`
- Benchmark: `go test -bench=BenchmarkName ./path/to/package`
- Fuzzing: `go test -fuzz=FuzzTestName -fuzztime=30s ./package`
- Smoke test: Check a specific repo: `go run ./cmd/zoekt-git-index /path/to/repo`

## Code Style Guidelines
- Import format: standard Go imports (stdlib, external, internal) with alphabetical sorting
- Error handling: explicit error checking with proper returns (no ignored errors)
- Naming: Go standard (CamelCase for exported, camelCase for private)
- Tests: Table-driven tests preferred with descriptive names
- Documentation: All exported functions should have comments
- Shell scripts: Use shfmt with `-i 2 -ci -bn` flags
- Proto files: Run buf lint and format checks
- Memory optimization: As a code search database, Zoekt is memory-sensitive - be conscious of struct field ordering and memory usage in core structures

## Documentation Resources
- Design overview: `doc/design.md` - Core architecture and search methodology
- Indexing details: `doc/indexing.md` - How the indexing process works
- Query syntax: `doc/query_syntax.md` - Search query language reference
- FAQ: `doc/faq.md` - Common questions and troubleshooting