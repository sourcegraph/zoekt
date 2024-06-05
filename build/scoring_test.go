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
	"github.com/sourcegraph/zoekt/ctags"
	"github.com/sourcegraph/zoekt/query"
	"github.com/sourcegraph/zoekt/shards"
)

type scoreCase struct {
	fileName  string
	content   []byte
	query     query.Q
	language  string
	wantScore float64
}

func TestFileNameMatch(t *testing.T) {
	cases := []scoreCase{
		{
			fileName: "a/b/c/config.go",
			query:    &query.Substring{FileName: true, Pattern: "config"},
			language: "Go",
			// 5500 (partial base at boundary) + 500 (word) + 10 (file order)
			wantScore: 6010,
		},
		{
			fileName: "a/b/c/config.go",
			query:    &query.Substring{FileName: true, Pattern: "config.go"},
			language: "Go",
			// 7000 (full base match) + 500 (word) + 10 (file order)
			wantScore: 7510,
		},
		{
			fileName: "a/config/c/d.go",
			query:    &query.Substring{FileName: true, Pattern: "config"},
			language: "Go",
			// 500 (word) + 10 (file order)
			wantScore: 510,
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
			// bm25-score: 0.57 <- sum-termFrequencyScore: 10.00, length-ratio: 1.00
			wantScore: 0.57,
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
			// bm25-score: 1.75 <- sum-termFrequencyScore: 56.00, length-ratio: 1.00
			wantScore: 1.75,
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
			// 5500 (partial symbol at boundary) + 1000 (Java class) + 50 (partial word) + 10 (file order)
			wantScore: 6560,
		},
		{
			fileName: "example.java",
			content:  exampleJava,
			query:    &query.Substring{Content: true, Pattern: "StaticClass"},
			language: "Java",
			// 5500 (partial symbol at boundary) + 1000 (Java class) + 500 (word) + 10 (file order)
			wantScore: 7010,
		},
		{
			fileName: "example.java",
			content:  exampleJava,
			query:    &query.Substring{Content: true, Pattern: "innerEnum"},
			language: "Java",
			// 7000 (symbol) + 900 (Java enum) + 500 (word) + 10 (file order)
			wantScore: 8410,
		},
		{
			fileName: "example.java",
			content:  exampleJava,
			query:    &query.Substring{Content: true, Pattern: "innerInterface"},
			language: "Java",
			// 7000 (symbol) + 800 (Java interface) + 500 (word) + 10 (file order)
			wantScore: 8310,
		},
		{
			fileName: "example.java",
			content:  exampleJava,
			query:    &query.Substring{Content: true, Pattern: "innerMethod"},
			language: "Java",
			// 7000 (symbol) + 700 (Java method) + 500 (word) + 10 (file order)
			wantScore: 8210,
		},
		{
			fileName: "example.java",
			content:  exampleJava,
			query:    &query.Substring{Content: true, Pattern: "field"},
			language: "Java",
			// 7000 (symbol) + 600 (Java field) + 500 (word) + 10 (file order)
			wantScore: 8110,
		},
		{
			fileName: "example.java",
			content:  exampleJava,
			query:    &query.Substring{Content: true, Pattern: "B"},
			language: "Java",
			// 7000 (symbol) + 500 (Java enum constant) + 500 (word) + 10 (file order)
			wantScore: 8010,
		},
		// 2 Atoms (1x content and 1x filename)
		{
			fileName: "example.java",
			content:  exampleJava,
			query:    &query.Substring{Pattern: "example"}, // matches filename and a Java field
			language: "Java",
			// 5500 (edge symbol) + 600 (Java field) + 500 (word) + 200 (atom) + 10 (file order)
			wantScore: 6810,
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
			// 7000 (symbol) + 700 (Java method) + 500 (word) + 266.67 (atom) + 10 (file order)
			wantScore: 8476.667,
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
			// 7000 (symbol) + 900 (Java enum) + 500 (word) + 300 (atom) + 10 (file order)
			wantScore: 8710,
		},
		{
			fileName: "example.java",
			content:  exampleJava,
			query:    &query.Substring{Content: true, Pattern: "unInnerInterface("},
			language: "Java",
			// 4000 (overlap Symbol) + 700 (Java method) + 50 (partial word) + 10 (file order)
			wantScore: 4760,
		},
		{
			fileName: "example.java",
			content:  exampleJava,
			query:    &query.Substring{Content: true, Pattern: "InnerEnum"},
			language: "Java",
			// 7000 (Symbol) + 900 (Java enum) + 500 (word) + 10 (file order)
			wantScore: 8410,
		},
		{
			fileName: "example.java",
			content:  exampleJava,
			query:    &query.Substring{Content: true, Pattern: "enum InnerEnum"},
			language: "Java",
			// 5500 (edge Symbol) + 900 (Java enum) + 500 (word) + 10 (file order)
			wantScore: 6910,
		},
		{
			fileName: "example.java",
			content:  exampleJava,
			query:    &query.Substring{Content: true, Pattern: "public enum InnerEnum {"},
			language: "Java",
			// 4000 (overlap Symbol) + 900 (Java enum) + 500 (word) + 10 (file order)
			wantScore: 5410,
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
			// 5500 (partial symbol at boundary) + 1000 (Kotlin class) + 50 (partial word) + 10 (file order)
			wantScore: 6560,
		},
		{
			fileName: "example.kt",
			content:  exampleKotlin,
			query:    &query.Substring{Content: true, Pattern: "ViewMetadata"},
			language: "Kotlin",
			// 7000 (symbol) + 900 (Kotlin interface) + 500 (word) + 10 (file order)
			wantScore: 8410,
		},
		{
			fileName: "example.kt",
			content:  exampleKotlin,
			query:    &query.Substring{Content: true, Pattern: "onScrolled"},
			language: "Kotlin",
			// 7000 (symbol) + 800 (Kotlin method) + 500 (word) + 10 (file order)
			wantScore: 8310,
		},
		{
			fileName: "example.kt",
			content:  exampleKotlin,
			query:    &query.Substring{Content: true, Pattern: "PreloadErrorHandler"},
			language: "Kotlin",
			// 7000 (symbol) + 700 (Kotlin typealias) + 500 (word) + 10 (file order)
			wantScore: 8210,
		},
		{
			fileName: "example.kt",
			content:  exampleKotlin,
			query:    &query.Substring{Content: true, Pattern: "FLING_THRESHOLD_PX"},
			language: "Kotlin",
			// 7000 (symbol) + 600 (Kotlin constant) + 500 (word) + 10 (file order)
			wantScore: 8110,
		},
		{
			fileName: "example.kt",
			content:  exampleKotlin,
			query:    &query.Substring{Content: true, Pattern: "scrollState"},
			language: "Kotlin",
			// 7000 (symbol) + 500 (Kotlin variable) + 500 (word) + 10 (file order)
			wantScore: 8010,
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
			// 7000 (Symbol) + 1000 (C++ class) + 500 (full word) + 10 (file order)
			wantScore: 8510,
		},
		{
			fileName: "example.cc",
			content:  exampleCpp,
			query:    &query.Substring{Content: true, Pattern: "NestedEnum"},
			language: "C++",
			// 7000 (Symbol) + 900 (C++ enum) + 500 (full word) + 10 (file order)
			wantScore: 8410,
		},
		{
			fileName: "example.cc",
			content:  exampleCpp,
			query:    &query.Substring{Content: true, Pattern: "main"},
			language: "C++",
			// 7000 (Symbol) + 800 (C++ function) + 500 (full word) + 10 (file order)
			wantScore: 8310,
		},
		{
			fileName: "example.cc",
			content:  exampleCpp,
			query:    &query.Substring{Content: true, Pattern: "FooStruct"},
			language: "C++",
			// 7000 (Symbol) + 700 (C++ struct) + 500 (full word) + 10 (file order)
			wantScore: 8210,
		},
		{
			fileName: "example.cc",
			content:  exampleCpp,
			query:    &query.Substring{Content: true, Pattern: "TheUnion"},
			language: "C++",
			// 7000 (Symbol) + 600 (C++ union) + 500 (full word) + 10 (file order)
			wantScore: 8110,
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
			// 7000 (symbol) + 1000 (Python class) + 500 (word) + 10 (file order)
			wantScore: 8510,
		},
		{
			fileName: "example.py",
			content:  examplePython,
			query:    &query.Substring{Content: true, Pattern: "g"},
			language: "Python",
			// 7000 (symbol) + 800 (Python function) + 500 (word) + 10 (file order)
			wantScore: 8310,
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
		// 7000 (symbol) + 800 (Python method) + 50 (partial word) + 10 (file order)
		wantScore: 7860,
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
			// 7000 (symbol) + 1000 (Ruby class) + 500 (word) + 10 (file order)
			wantScore: 8510,
		},
		{
			fileName: "example.rb",
			content:  exampleRuby,
			query:    &query.Substring{Content: true, Pattern: "parental_func"},
			language: "Ruby",
			// 7000 (symbol) + 900 (Ruby method) + 500 (word) + 10 (file order)
			wantScore: 8410,
		},
		{
			fileName: "example.rb",
			content:  exampleRuby,
			query:    &query.Substring{Content: true, Pattern: "MyModule"},
			language: "Ruby",
			// 7000 (symbol) + 500 (Ruby module) + 500 (word) + 10 (file order)
			wantScore: 8210,
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
			// 7000 (symbol) + 1000 (Scala class) + 500 (word) + 10 (file order)
			wantScore: 8510,
		},
		{
			fileName: "example.scala",
			content:  exampleScala,
			query:    &query.Substring{Content: true, Pattern: "stdLibPatches"},
			language: "Scala",
			// 7000 (symbol) + 800 (Scala object) + 500 (word) + 10 (file order)
			wantScore: 8310,
		},
		{
			fileName: "example.scala",
			content:  exampleScala,
			query:    &query.Substring{Content: true, Pattern: "close"},
			language: "Scala",
			// 7000 (symbol) + 700 (Scala method) + 500 (word) + 10 (file order)
			wantScore: 8210,
		},
		{
			fileName: "example.scala",
			content:  exampleScala,
			query:    &query.Substring{Content: true, Pattern: "javaSymbol"},
			language: "Scala",
			// 7000 (symbol) + 500 (Scala method) + 500 (word) + 10 (file order)
			wantScore: 8010,
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
			// 7000 (full base match) + 1000 (Go interface) + 500 (word) + 10 (file order)
			wantScore: 8510,
		},
		{
			fileName: "src/net/http/client.go",
			content: []byte(`
package http
type aStruct struct {}
`),
			query:    &query.Substring{Content: true, Pattern: "aStruct"},
			language: "Go",
			// 7000 (full base match) + 900 (Go struct) + 500 (word) + 10 (file order)
			wantScore: 8410,
		},
		{
			fileName: "src/net/http/client.go",
			content: []byte(`
package http
func aFunc() bool {}
`),
			query:    &query.Substring{Content: true, Pattern: "aFunc"},
			language: "Go",
			// 7000 (full base match) + 800 (Go function) + 500 (word) + 10 (file order)
			wantScore: 8310,
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
			// 7000 (full base match) + 800 (Go func) + 50 (Exported Go) + 500 (word) + 200 (atom) + 10 (file order)
			wantScore: 8560,
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

		if got := srs.Files[0].Score; math.Abs(got-c.wantScore) > epsilon {
			t.Fatalf("score: want %f, got %f\ndebug: %s\ndebugscore: %s", c.wantScore, got, srs.Files[0].Debug, srs.Files[0].ChunkMatches[0].DebugScore)
		}

		if got := srs.Files[0].Language; got != c.language {
			t.Fatalf("want %s, got %s", c.language, got)
		}
	})
}

func TestDocumentRanks(t *testing.T) {
	requireCTags(t)
	dir := t.TempDir()

	opts := Options{
		IndexDir: dir,
		RepositoryDescription: zoekt.Repository{
			Name: "repo",
		},
		DocumentRanksVersion: "ranking",
	}

	searchQuery := &query.Substring{Content: true, Pattern: "Inner"}
	exampleJava, err := os.ReadFile("./testdata/example.java")
	if err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name                string
		documentRank        float64
		documentRanksWeight float64
		wantScore           float64
	}{
		{
			name: "score with no document ranks",
			// 5500 (partial symbol at boundary) + 1000 (Java class) + 500 (word match) + 10 (file order)
			wantScore: 7010.00,
		},
		{
			name:         "score with document ranks",
			documentRank: 0.8,
			// 5500 (partial symbol at boundary) + 1000 (Java class) + 500 (word match) + 225 (file rank) + 10 (file order)
			wantScore: 7235.00,
		},
		{
			name:                "score with custom document ranks weight",
			documentRank:        0.8,
			documentRanksWeight: 1000.0,
			// 5500 (partial symbol at boundary) + 1000 (Java class) + 500 (word match) + 25.00 (file rank) + 10 (file order)
			wantScore: 7035.00,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			b, err := NewBuilder(opts)
			if err != nil {
				t.Fatalf("NewBuilder: %v", err)
			}

			err = b.Add(zoekt.Document{Name: "example.java", Content: exampleJava, Ranks: []float64{c.documentRank}})
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
				UseDocumentRanks:    true,
				DocumentRanksWeight: c.documentRanksWeight,
				DebugScore:          true,
			})
			if err != nil {
				t.Fatal(err)
			}

			if got, want := len(srs.Files), 1; got != want {
				t.Fatalf("file matches: want %d, got %d", want, got)
			}

			if got := srs.Files[0].Score; got != c.wantScore {
				t.Fatalf("score: want %f, got %f\ndebug: %s\ndebugscore: %s", c.wantScore, got, srs.Files[0].Debug, srs.Files[0].LineMatches[0].DebugScore)
			}
		})
	}
}

func TestRepoRanks(t *testing.T) {
	requireCTags(t)
	dir := t.TempDir()

	opts := Options{
		IndexDir: dir,
		RepositoryDescription: zoekt.Repository{
			Name: "repo",
		},
		DocumentRanksVersion: "ranking",
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
			wantScore: 7010.00,
		},
		{
			name:     "medium shard rank",
			repoRank: 30000,
			// 5500 (partial symbol at boundary) + 1000 (Java class) + 500 (word match) + 10 (file order) + 9.16 (repo rank)
			wantScore: 7019.16,
		},
		{
			name:     "high shard rank",
			repoRank: 60000,
			// 5500 (partial symbol at boundary) + 1000 (Java class) + 500 (word match) + 10 (file order) + 18.31 (repo rank)
			wantScore: 7028.31,
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
				UseDocumentRanks: true,
				DebugScore:       true,
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
