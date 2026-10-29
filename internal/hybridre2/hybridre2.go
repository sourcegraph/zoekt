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
// wasilibs/go-re2 (RE2 via cgo) based on input size.
//
// Motivation: Go's regexp engine lacks a lazy DFA, making it O(n·m) for
// hard patterns. RE2's lazy DFA provides linear-time matching, which is
// dramatically faster for large inputs (>32KB) or complex patterns. For
// small inputs the cgo call overhead of go-re2 exceeds the savings,
// so grafana/regexp remains the better choice there.
//
// The threshold is controlled by the ZOEKT_RE2_THRESHOLD_BYTES environment
// variable, read once on the first Compile. It only takes effect in binaries
// built with -tags re2_cgo (which requires CGO_ENABLED=1, the RE2 and Abseil
// development libraries, pkg-config and a C++ compiler); default builds do
// not link go-re2 and ignore it, logging a warning if it is set to a
// non-negative value:
//
//   - -1 (default): disabled, always use grafana/regexp
//   - 0: always use go-re2
//   - N > 0: use go-re2 when len(input) >= N bytes
//
// # Known tradeoffs
//
// Memory: each Regexp holds compiled state for both engines when RE2 is
// enabled, and RE2's lazy DFA can grow up to go-re2's memory limit (128 MiB
// by default) for as long as the Regexp is alive. Native memory is only
// released when the Regexp is garbage collected, so compile a pattern once
// and reuse it. Monitor RSS when first enabling the threshold in production.
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
//
// Caching: the index package caches compiled patterns of up to 4 KB, so for
// those the memory above is bounded by the cache size (256 entries) rather
// than the number of shards searched. The DFA of each cached Regexp stays
// resident while it is cached.
// A compile that panics costs one shard, as it did before the cache.
package hybridre2

import (
	"log"
	"os"
	"strconv"
	"sync"

	grafanaregexp "github.com/grafana/regexp"
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
var threshold = sync.OnceValue(readThreshold)

func readThreshold() int64 {
	val, ok := os.LookupEnv(envThreshold)
	if !ok {
		return disabled
	}
	n, err := strconv.ParseInt(val, 10, 64)
	if err != nil || n < 0 {
		return disabled
	}
	if !re2Available {
		log.Printf("hybridre2: %s is set but this binary was built without -tags re2_cgo; RE2 stays disabled", envThreshold)
		return disabled
	}
	return n
}

// Regexp is a compiled regular expression that dispatches to either
// grafana/regexp or go-re2 at match time, based on input size.
type Regexp struct {
	grafana *grafanaregexp.Regexp
	re2     *re2Engine // nil when threshold() < 0 (disabled)
}

// Compile returns a new Regexp. The grafana/regexp variant is always compiled.
// The go-re2 variant is only compiled when ZOEKT_RE2_THRESHOLD_BYTES is set to
// a non-negative value; when RE2 is disabled (the default), skipping RE2
// compilation keeps the disabled path truly zero-cost.
func Compile(pattern string) (*Regexp, error) {
	g, err := grafanaregexp.Compile(pattern)
	if err != nil {
		return nil, err
	}
	result := &Regexp{grafana: g}
	if threshold() >= 0 {
		r, err := compileRE2(pattern)
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
