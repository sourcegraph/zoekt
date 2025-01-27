// Copyright 2016 Google Inc. All rights reserved.
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

package build

import (
	"context"
	"math"
	"os"
	"testing"

	"github.com/sourcegraph/zoekt"
	"github.com/sourcegraph/zoekt/internal/ctags"
	"github.com/sourcegraph/zoekt/internal/shards"
	"github.com/sourcegraph/zoekt/query"
)

type scoreCase struct {
	fileName          string
	content           []byte
	query             query.Q
	language          string
	wantScore         float64
	wantBestLineMatch uint32
}

func TestFileNameMatch(t *testing.T) {
	cases := []scoreCase{
		{
			fileName: "a/b/c/config.go",
			query:    &query.Substring{FileName: true, Pattern: "config"},
			language: "Go",
			// 5500 (partial base at boundary) + 500 (word)
			wantScore: 6000,
		},
		{
			fileName: "a/b/c/config.go",
			query:    &query.Substring{FileName: true, Pattern: "config.go"},
			language: "Go",
			// 7000 (full base match) + 500 (word)
			wantScore: 7500,
		},
		{
			fileName: "a/config/c/d.go",
			query:    &query.Substring{FileName: true, Pattern: "config"},
			language: "Go",
			// 500 (word)
			wantScore: 500,
		},
	}

	for _, c := range cases {
		checkScoring(t, c, false, ctags.UniversalCTags)
	}
}

func TestBM25(t *testing.T) {
	exampleJava, err := os.ReadFile("./testdata/example.java")
	if err != nil {
		t.Fatal(err)
	}

	cases := []scoreCase{
		{
			// Matches on both filename and content
			fileName: "example.java",
			query:    &query.Substring{Pattern: "example"},
			content:  exampleJava,
			language: "Java",
			// bm25-score: 0.58 <- sum-termFrequencyScore: 14.00, length-ratio: 1.00
			wantScore: 0.58,
			// line 5:    private final int exampleField;
			wantBestLineMatch: 5,
		}, {
			// Matches only on content
			fileName: "example.java",
			query: &query.And{Children: []query.Q{
				&query.Substring{Pattern: "inner"},
				&query.Substring{Pattern: "static"},
				&query.Substring{Pattern: "interface"},
			}},
			content:  exampleJava,
			language: "Java",
			// bm25-score: 1.81 <- sum-termFrequencyScore: 116.00, length-ratio: 1.00
			wantScore: 1.81,
			// line 54: private static <A, B> B runInnerInterface(InnerInterface<A, B> fn, A a) {
			wantBestLineMatch: 54,
		}, {
			// Another content-only match
			fileName: "example.java",
			query: &query.And{Children: []query.Q{
				&query.Substring{Pattern: "system"},
				&query.Substring{Pattern: "time"},
			}},
			content:  exampleJava,
			language: "Java",
			// bm25-score: 0.96 <- sum-termFrequencies: 12, length-ratio: 1.00
			wantScore: 0.96,
			// line 59: if (System.nanoTime() > System.currentTimeMillis()) {
			wantBestLineMatch: 59,
		},
		{
			// Matches only on filename
			fileName: "example.java",
			query:    &query.Substring{Pattern: "java"},
			content:  exampleJava,
			language: "Java",
			// bm25-score: 0.51 <- sum-termFrequencyScore: 5.00, length-ratio: 1.00
			wantScore: 0.51,
		},
		{
			// Matches only on filename, and content is missing
			fileName: "a/b/c/config.go",
			query:    &query.Substring{Pattern: "config.go"},
			language: "Go",
			// bm25-score: 0.60 <- sum-termFrequencyScore: 5.00, length-ratio: 0.00
			wantScore: 0.60,
		},
	}

	for _, c := range cases {
		checkScoring(t, c, true, ctags.UniversalCTags)
	}
}

func TestJava(t *testing.T) {
	exampleJava, err := os.ReadFile("./testdata/example.java")
	if err != nil {
		t.Fatal(err)
	}

	cases := []scoreCase{
		{
			fileName: "example.java",
			content:  exampleJava,
			query:    &query.Substring{Content: true, Pattern: "nerClass"},
			language: "Java",
			// 5500 (partial symbol at boundary) + 1000 (Java class) + 50 (partial word)
			wantScore: 6550,
			// line 37: public class InnerClass implements InnerInterface<Integer, Integer> {
			wantBestLineMatch: 37,
		},
		{
			fileName: "example.java",
			content:  exampleJava,
			query:    &query.Substring{Content: true, Pattern: "StaticClass"},
			language: "Java",
			// 5500 (partial symbol at boundary) + 1000 (Java class) + 500 (word)
			wantScore: 7000,
			// line 32:   public static class InnerStaticClass {
			wantBestLineMatch: 32,
		},
		{
			fileName: "example.java",
			content:  exampleJava,
			query:    &query.Substring{Content: true, Pattern: "innerEnum"},
			language: "Java",
			// 7000 (symbol) + 900 (Java enum) + 500 (word)
			wantScore: 8400,
			// line 16:   public enum InnerEnum {
			wantBestLineMatch: 16,
		},
		{
			fileName: "example.java",
			content:  exampleJava,
			query:    &query.Substring{Content: true, Pattern: "innerInterface"},
			language: "Java",
			// 7000 (symbol) + 800 (Java interface) + 500 (word)
			wantScore: 8300,
			// line 22:     public interface InnerInterface<A, B> {
			wantBestLineMatch: 22,
		},
		{
			fileName: "example.java",
			content:  exampleJava,
			query:    &query.Substring{Content: true, Pattern: "innerMethod"},
			language: "Java",
			// 7000 (symbol) + 700 (Java method) + 500 (word)
			wantScore: 8200,
			// line 44:     public void innerMethod() {
			wantBestLineMatch: 44,
		},
		{
			fileName: "example.java",
			content:  exampleJava,
			query:    &query.Substring{Content: true, Pattern: "field"},
			language: "Java",
			// 7000 (symbol) + 600 (Java field) + 500 (word)
			wantScore: 8100,
			// line 38:     private final int field;
			wantBestLineMatch: 38,
		},
		{
			fileName: "example.java",
			content:  exampleJava,
			query:    &query.Substring{Content: true, Pattern: "B"},
			language: "Java",
			// 7000 (symbol) + 500 (Java enum constant) + 500 (word)
			wantScore: 8000,
			// line 18:     B,
			wantBestLineMatch: 18,
		},
		// 2 Atoms (1x content and 1x filename)
		{
			fileName: "example.java",
			content:  exampleJava,
			query:    &query.Substring{Pattern: "example"}, // matches filename and a Java field
			language: "Java",
			// 5500 (edge symbol) + 600 (Java field) + 500 (word) + 200 (atom)
			wantScore: 6800,
			// line 5:   private final int exampleField;
			wantBestLineMatch: 5,
		},
		// 3 Atoms (2x content, 1x filename)
		{
			fileName: "example.java",
			content:  exampleJava,
			query: &query.Or{Children: []query.Q{
				&query.Substring{Pattern: "example"},                          // matches filename and Java field
				&query.Substring{Content: true, Pattern: "runInnerInterface"}, // matches a Java method
			}},
			language: "Java",
			// 7000 (symbol) + 700 (Java method) + 500 (word) + 266.67 (atom)
			wantScore: 8466,
			// line 54:   private static <A, B> B runInnerInterface(InnerInterface<A, B> fn, A a) {
			wantBestLineMatch: 54,
		},
		// 4 Atoms (4x content)
		{
			fileName: "example.java",
			content:  exampleJava,
			query: &query.Or{Children: []query.Q{
				&query.Substring{Content: true, Pattern: "testAnon"},
				&query.Substring{Content: true, Pattern: "Override"},
				&query.Substring{Content: true, Pattern: "InnerEnum"},
				&query.Substring{Content: true, Pattern: "app"},
			}},
			language: "Java",
			// 7000 (symbol) + 900 (Java enum) + 500 (word) + 300 (atom)
			wantScore: 8700,
			// line 16:   public enum InnerEnum {
			wantBestLineMatch: 16,
		},
		{
			fileName: "example.java",
			content:  exampleJava,
			query:    &query.Substring{Content: true, Pattern: "unInnerInterface("},
			language: "Java",
			// 4000 (overlap Symbol) + 700 (Java method) + 50 (partial word)
			wantScore: 4750,
			// line 54:   private static <A, B> B runInnerInterface(InnerInterface<A, B> fn, A a) {
			wantBestLineMatch: 54,
		},
		{
			fileName: "example.java",
			content:  exampleJava,
			query:    &query.Substring{Content: true, Pattern: "InnerEnum"},
			language: "Java",
			// 7000 (Symbol) + 900 (Java enum) + 500 (word)
			wantScore: 8400,
			// line 16:   public enum InnerEnum {
			wantBestLineMatch: 16,
		},
		{
			fileName: "example.java",
			content:  exampleJava,
			query:    &query.Substring{Content: true, Pattern: "enum InnerEnum"},
			language: "Java",
			// 5500 (edge Symbol) + 900 (Java enum) + 500 (word)
			wantScore: 6900,
			// line 16:   public enum InnerEnum {
			wantBestLineMatch: 16,
		},
		{
			fileName: "example.java",
			content:  exampleJava,
			query:    &query.Substring{Content: true, Pattern: "public enum InnerEnum {"},
			language: "Java",
			// 4000 (overlap Symbol) + 900 (Java enum) + 500 (word)
			wantScore: 5400,
			// line 16:   public enum InnerEnum {
			wantBestLineMatch: 16,
		},
	}

	for _, c := range cases {
		checkScoring(t, c, false, ctags.UniversalCTags)
	}
}

func TestKotlin(t *testing.T) {
	exampleKotlin, err := os.ReadFile("./testdata/example.kt")
	if err != nil {
		t.Fatal(err)
	}

	cases := []scoreCase{
		{
			fileName: "example.kt",
			content:  exampleKotlin,
			query:    &query.Substring{Content: true, Pattern: "oxyPreloader"},
			language: "Kotlin",
			// 5500 (partial symbol at boundary) + 1000 (Kotlin class) + 50 (partial word)
			wantScore: 6550,
		},
		{
			fileName: "example.kt",
			content:  exampleKotlin,
			query:    &query.Substring{Content: true, Pattern: "ViewMetadata"},
			language: "Kotlin",
			// 7000 (symbol) + 900 (Kotlin interface) + 500 (word)
			wantScore: 8400,
		},
		{
			fileName: "example.kt",
			content:  exampleKotlin,
			query:    &query.Substring{Content: true, Pattern: "onScrolled"},
			language: "Kotlin",
			// 7000 (symbol) + 800 (Kotlin method) + 500 (word)
			wantScore: 8300,
		},
		{
			fileName: "example.kt",
			content:  exampleKotlin,
			query:    &query.Substring{Content: true, Pattern: "PreloadErrorHandler"},
			language: "Kotlin",
			// 7000 (symbol) + 700 (Kotlin typealias) + 500 (word)
			wantScore: 8200,
		},
		{
			fileName: "example.kt",
			content:  exampleKotlin,
			query:    &query.Substring{Content: true, Pattern: "FLING_THRESHOLD_PX"},
			language: "Kotlin",
			// 7000 (symbol) + 600 (Kotlin constant) + 500 (word)
			wantScore: 8100,
		},
		{
			fileName: "example.kt",
			content:  exampleKotlin,
			query:    &query.Substring{Content: true, Pattern: "scrollState"},
			language: "Kotlin",
			// 7000 (symbol) + 500 (Kotlin variable) + 500 (word)
			wantScore: 8000,
		},
	}

	parserType := ctags.UniversalCTags
	for _, c := range cases {
		t.Run(c.language, func(t *testing.T) {
			checkScoring(t, c, false, parserType)
		})
	}
}

func TestCpp(t *testing.T) {
	exampleCpp, err := os.ReadFile("./testdata/example.cc")
	if err != nil {
		t.Fatal(err)
	}

	cases := []scoreCase{
		{
			fileName: "example.cc",
			content:  exampleCpp,
			query:    &query.Substring{Content: true, Pattern: "FooClass"},
			language: "C++",
			// 7000 (Symbol) + 1000 (C++ class) + 500 (full word)
			wantScore: 8500,
		},
		{
			fileName: "example.cc",
			content:  exampleCpp,
			query:    &query.Substring{Content: true, Pattern: "NestedEnum"},
			language: "C++",
			// 7000 (Symbol) + 900 (C++ enum) + 500 (full word)
			wantScore: 8400,
		},
		{
			fileName: "example.cc",
			content:  exampleCpp,
			query:    &query.Substring{Content: true, Pattern: "main"},
			language: "C++",
			// 7000 (Symbol) + 800 (C++ function) + 500 (full word)
			wantScore: 8300,
		},
		{
			fileName: "example.cc",
			content:  exampleCpp,
			query:    &query.Substring{Content: true, Pattern: "FooStruct"},
			language: "C++",
			// 7000 (Symbol) + 700 (C++ struct) + 500 (full word)
			wantScore: 8200,
		},
		{
			fileName: "example.cc",
			content:  exampleCpp,
			query:    &query.Substring{Content: true, Pattern: "TheUnion"},
			language: "C++",
			// 7000 (Symbol) + 600 (C++ union) + 500 (full word)
			wantScore: 8100,
		},
	}

	parserType := ctags.UniversalCTags
	for _, c := range cases {
		t.Run(c.language, func(t *testing.T) {
			checkScoring(t, c, false, parserType)
		})
	}
}

func TestPython(t *testing.T) {
	examplePython, err := os.ReadFile("./testdata/example.py")
	if err != nil {
		t.Fatal(err)
	}

	cases := []scoreCase{
		{
			fileName: "example.py",
			content:  examplePython,
			query:    &query.Substring{Content: true, Pattern: "C1"},
			language: "Python",
			// 7000 (symbol) + 1000 (Python class) + 500 (word)
			wantScore: 8500,
		},
		{
			fileName: "example.py",
			content:  examplePython,
			query:    &query.Substring{Content: true, Pattern: "g"},
			language: "Python",
			// 7000 (symbol) + 800 (Python function) + 500 (word)
			wantScore: 8300,
		},
	}

	for _, parserType := range []ctags.CTagsParserType{ctags.UniversalCTags, ctags.ScipCTags} {
		for _, c := range cases {
			checkScoring(t, c, false, parserType)
		}
	}

	// Only test SCIP, as universal-ctags doesn't correctly recognize this as a method
	scipOnlyCase := scoreCase{
		fileName: "example.py",
		content:  examplePython,
		query:    &query.Substring{Content: true, Pattern: "__init__"},
		language: "Python",
		// 7000 (symbol) + 800 (Python method) + 50 (partial word)
		wantScore: 7850,
	}

	checkScoring(t, scipOnlyCase, false, ctags.ScipCTags)
}

func TestRuby(t *testing.T) {
	exampleRuby, err := os.ReadFile("./testdata/example.rb")
	if err != nil {
		t.Fatal(err)
	}

	cases := []scoreCase{
		{
			fileName: "example.rb",
			content:  exampleRuby,
			query:    &query.Substring{Content: true, Pattern: "Parental"},
			language: "Ruby",
			// 7000 (symbol) + 1000 (Ruby class) + 500 (word)
			wantScore: 8500,
		},
		{
			fileName: "example.rb",
			content:  exampleRuby,
			query:    &query.Substring{Content: true, Pattern: "parental_func"},
			language: "Ruby",
			// 7000 (symbol) + 900 (Ruby method) + 500 (word)
			wantScore: 8400,
		},
		{
			fileName: "example.rb",
			content:  exampleRuby,
			query:    &query.Substring{Content: true, Pattern: "MyModule"},
			language: "Ruby",
			// 7000 (symbol) + 500 (Ruby module) + 500 (word)
			wantScore: 8200,
		},
	}

	for _, parserType := range []ctags.CTagsParserType{ctags.UniversalCTags, ctags.ScipCTags} {
		for _, c := range cases {
			checkScoring(t, c, false, parserType)
		}
	}
}

func TestScala(t *testing.T) {
	exampleScala, err := os.ReadFile("./testdata/example.scala")
	if err != nil {
		t.Fatal(err)
	}

	cases := []scoreCase{
		{
			fileName: "example.scala",
			content:  exampleScala,
			query:    &query.Substring{Content: true, Pattern: "SymbolIndexBucket"},
			language: "Scala",
			// 7000 (symbol) + 1000 (Scala class) + 500 (word)
			wantScore: 8500,
		},
		{
			fileName: "example.scala",
			content:  exampleScala,
			query:    &query.Substring{Content: true, Pattern: "stdLibPatches"},
			language: "Scala",
			// 7000 (symbol) + 800 (Scala object) + 500 (word)
			wantScore: 8300,
		},
		{
			fileName: "example.scala",
			content:  exampleScala,
			query:    &query.Substring{Content: true, Pattern: "close"},
			language: "Scala",
			// 7000 (symbol) + 700 (Scala method) + 500 (word)
			wantScore: 8200,
		},
		{
			fileName: "example.scala",
			content:  exampleScala,
			query:    &query.Substring{Content: true, Pattern: "javaSymbol"},
			language: "Scala",
			// 7000 (symbol) + 500 (Scala method) + 500 (word)
			wantScore: 8000,
		},
	}

	parserType := ctags.UniversalCTags
	for _, c := range cases {
		checkScoring(t, c, false, parserType)
	}
}

func TestGo(t *testing.T) {
	cases := []scoreCase{
		{
			fileName: "src/net/http/client.go",
			content: []byte(`
package http
type aInterface interface {}
`),
			query:    &query.Substring{Content: true, Pattern: "aInterface"},
			language: "Go",
			// 7000 (full base match) + 1000 (Go interface) + 500 (word)
			wantScore: 8500,
		},
		{
			fileName: "src/net/http/client.go",
			content: []byte(`
package http
type aStruct struct {}
`),
			query:    &query.Substring{Content: true, Pattern: "aStruct"},
			language: "Go",
			// 7000 (full base match) + 900 (Go struct) + 500 (word)
			wantScore: 8400,
		},
		{
			fileName: "src/net/http/client.go",
			content: []byte(`
package http
func aFunc() bool {}
`),
			query:    &query.Substring{Content: true, Pattern: "aFunc"},
			language: "Go",
			// 7000 (full base match) + 800 (Go function) + 500 (word)
			wantScore: 8300,
		},
		{
			fileName: "src/net/http/client.go",
			content: []byte(`
package http
func Get() {
	panic("")
}
`),
			query: &query.And{Children: []query.Q{
				&query.Symbol{Expr: &query.Substring{Pattern: "http", Content: true}},
				&query.Symbol{Expr: &query.Substring{Pattern: "Get", Content: true}},
			}},
			language: "Go",
			// 7000 (full base match) + 800 (Go func) + 50 (Exported Go) + 500 (word) + 200 (atom)
			wantScore: 8550,
		},
	}

	for _, parserType := range []ctags.CTagsParserType{ctags.UniversalCTags, ctags.ScipCTags} {
		for _, c := range cases {
			checkScoring(t, c, false, parserType)
		}
	}
}

func skipIfCTagsUnavailable(t *testing.T, parserType ctags.CTagsParserType) {
	// Never skip universal-ctags tests in CI
	if os.Getenv("CI") != "" && parserType == ctags.UniversalCTags {
		return
	}

	switch parserType {
	case ctags.UniversalCTags:
		requireCTags(t)
	case ctags.ScipCTags:
		if checkScipCTags() == "" {
			t.Skip("scip-ctags not available")
		}
	default:
		t.Fatalf("unexpected parser type")
	}
}

func checkScoring(t *testing.T, c scoreCase, useBM25 bool, parserType ctags.CTagsParserType) {
	skipIfCTagsUnavailable(t, parserType)

	name := c.language
	if parserType == ctags.ScipCTags {
		name += "-scip"
	}

	t.Run(name, func(t *testing.T) {
		dir := t.TempDir()

		opts := Options{
			IndexDir: dir,
			RepositoryDescription: zoekt.Repository{
				Name: "repo",
			},
			LanguageMap: ctags.LanguageMap{
				normalizeLanguage(c.language): parserType,
			},
		}

		epsilon := 0.01

		b, err := NewBuilder(opts)
		if err != nil {
			t.Fatalf("NewBuilder: %v", err)
		}
		if err := b.AddFile(c.fileName, c.content); err != nil {
			t.Fatal(err)
		}
		if err := b.Finish(); err != nil {
			t.Fatalf("Finish: %v", err)
		}

		ss, err := shards.NewDirectorySearcher(dir)
		if err != nil {
			t.Fatalf("NewDirectorySearcher(%s): %v", dir, err)
		}
		defer ss.Close()

		srs, err := ss.Search(context.Background(), c.query, &zoekt.SearchOptions{
			UseBM25Scoring: useBM25,
			ChunkMatches:   true,
			DebugScore:     true})
		if err != nil {
			t.Fatal(err)
		}

		if got, want := len(srs.Files), 1; got != want {
			t.Fatalf("file matches: want %d, got %d", want, got)
		}

		if got := withoutTiebreaker(srs.Files[0].Score, useBM25); math.Abs(got-c.wantScore) > epsilon {
			t.Fatalf("score: want %f, got %f\ndebug: %s\ndebugscore: %s", c.wantScore, got, srs.Files[0].Debug, srs.Files[0].ChunkMatches[0].DebugScore)
		}

		if c.wantBestLineMatch != 0 {
			if len(srs.Files[0].ChunkMatches) == 0 {
				t.Fatalf("want BestLineMatch %d, but no chunk matches were returned", c.wantBestLineMatch)
			}
			chunkMatch := srs.Files[0].ChunkMatches[0]
			if chunkMatch.BestLineMatch != c.wantBestLineMatch {
				t.Fatalf("want BestLineMatch %d, got %d", c.wantBestLineMatch, chunkMatch.BestLineMatch)
			}
		}

		if got := srs.Files[0].Language; got != c.language {
			t.Fatalf("want %s, got %s", c.language, got)
		}
	})
}

// helper to remove the tiebreaker from the score for easier comparison
func withoutTiebreaker(fullScore float64, useBM25 bool) float64 {
	if useBM25 {
		return fullScore
	}
	return math.Trunc(fullScore / zoekt.ScoreOffset)
}

func TestRepoRanks(t *testing.T) {
	requireCTags(t)
	dir := t.TempDir()

	opts := Options{
		IndexDir: dir,
		RepositoryDescription: zoekt.Repository{
			Name: "repo",
		},
	}

	searchQuery := &query.Substring{Content: true, Pattern: "Inner"}
	exampleJava, err := os.ReadFile("./testdata/example.java")
	if err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name      string
		repoRank  uint16
		wantScore float64
	}{
		{
			name: "no shard rank",
			// 5500 (partial symbol at boundary) + 1000 (Java class) + 500 (word match) + 10 (file order)
			wantScore: 7000_00000_10.00,
		},
		{
			name:     "medium shard rank",
			repoRank: 30000,
			// 5500 (partial symbol at boundary) + 1000 (Java class) + 500 (word match) + 30000 (repo rank) + 10 (file order)
			wantScore: 7000_30000_10.00,
		},
		{
			name:     "high shard rank",
			repoRank: 60000,
			// 5500 (partial symbol at boundary) + 1000 (Java class) + 500 (word match) + 60000 (repo rank) + 10 (file order)
			wantScore: 7000_60000_10.00,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			opts.RepositoryDescription = zoekt.Repository{
				Name: "repo",
				Rank: c.repoRank,
			}

			b, err := NewBuilder(opts)
			if err != nil {
				t.Fatalf("NewBuilder: %v", err)
			}

			err = b.Add(zoekt.Document{Name: "example.java", Content: exampleJava})
			if err != nil {
				t.Fatal(err)
			}

			if err := b.Finish(); err != nil {
				t.Fatalf("Finish: %v", err)
			}

			ss, err := shards.NewDirectorySearcher(dir)
			if err != nil {
				t.Fatalf("NewDirectorySearcher(%s): %v", dir, err)
			}
			defer ss.Close()

			srs, err := ss.Search(context.Background(), searchQuery, &zoekt.SearchOptions{
				DebugScore: true,
			})
			if err != nil {
				t.Fatal(err)
			}

			if got, want := len(srs.Files), 1; got != want {
				t.Fatalf("file matches: want %d, got %d", want, got)
			}

			if got := srs.Files[0].Score; math.Abs(got-c.wantScore) >= 0.01 {
				t.Fatalf("score: want %f, got %f\ndebug: %s\ndebugscore: %s", c.wantScore, got, srs.Files[0].Debug, srs.Files[0].LineMatches[0].DebugScore)
			}
		})
	}
}
