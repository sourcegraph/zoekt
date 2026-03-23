// Copyright 2026 Google Inc. All rights reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//    http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Package hybridre2 provides a hybrid regex engine that switches between
// grafana/regexp (an optimized fork of Go's stdlib regexp) and
// wasilibs/go-re2 (RE2 via WebAssembly) based on input size.
//
// Motivation: Go's regexp engine lacks a lazy DFA, making it O(n·m) for
// hard patterns. RE2's lazy DFA provides linear-time matching, which is
// dramatically faster for large inputs (>32KB) or complex patterns. For
// small inputs the WASM call overhead of go-re2 exceeds the savings,
// so grafana/regexp remains the better choice there.
//
// The threshold is controlled by the ZOEKT_RE2_THRESHOLD_BYTES environment
// variable, read once at program startup:
//
//   - -1 (default): disabled, always use grafana/regexp
//   - 0: always use go-re2
//   - N > 0: use go-re2 when len(input) >= N bytes
//
// # Known tradeoffs
//
// Memory: each Regexp holds compiled state for both engines when RE2 is
// enabled. Patterns are compiled per-search (not cached globally), so under
// high concurrency with many unique patterns the WASM heap adds up. Monitor
// RSS when first enabling the threshold in production.
//
// UTF-8 semantics: go-re2 stops at invalid UTF-8; grafana/regexp replaces
// invalid bytes with U+FFFD and continues. Results may differ on binary
// content that slips past content-type detection. See FindAllIndex for
// details.
//
// RE2 compilation failure: if RE2 rejects a pattern that grafana/regexp
// accepts (due to syntax differences between the two engines), Compile
// returns an error rather than silently falling back to grafana/regexp.
// This is intentional (fail-fast), but it means enabling the threshold
// could surface errors for edge-case patterns that work today. Patterns
// sourced from zoekt query parsing are validated before reaching this
// package, so this is unlikely in practice.
package hybridre2

import (
	"os"
	"strconv"
	"sync"

	grafanaregexp "github.com/grafana/regexp"
	re2regexp "github.com/wasilibs/go-re2"
)

const (
	// envThreshold is the environment variable name controlling the size
	// threshold (bytes) at which go-re2 is used instead of grafana/regexp.
	// Set to -1 (default) to disable go-re2 entirely, 0 to always use it.
	envThreshold = "ZOEKT_RE2_THRESHOLD_BYTES"

	// disabled is the sentinel value meaning go-re2 is never used.
	disabled = int64(-1)
)

// threshold returns the configured byte threshold, reading
// ZOEKT_RE2_THRESHOLD_BYTES from the environment exactly once.
// Negative means disabled; zero means always use RE2.
//
// Tests may reassign this variable to override the threshold.
var threshold = sync.OnceValue(func() int64 {
	if val, ok := os.LookupEnv(envThreshold); ok {
		if n, err := strconv.ParseInt(val, 10, 64); err == nil {
			return n
		}
	}
	return disabled
})

// Regexp is a compiled regular expression that dispatches to either
// grafana/regexp or go-re2 at match time, based on input size.
type Regexp struct {
	grafana *grafanaregexp.Regexp
	re2     *re2regexp.Regexp // nil when threshold() < 0 (disabled)
}

// Compile returns a new Regexp. The grafana/regexp variant is always compiled.
// The go-re2 variant is only compiled when ZOEKT_RE2_THRESHOLD_BYTES is set to
// a non-negative value; when RE2 is disabled (the default), skipping WASM
// compilation keeps the disabled path truly zero-cost.
func Compile(pattern string) (*Regexp, error) {
	g, err := grafanaregexp.Compile(pattern)
	if err != nil {
		return nil, err
	}
	result := &Regexp{grafana: g}
	if threshold() >= 0 {
		r, err := re2regexp.Compile(pattern)
		if err != nil {
			return nil, err
		}
		result.re2 = r
	}
	return result, nil
}

// MustCompile is like Compile but panics on error.
func MustCompile(pattern string) *Regexp {
	re, err := Compile(pattern)
	if err != nil {
		panic("hybridre2: Compile(" + pattern + "): " + err.Error())
	}
	return re
}

// useRE2 reports whether the RE2 engine should be used for an input of the
// given length, based on the current threshold setting.
func useRE2(inputLen int) bool {
	t := threshold()
	return t >= 0 && int64(inputLen) >= t
}

// FindAllIndex returns successive non-overlapping matches of the expression
// in b. It uses go-re2 when len(b) >= threshold() (and RE2 is enabled),
// and grafana/regexp otherwise. Match indices are relative to b.
//
// NOTE: go-re2 stops matching at invalid UTF-8 bytes, whereas grafana/regexp
// replaces them with U+FFFD and continues. This means results may differ on
// binary or non-UTF-8 content when RE2 is active. The default threshold of -1
// (disabled) ensures zero behaviour change for existing deployments; operators
// enabling the threshold should be aware of this distinction.
func (re *Regexp) FindAllIndex(b []byte, n int) [][]int {
	if re.re2 != nil && useRE2(len(b)) {
		return re.re2.FindAllIndex(b, n)
	}
	return re.grafana.FindAllIndex(b, n)
}

// String returns the source text used to compile the regular expression.
func (re *Regexp) String() string {
	return re.grafana.String()
}
