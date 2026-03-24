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

package hybridre2

import (
	"fmt"
	"testing"

	grafanaregexp "github.com/grafana/regexp"
)

// withThreshold overrides the effective threshold for the duration of the test
// and registers a t.Cleanup to restore it afterwards.
//
// NOT safe for concurrent use: do not call t.Parallel() after withThreshold,
// and do not use it from TestMain or init().
func withThreshold(tb testing.TB, thresh int64) {
	tb.Helper()
	old := threshold
	threshold = func() int64 { return thresh }
	tb.Cleanup(func() { threshold = old })
}

// ---- unit tests ----

func TestCompileValid(t *testing.T) {
	_, err := Compile(`foo.*bar`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestCompileInvalid(t *testing.T) {
	_, err := Compile(`[invalid`)
	if err == nil {
		t.Fatal("expected error for invalid pattern, got nil")
	}
}

func TestMustCompilePanics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("MustCompile should panic on invalid pattern")
		}
	}()
	MustCompile(`[invalid`)
}

func TestString(t *testing.T) {
	const pat = `foo.*bar`
	re := MustCompile(pat)
	if re.String() != pat {
		t.Fatalf("String() = %q, want %q", re.String(), pat)
	}
}

// TestFindAllIndexDisabled checks that with threshold=-1, we use grafana/regexp.
func TestFindAllIndexDisabled(t *testing.T) {
	corpus := []byte("func main() { fmt.Println(\"hello world\") }")
	patterns := []string{`\w+`, `fmt\.\w+`, `(?i)MAIN`, `"[^"]*"`}

	withThreshold(t, disabled)
	for _, pat := range patterns {
		hybrid := MustCompile(pat)
		grafana := grafanaregexp.MustCompile(pat)
		got := hybrid.FindAllIndex(corpus, -1)
		want := grafana.FindAllIndex(corpus, -1)
		if !equalIndexSlices(got, want) {
			t.Errorf("disabled mode, pattern %q: hybrid=%v grafana=%v", pat, got, want)
		}
	}
}

// TestFindAllIndexForcedRE2 checks that with threshold=0, go-re2 is used and
// produces identical results to grafana/regexp for standard patterns.
func TestFindAllIndexForcedRE2(t *testing.T) {
	corpus := []byte("func main() { fmt.Println(\"hello world\") }")
	patterns := []string{`\w+`, `fmt\.\w+`, `(?i)MAIN`, `"[^"]*"`}

	withThreshold(t, 0)
	for _, pat := range patterns {
		hybrid := MustCompile(pat)
		grafana := grafanaregexp.MustCompile(pat)
		got := hybrid.FindAllIndex(corpus, -1)
		want := grafana.FindAllIndex(corpus, -1)
		if !equalIndexSlices(got, want) {
			t.Errorf("forced-re2 mode, pattern %q: hybrid=%v grafana=%v", pat, got, want)
		}
	}
}

// TestThresholdSwitching verifies the engine switches at the configured byte boundary.
func TestThresholdSwitching(t *testing.T) {
	const thresh = int64(512)
	pattern := `func\s+\w+`
	grafana := grafanaregexp.MustCompile(pattern)

	smallCorpus := makeCorpus(300) // < 512
	largeCorpus := makeCorpus(600) // >= 512

	withThreshold(t, thresh)
	hybrid := MustCompile(pattern)

	for _, tc := range []struct {
		name   string
		corpus []byte
	}{
		{"small(<threshold)", smallCorpus},
		{"large(>=threshold)", largeCorpus},
	} {
		got := hybrid.FindAllIndex(tc.corpus, -1)
		want := grafana.FindAllIndex(tc.corpus, -1)
		if !equalIndexSlices(got, want) {
			t.Errorf("%s: hybrid=%v grafana=%v", tc.name, got, want)
		}
	}
}

// TestFindAllIndexIdenticalResults is a comprehensive correctness sweep across
// pattern types and input sizes, asserting identical match positions.
func TestFindAllIndexIdenticalResults(t *testing.T) {
	patterns := []struct {
		name    string
		pattern string
	}{
		{"literal", `hello`},
		{"case-insensitive", `(?i)Hello`},
		{"word-boundary", `\bfunc\b`},
		{"alternation", `foo|bar|baz`},
		{"char-class", `[a-zA-Z_]\w*`},
		{"complex", `(func|var|const)\s+[A-Z]\w*`},
		{"dot-plus", `.+`},
		{"anchored-line", `(?m)^package\s+\w+`},
		{"no-match", `XYZZY_NEVER_MATCHES`},
	}

	sizes := []struct {
		name string
		size int
	}{
		{"64B", 64},
		{"512B", 512},
		{"4KB", 4 * 1024},
		{"64KB", 64 * 1024},
		{"256KB", 256 * 1024},
	}

	// Force re2 path to test its correctness across all sizes.
	withThreshold(t, 0)
	for _, sz := range sizes {
		corpus := makeCorpus(sz.size)
		for _, pat := range patterns {
			name := sz.name + "/" + pat.name
			t.Run(name, func(t *testing.T) {
				hybrid := MustCompile(pat.pattern)
				grafana := grafanaregexp.MustCompile(pat.pattern)

				got := hybrid.FindAllIndex(corpus, -1)
				want := grafana.FindAllIndex(corpus, -1)
				if !equalIndexSlices(got, want) {
					t.Errorf("pattern=%q size=%d: len(hybrid)=%d len(grafana)=%d",
						pat.pattern, sz.size, len(got), len(want))
					if len(got) > 0 && len(want) > 0 {
						t.Errorf("  first hybrid=%v first grafana=%v", got[0], want[0])
					}
				}
			})
		}
	}
}

// TestFindAllIndexLimitN verifies that the n parameter (match count limit) is
// honoured identically by both engines.
func TestFindAllIndexLimitN(t *testing.T) {
	corpus := makeCorpus(64 * 1024) // large enough to have many matches
	patterns := []string{`func\s+\w+`, `\bvar\b`, `[A-Z]\w*`}

	withThreshold(t, 0) // force re2 path
	for _, pat := range patterns {
		hybrid := MustCompile(pat)
		grafana := grafanaregexp.MustCompile(pat)

		got := hybrid.FindAllIndex(corpus, 1)
		want := grafana.FindAllIndex(corpus, 1)
		if !equalIndexSlices(got, want) {
			t.Errorf("n=1, pattern=%q: hybrid=%v grafana=%v", pat, got, want)
		}
		// Sanity: n=1 should return at most one match.
		if len(got) > 1 {
			t.Errorf("n=1, pattern=%q: got %d matches, want <= 1", pat, len(got))
		}
	}
}

// TestNoMatchReturnsEmpty verifies no-match returns nil/empty consistently.
func TestNoMatchReturnsEmpty(t *testing.T) {
	corpus := makeCorpus(1024)

	for _, thresh := range []int64{disabled, 0} {
		t.Run(fmt.Sprintf("thresh=%d", thresh), func(t *testing.T) {
			withThreshold(t, thresh)
			// MustCompile must be after withThreshold so that the lazy RE2
			// compilation in Compile() sees the overridden threshold and
			// actually initialises re.re2 when thresh=0.
			re := MustCompile(`XYZZY_NEVER_MATCHES`)
			if got := re.FindAllIndex(corpus, -1); len(got) != 0 {
				t.Errorf("thresh=%d: expected empty, got %v", thresh, got)
			}
		})
	}
}

// ---- benchmarks ----

// BenchmarkEngines measures FindAllIndex performance for grafana/regexp vs
// go-re2 across multiple input sizes and pattern complexities.
//
// Run with:
//
//	go test -bench=BenchmarkEngines -benchmem -benchtime=3s ./internal/hybridre2/
func BenchmarkEngines(b *testing.B) {
	patterns := []struct {
		name    string
		pattern string
	}{
		{"literal", `main`},
		{"case-insensitive", `(?i)func`},
		{"alternation-5", `func|var|const|type|import`},
		{"complex", `(func|var)\s+[A-Z]\w*\s*\(`},
		{"hard-no-match", `XYZZY_NEVER_MATCHES_AT_ALL`},
	}

	sizes := []struct {
		name string
		size int
	}{
		{"512B", 512},
		{"4KB", 4 * 1024},
		{"32KB", 32 * 1024},
		{"128KB", 128 * 1024},
		{"512KB", 512 * 1024},
	}

	// Pre-build all corpora outside the benchmark loop.
	corpora := make(map[string][]byte, len(sizes))
	for _, sz := range sizes {
		corpora[sz.name] = makeCorpus(sz.size)
	}

	for _, pat := range patterns {
		grafanaRe := grafanaregexp.MustCompile(pat.pattern)

		for _, sz := range sizes {
			corpus := corpora[sz.name]
			name := pat.name + "/" + sz.name

			b.Run("grafana/"+name, func(b *testing.B) {
				b.SetBytes(int64(len(corpus)))
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					_ = grafanaRe.FindAllIndex(corpus, -1)
				}
			})

			b.Run("go-re2/"+name, func(b *testing.B) {
				withThreshold(b, 0) // force re2 for all sizes
				// MustCompile must be after withThreshold so that re.re2
				// is initialised (lazy compilation checks threshold() at
				// compile time, not match time).
				hybridRe := MustCompile(pat.pattern)
				b.SetBytes(int64(len(corpus)))
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					_ = hybridRe.FindAllIndex(corpus, -1)
				}
			})
		}
	}
}

// ---- helpers ----

// makeCorpus returns a realistic-looking Go source corpus of approximately
// the requested size.
func makeCorpus(size int) []byte {
	const template = `package main

import (
	"fmt"
	"strings"
)

// Foo is an exported function that transforms its input.
func Foo(input string) string {
	return strings.ToUpper(input)
}

// Bar demonstrates calling Foo.
func Bar() {
	result := Foo("hello world")
	fmt.Println(result)
}

var globalVar = "some value"
const MaxItems = 100

type MyStruct struct {
	Name  string
	Value int
}

func (m MyStruct) String() string {
	return fmt.Sprintf("%s=%d", m.Name, m.Value)
}

`
	buf := make([]byte, 0, size)
	for len(buf) < size {
		buf = append(buf, []byte(template)...)
	}
	return buf[:size]
}

func equalIndexSlices(a, b [][]int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !equalIntSlice(a[i], b[i]) {
			return false
		}
	}
	return true
}

func equalIntSlice(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
