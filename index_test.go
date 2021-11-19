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

package zoekt

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"reflect"
	"regexp/syntax"
	"strings"
	"testing"

	"github.com/kylelemons/godebug/pretty"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/google/zoekt/query"
)

func clearScores(r *SearchResult) {
	for i := range r.Files {
		r.Files[i].Score = 0.0
		for j := range r.Files[i].LineMatches {
			r.Files[i].LineMatches[j].Score = 0.0
		}
		r.Files[i].Checksum = nil
		r.Files[i].Debug = ""
	}
}

func testIndexBuilder(t *testing.T, repo *Repository, docs ...Document) *IndexBuilder {
	b, err := NewIndexBuilder(repo)
	b.contentBloom.bits = b.contentBloom.bits[:bloomSizeTest]
	b.nameBloom.bits = b.nameBloom.bits[:bloomSizeTest]
	if err != nil {
		t.Fatalf("NewIndexBuilder: %v", err)
	}

	for i, d := range docs {
		if err := b.Add(d); err != nil {
			t.Fatalf("Add %d: %v", i, err)
		}
	}

	return b
}

func TestBoundary(t *testing.T) {
	b := testIndexBuilder(t, nil,
		Document{Name: "f1", Content: []byte("x the")},
		Document{Name: "f1", Content: []byte("reader")})
	res := searchForTest(t, b, &query.Substring{Pattern: "there"})
	if len(res.Files) > 0 {
		t.Fatalf("got %v, want no matches", res.Files)
	}
}

func TestDocSectionInvalid(t *testing.T) {
	b, err := NewIndexBuilder(nil)
	if err != nil {
		t.Fatalf("NewIndexBuilder: %v", err)
	}
	doc := Document{
		Name:    "f1",
		Content: []byte("01234567890123"),
		Symbols: []DocumentSection{{5, 8}, {7, 9}},
	}

	if err := b.Add(doc); err == nil {
		t.Errorf("overlapping doc sections should fail")
	}

	doc = Document{
		Name:    "f1",
		Content: []byte("01234567890123"),
		Symbols: []DocumentSection{{0, 20}},
	}

	if err := b.Add(doc); err == nil {
		t.Errorf("doc sections beyond EOF should fail")
	}
}

func TestBloomSkip(t *testing.T) {
	for _, tc := range []struct {
		skip bool
		want int
	}{
		{false, 1},
		{true, 0},
	} {
		if tc.skip {
			os.Setenv("ZOEKT_DISABLE_BLOOM", "1")
		}
		b := testIndexBuilder(t, nil,
			Document{Name: "f1", Content: []byte("reader derre errea")},
		)
		res := searchForTest(t, b, &query.Substring{Pattern: "derrea"})
		if res.Stats.ShardsSkippedFilter != tc.want {
			t.Errorf("bloom disabled=%v filtered out %v shards, want %v",
				tc.skip, res.Stats.ShardsSkippedFilter, tc.want)
		}
		os.Unsetenv("ZOEKT_DISABLE_BLOOM")
	}
}

func TestBasic(t *testing.T) {
	b := testIndexBuilder(t, nil,
		Document{
			Name:    "f2",
			Content: []byte("to carry water in the no later bla"),
			// ------------- 0123456789012345678901234567890123456789
		})

	res := searchForTest(t, b, &query.Substring{
		Pattern:       "water",
		CaseSensitive: true,
	})
	fmatches := res.Files
	if len(fmatches) != 1 || len(fmatches[0].LineMatches) != 1 {
		t.Fatalf("got %v, want 1 matches", fmatches)
	}

	got := fmt.Sprintf("%s:%d", fmatches[0].FileName, fmatches[0].LineMatches[0].LineFragments[0].Offset)
	want := "f2:9"
	if got != want {
		t.Errorf("1: got %s, want %s", got, want)
	}
}

func TestEmptyIndex(t *testing.T) {
	b := testIndexBuilder(t, nil)
	searcher := searcherForTest(t, b)

	var opts SearchOptions
	if _, err := searcher.Search(context.Background(), &query.Substring{}, &opts); err != nil {
		t.Fatalf("Search: %v", err)
	}

	if _, err := searcher.List(context.Background(), &query.Repo{}, nil); err != nil {
		t.Fatalf("List: %v", err)
	}

	if _, err := searcher.Search(context.Background(), &query.Substring{Pattern: "java", FileName: true}, &opts); err != nil {
		t.Fatalf("Search: %v", err)
	}
}

type memSeeker struct {
	data []byte
}

func (s *memSeeker) Name() string {
	return "memseeker"
}

func (s *memSeeker) Close() {}
func (s *memSeeker) Read(off, sz uint32) ([]byte, error) {
	return s.data[off : off+sz], nil
}

func (s *memSeeker) Size() (uint32, error) {
	return uint32(len(s.data)), nil
}

func TestNewlines(t *testing.T) {
	b := testIndexBuilder(t, nil,
		Document{Name: "filename", Content: []byte("line1\nline2\nbla")})

	sres := searchForTest(t, b, &query.Substring{Pattern: "ne2"})

	matches := sres.Files
	want := []FileMatch{{
		FileName: "filename",
		LineMatches: []LineMatch{
			{
				LineFragments: []LineFragmentMatch{{
					Offset:      8,
					LineOffset:  2,
					MatchLength: 3,
				}},
				Line:       []byte("line2"),
				LineStart:  6,
				LineEnd:    11,
				LineNumber: 2,
			},
		},
	}}

	if !reflect.DeepEqual(matches, want) {
		t.Errorf("got %v, want %v", matches, want)
	}
}

// A result spanning multiple lines should have LineMatches that only cover
// single lines.
func TestQueryNewlines(t *testing.T) {
	text := "line1\nline2\nbla"
	b := testIndexBuilder(t, nil,
		Document{Name: "filename", Content: []byte(text)})
	sres := searchForTest(t, b, &query.Substring{Pattern: "ine2\nbla"})
	matches := sres.Files
	if len(matches) != 1 {
		t.Fatalf("got %d file matches, want exactly one", len(matches))
	}
	m := matches[0]
	if len(m.LineMatches) != 2 {
		t.Fatalf("got %d line matches, want exactly two", len(m.LineMatches))
	}
}

func searchForTest(t *testing.T, b *IndexBuilder, q query.Q, o ...SearchOptions) *SearchResult {
	searcher := searcherForTest(t, b)
	var opts SearchOptions
	if len(o) > 0 {
		opts = o[0]
	}
	res, err := searcher.Search(context.Background(), q, &opts)
	if err != nil {
		t.Fatalf("Search(%s): %v", q, err)
	}
	clearScores(res)
	return res
}

func searcherForTest(t *testing.T, b *IndexBuilder) Searcher {
	var buf bytes.Buffer
	b.Write(&buf)
	f := &memSeeker{buf.Bytes()}

	searcher, err := NewSearcher(f)
	if err != nil {
		t.Fatalf("NewSearcher: %v", err)
	}

	return searcher
}

func TestCaseFold(t *testing.T) {
	b := testIndexBuilder(t, nil,
		Document{Name: "f1", Content: []byte("I love BaNaNAS.")},
		// ---------- 012345678901234567890123456
	)
	sres := searchForTest(t, b, &query.Substring{
		Pattern:       "bananas",
		CaseSensitive: true,
	})
	matches := sres.Files
	if len(matches) != 0 {
		t.Errorf("foldcase: got %#v, want 0 matches", matches)
	}

	sres = searchForTest(t, b,
		&query.Substring{
			Pattern:       "BaNaNAS",
			CaseSensitive: true,
		})
	matches = sres.Files
	if len(matches) != 1 {
		t.Errorf("no foldcase: got %v, want 1 matches", matches)
	} else if matches[0].LineMatches[0].LineFragments[0].Offset != 7 {
		t.Errorf("foldcase: got %v, want offsets 7", matches)
	}
}

func TestAndSearch(t *testing.T) {
	b, err := NewIndexBuilder(nil)
	if err != nil {
		t.Fatalf("NewIndexBuilder: %v", err)
	}

	b.AddFile("f1", []byte("x banana y"))
	b.AddFile("f2", []byte("x apple y"))
	b.AddFile("f3", []byte("x banana apple y"))
	// ---------------------0123456789012345
	sres := searchForTest(t, b, query.NewAnd(
		&query.Substring{
			Pattern: "banana",
		},
		&query.Substring{
			Pattern: "apple",
		},
	))
	matches := sres.Files
	if len(matches) != 1 || len(matches[0].LineMatches) != 1 || len(matches[0].LineMatches[0].LineFragments) != 2 {
		t.Fatalf("got %#v, want 1 match with 2 fragments", matches)
	}

	if matches[0].LineMatches[0].LineFragments[0].Offset != 2 || matches[0].LineMatches[0].LineFragments[1].Offset != 9 {
		t.Fatalf("got %#v, want offsets 2,9", matches)
	}

	wantStats := Stats{
		FilesLoaded:        1,
		ContentBytesLoaded: 17,
		IndexBytesLoaded:   8,
		NgramMatches:       3, // we look at doc 1, because it's max(0,1) due to AND
		MatchCount:         1,
		FileCount:          1,
		FilesConsidered:    2,
		ShardsScanned:      1,
	}
	if diff := pretty.Compare(wantStats, sres.Stats); diff != "" {
		t.Errorf("got stats diff %s", diff)
	}
}

func TestAndNegateSearch(t *testing.T) {
	b, err := NewIndexBuilder(nil)
	if err != nil {
		t.Fatalf("NewIndexBuilder: %v", err)
	}

	b.AddFile("f1", []byte("x banana y"))
	b.AddFile("f4", []byte("x banana apple y"))
	// ---------------------0123456789012345
	sres := searchForTest(t, b, query.NewAnd(
		&query.Substring{
			Pattern: "banana",
		},
		&query.Not{Child: &query.Substring{
			Pattern: "apple",
		}}))

	matches := sres.Files

	if len(matches) != 1 || len(matches[0].LineMatches) != 1 {
		t.Fatalf("got %v, want 1 match", matches)
	}
	if matches[0].FileName != "f1" {
		t.Fatalf("got match %#v, want FileName: f1", matches[0])
	}
	if matches[0].LineMatches[0].LineFragments[0].Offset != 2 {
		t.Fatalf("got %v, want offsets 2,9", matches)
	}
}

func TestNegativeMatchesOnlyShortcut(t *testing.T) {
	b, err := NewIndexBuilder(nil)
	if err != nil {
		t.Fatalf("NewIndexBuilder: %v", err)
	}

	b.AddFile("f1", []byte("x banana y"))
	b.AddFile("f2", []byte("x appelmoes y"))
	b.AddFile("f3", []byte("x appelmoes y"))
	b.AddFile("f3", []byte("x appelmoes y"))

	sres := searchForTest(t, b, query.NewAnd(
		&query.Substring{
			Pattern: "banana",
		},
		&query.Not{Child: &query.Substring{
			Pattern: "appel",
		}}))

	if sres.Stats.FilesConsidered != 1 {
		t.Errorf("got %#v, want FilesConsidered: 1", sres.Stats)
	}
}

func TestFileSearch(t *testing.T) {
	b, err := NewIndexBuilder(nil)
	if err != nil {
		t.Fatalf("NewIndexBuilder: %v", err)
	}

	b.AddFile("banzana", []byte("x orange y"))
	// --------0123456
	b.AddFile("banana", []byte("x apple y"))
	// --------789012
	sres := searchForTest(t, b, &query.Substring{
		Pattern:  "anan",
		FileName: true,
	})

	matches := sres.Files
	if len(matches) != 1 || len(matches[0].LineMatches) != 1 {
		t.Fatalf("got %v, want 1 match", matches)
	}

	got := matches[0].LineMatches[0]
	want := LineMatch{
		Line: []byte("banana"),
		LineFragments: []LineFragmentMatch{{
			Offset:      1,
			LineOffset:  1,
			MatchLength: 4,
		}},
		FileName: true,
	}

	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %#v, want %#v", got, want)
	}
}

func TestFileCase(t *testing.T) {
	b, err := NewIndexBuilder(nil)
	if err != nil {
		t.Fatalf("NewIndexBuilder: %v", err)
	}

	b.AddFile("BANANA", []byte("x orange y"))
	sres := searchForTest(t, b, &query.Substring{
		Pattern:  "banana",
		FileName: true,
	})

	matches := sres.Files
	if len(matches) != 1 || matches[0].FileName != "BANANA" {
		t.Fatalf("got %v, want 1 match 'BANANA'", matches)
	}
}

func TestFileRegexpSearchBruteForce(t *testing.T) {
	b, err := NewIndexBuilder(nil)
	if err != nil {
		t.Fatalf("NewIndexBuilder: %v", err)
	}

	b.AddFile("banzana", []byte("x orange y"))
	// --------------------------0123456879
	b.AddFile("banana", []byte("x apple y"))
	sres := searchForTest(t, b, &query.Regexp{
		Regexp:   mustParseRE("[qn][zx]"),
		FileName: true,
	})

	matches := sres.Files
	if len(matches) != 1 || matches[0].FileName != "banzana" {
		t.Fatalf("got %v, want 1 match on 'banzana'", matches)
	}
}

func TestFileRegexpSearchShortString(t *testing.T) {
	b, err := NewIndexBuilder(nil)
	if err != nil {
		t.Fatalf("NewIndexBuilder: %v", err)
	}

	b.AddFile("banana.py", []byte("x orange y"))
	sres := searchForTest(t, b, &query.Regexp{
		Regexp:   mustParseRE("ana.py"),
		FileName: true,
	})

	matches := sres.Files
	if len(matches) != 1 || matches[0].FileName != "banana.py" {
		t.Fatalf("got %v, want 1 match on 'banana.py'", matches)
	}
}

func TestFileSubstringSearchBruteForce(t *testing.T) {
	b, err := NewIndexBuilder(nil)
	if err != nil {
		t.Fatalf("NewIndexBuilder: %v", err)
	}

	b.AddFile("BANZANA", []byte("x orange y"))
	b.AddFile("banana", []byte("x apple y"))

	q := &query.Substring{
		Pattern:  "z",
		FileName: true,
	}

	res := searchForTest(t, b, q)
	if len(res.Files) != 1 || res.Files[0].FileName != "BANZANA" {
		t.Fatalf("got %v, want 1 match on 'BANZANA''", res.Files)
	}
}

func TestFileSubstringSearchBruteForceEnd(t *testing.T) {
	b, err := NewIndexBuilder(nil)
	if err != nil {
		t.Fatalf("NewIndexBuilder: %v", err)
	}

	b.AddFile("BANZANA", []byte("x orange y"))
	b.AddFile("bananaq", []byte("x apple y"))

	q := &query.Substring{
		Pattern:  "q",
		FileName: true,
	}

	res := searchForTest(t, b, q)
	if want := "bananaq"; len(res.Files) != 1 || res.Files[0].FileName != want {
		t.Fatalf("got %v, want 1 match in %q", res.Files, want)
	}
}

func TestSearchMatchAll(t *testing.T) {
	b, err := NewIndexBuilder(nil)
	if err != nil {
		t.Fatalf("NewIndexBuilder: %v", err)
	}

	b.AddFile("banzana", []byte("x orange y"))
	// --------------------------0123456879
	b.AddFile("banana", []byte("x apple y"))
	sres := searchForTest(t, b, &query.Const{Value: true})

	matches := sres.Files
	if len(matches) != 2 {
		t.Fatalf("got %v, want 2 matches", matches)
	}
}

func TestSearchNewline(t *testing.T) {
	b, err := NewIndexBuilder(nil)
	if err != nil {
		t.Fatalf("NewIndexBuilder: %v", err)
	}

	b.AddFile("banzana", []byte("abcd\ndefg"))
	sres := searchForTest(t, b, &query.Substring{Pattern: "d\nd"})

	// Just check that we don't crash.

	matches := sres.Files
	if len(matches) != 1 {
		t.Fatalf("got %v, want 1 matches", matches)
	}
}

func TestSearchMatchAllRegexp(t *testing.T) {
	b, err := NewIndexBuilder(nil)
	if err != nil {
		t.Fatalf("NewIndexBuilder: %v", err)
	}

	b.AddFile("banzana", []byte("abcd"))
	// --------------------------0123456879
	b.AddFile("banana", []byte("pqrs"))
	sres := searchForTest(t, b, &query.Regexp{Regexp: mustParseRE(".")})

	matches := sres.Files
	if len(matches) != 2 || sres.Stats.MatchCount != 2 {
		t.Fatalf("got %v, want 2 matches", matches)
	}
	if len(matches[0].LineMatches[0].Line) != 4 || len(matches[1].LineMatches[0].Line) != 4 {
		t.Fatalf("want 4 chars in every file, got %#v", matches)
	}
}

func TestFileRestriction(t *testing.T) {
	b, err := NewIndexBuilder(nil)
	if err != nil {
		t.Fatalf("NewIndexBuilder: %v", err)
	}

	b.AddFile("banana1", []byte("x orange y"))
	// --------------------------0123456879
	b.AddFile("banana2", []byte("x apple y"))
	b.AddFile("orange", []byte("x apple y"))
	sres := searchForTest(t, b, query.NewAnd(
		&query.Substring{
			Pattern:  "banana",
			FileName: true,
		},
		&query.Substring{
			Pattern: "apple",
		}))

	matches := sres.Files
	if len(matches) != 1 || len(matches[0].LineMatches) != 1 {
		t.Fatalf("got %v, want 1 match", matches)
	}

	match := matches[0].LineMatches[0]
	got := string(match.Line)
	want := "x apple y"
	if got != want {
		t.Errorf("got match %#v, want line %q", match, want)
	}
}

func TestFileNameBoundary(t *testing.T) {
	b := testIndexBuilder(t, nil,
		Document{Name: "banana2", Content: []byte("x apple y")},
		Document{Name: "helpers.go", Content: []byte("x apple y")},
		Document{Name: "foo", Content: []byte("x apple y")})
	sres := searchForTest(t, b, &query.Substring{
		Pattern:  "helpers.go",
		FileName: true,
	})

	matches := sres.Files
	if len(matches) != 1 || len(matches[0].LineMatches) != 1 {
		t.Fatalf("got %v, want 1 match", matches)
	}
}

func TestDocumentOrder(t *testing.T) {
	var docs []Document
	for i := 0; i < 3; i++ {
		docs = append(docs, Document{Name: fmt.Sprintf("f%d", i), Content: []byte("needle")})
	}

	b := testIndexBuilder(t, nil, docs...)

	sres := searchForTest(t, b, query.NewAnd(
		&query.Substring{
			Pattern: "needle",
		}))

	want := []string{"f0", "f1", "f2"}
	var got []string
	for _, f := range sres.Files {
		got = append(got, f.FileName)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestBranchMask(t *testing.T) {
	b := testIndexBuilder(t, &Repository{
		Branches: []RepositoryBranch{
			{"master", "v-master"},
			{"stable", "v-stable"},
			{"bonzai", "v-bonzai"},
		},
	}, Document{Name: "f1", Content: []byte("needle"), Branches: []string{"master"}},
		Document{Name: "f2", Content: []byte("needle"), Branches: []string{"stable", "master"}},
		Document{Name: "f3", Content: []byte("needle"), Branches: []string{"stable", "master"}},
		Document{Name: "f4", Content: []byte("needle"), Branches: []string{"bonzai"}},
	)

	sres := searchForTest(t, b, query.NewAnd(
		&query.Substring{
			Pattern: "needle",
		},
		&query.Branch{
			Pattern: "table",
		}))

	if len(sres.Files) != 2 || sres.Files[0].FileName != "f2" || sres.Files[1].FileName != "f3" {
		t.Fatalf("got %v, want 2 result from [f2,f3]", sres.Files)
	}

	if len(sres.Files[0].Branches) != 1 || sres.Files[0].Branches[0] != "stable" {
		t.Fatalf("got %v, want 1 branch 'stable'", sres.Files[0].Branches)
	}
}

func TestBranchLimit(t *testing.T) {
	for limit := 64; limit <= 65; limit++ {
		r := &Repository{}
		for i := 0; i < limit; i++ {
			s := fmt.Sprintf("b%d", i)
			r.Branches = append(r.Branches, RepositoryBranch{
				s, "v-" + s,
			})
		}
		_, err := NewIndexBuilder(r)
		if limit == 64 && err != nil {
			t.Fatalf("NewIndexBuilder: %v", err)
		} else if limit == 65 && err == nil {
			t.Fatalf("NewIndexBuilder succeeded")
		}
	}
}

func TestBranchReport(t *testing.T) {
	branches := []string{"stable", "master"}
	b := testIndexBuilder(t, &Repository{
		Branches: []RepositoryBranch{
			{"stable", "vs"},
			{"master", "vm"},
		},
	},
		Document{Name: "f2", Content: []byte("needle"), Branches: branches})
	sres := searchForTest(t, b, &query.Substring{
		Pattern: "needle",
	})
	if len(sres.Files) != 1 {
		t.Fatalf("got %v, want 1 result from f2", sres.Files)
	}

	f := sres.Files[0]
	if !reflect.DeepEqual(f.Branches, branches) {
		t.Fatalf("got branches %q, want %q", f.Branches, branches)
	}
}

func TestBranchVersions(t *testing.T) {
	b := testIndexBuilder(t, &Repository{
		Branches: []RepositoryBranch{
			{"stable", "v-stable"},
			{"master", "v-master"},
		},
	}, Document{Name: "f2", Content: []byte("needle"), Branches: []string{"master"}})

	sres := searchForTest(t, b, &query.Substring{
		Pattern: "needle",
	})
	if len(sres.Files) != 1 {
		t.Fatalf("got %v, want 1 result from f2", sres.Files)
	}

	f := sres.Files[0]
	if f.Version != "v-master" {
		t.Fatalf("got file %#v, want version 'v-master'", f)
	}
}

func mustParseRE(s string) *syntax.Regexp {
	r, err := syntax.Parse(s, 0)
	if err != nil {
		panic(err)
	}

	return r
}

func TestRegexp(t *testing.T) {
	content := []byte("needle the bla")
	b := testIndexBuilder(t, nil,
		Document{
			Name:    "f1",
			Content: content,
		})
	// ------------------------------01234567890123

	sres := searchForTest(t, b,
		&query.Regexp{
			Regexp: mustParseRE("dle.*bla"),
		})

	if len(sres.Files) != 1 || len(sres.Files[0].LineMatches) != 1 {
		t.Fatalf("got %v, want 1 match in 1 file", sres.Files)
	}

	got := sres.Files[0].LineMatches[0]
	want := LineMatch{
		LineFragments: []LineFragmentMatch{{
			LineOffset:  3,
			Offset:      3,
			MatchLength: 11,
		}},
		Line:       content,
		FileName:   false,
		LineNumber: 1,
		LineStart:  0,
		LineEnd:    14,
	}

	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %#v, want %#v", got, want)
	}
}

func TestRegexpFile(t *testing.T) {
	content := []byte("needle the bla")
	// ----------------01234567890123

	name := "let's play: find the mussel"
	b := testIndexBuilder(t, nil,
		Document{Name: name, Content: content},
		Document{Name: "play.txt", Content: content})

	sres := searchForTest(t, b,
		&query.Regexp{
			Regexp:   mustParseRE("play.*mussel"),
			FileName: true,
		})

	if len(sres.Files) != 1 || len(sres.Files[0].LineMatches) != 1 {
		t.Fatalf("got %v, want 1 match in 1 file", sres.Files)
	}

	if sres.Files[0].FileName != name {
		t.Errorf("got match %#v, want name %q", sres.Files[0], name)
	}
}

func TestRegexpOrder(t *testing.T) {
	content := []byte("bla the needle")
	// ----------------01234567890123
	b := testIndexBuilder(t, nil,
		Document{Name: "f1", Content: content})

	sres := searchForTest(t, b,
		&query.Regexp{
			Regexp: mustParseRE("dle.*bla"),
		})

	if len(sres.Files) != 0 {
		t.Fatalf("got %v, want 0 matches", sres.Files)
	}
}

func TestRepoName(t *testing.T) {
	content := []byte("bla the needle")
	// ----------------01234567890123
	b := testIndexBuilder(t, &Repository{Name: "bla"},
		Document{Name: "f1", Content: content})

	sres := searchForTest(t, b,
		query.NewAnd(
			&query.Substring{Pattern: "needle"},
			&query.Repo{Pattern: "foo"},
		))

	if len(sres.Files) != 0 {
		t.Fatalf("got %v, want 0 matches", sres.Files)
	}

	if sres.Stats.FilesConsidered > 0 {
		t.Fatalf("got FilesConsidered %d, should have short circuited", sres.Stats.FilesConsidered)
	}

	sres = searchForTest(t, b,
		query.NewAnd(
			&query.Substring{Pattern: "needle"},
			&query.Repo{Pattern: "bla"},
		))
	if len(sres.Files) != 1 {
		t.Fatalf("got %v, want 1 match", sres.Files)
	}
}

func TestMergeMatches(t *testing.T) {
	content := []byte("blablabla")
	b := testIndexBuilder(t, nil,
		Document{Name: "f1", Content: content})

	sres := searchForTest(t, b,
		&query.Substring{Pattern: "bla"})
	if len(sres.Files) != 1 || len(sres.Files[0].LineMatches) != 1 {
		t.Fatalf("got %v, want 1 match", sres.Files)
	}
}

func TestRepoURL(t *testing.T) {
	content := []byte("blablabla")
	b := testIndexBuilder(t, &Repository{
		Name:                 "name",
		URL:                  "URL",
		CommitURLTemplate:    "commit",
		FileURLTemplate:      "file-url",
		LineFragmentTemplate: "fragment",
	}, Document{Name: "f1", Content: content})

	sres := searchForTest(t, b, &query.Substring{Pattern: "bla"})

	if sres.RepoURLs["name"] != "file-url" {
		t.Errorf("got RepoURLs %v, want {name: URL}", sres.RepoURLs)
	}
	if sres.LineFragments["name"] != "fragment" {
		t.Errorf("got URLs %v, want {name: URL}", sres.LineFragments)
	}
}

func TestRegexpCaseSensitive(t *testing.T) {
	content := []byte("bla\nfunc unmarshalGitiles\n")
	b := testIndexBuilder(t, nil, Document{
		Name:    "f1",
		Content: content,
	})

	res := searchForTest(t, b,
		&query.Regexp{
			Regexp:        mustParseRE("func.*Gitiles"),
			CaseSensitive: true,
		})

	if len(res.Files) != 1 {
		t.Fatalf("got %v, want one match", res.Files)
	}
}

func TestRegexpCaseFolding(t *testing.T) {
	content := []byte("bla\nfunc unmarshalGitiles\n")

	b := testIndexBuilder(t, nil,
		Document{Name: "f1", Content: content})
	res := searchForTest(t, b,
		&query.Regexp{
			Regexp:        mustParseRE("func.*GITILES"),
			CaseSensitive: false,
		})

	if len(res.Files) != 1 {
		t.Fatalf("got %v, want one match", res.Files)
	}
}

func TestCaseRegexp(t *testing.T) {
	content := []byte("BLABLABLA")
	b := testIndexBuilder(t, nil,
		Document{Name: "f1", Content: content})
	res := searchForTest(t, b,
		&query.Regexp{
			Regexp:        mustParseRE("[xb][xl][xa]"),
			CaseSensitive: true,
		})

	if len(res.Files) > 0 {
		t.Fatalf("got %v, want no matches", res.Files)
	}
}

func TestNegativeRegexp(t *testing.T) {
	content := []byte("BLABLABLA needle bla")
	b := testIndexBuilder(t, nil,
		Document{Name: "f1", Content: content})
	res := searchForTest(t, b,
		query.NewAnd(
			&query.Substring{
				Pattern: "needle",
			},
			&query.Not{
				Child: &query.Regexp{
					Regexp: mustParseRE(".cs"),
				},
			}))

	if len(res.Files) != 1 {
		t.Fatalf("got %v, want 1 match", res.Files)
	}
}

func TestSymbolRank(t *testing.T) {
	t.Skip()

	content := []byte("func bla() blubxxxxx")
	// ----------------01234567890123456789
	b := testIndexBuilder(t, nil,
		Document{
			Name:    "f1",
			Content: content,
		}, Document{
			Name:    "f2",
			Content: content,
			Symbols: []DocumentSection{{5, 8}},
		}, Document{
			Name:    "f3",
			Content: content,
		})

	res := searchForTest(t, b,
		&query.Substring{
			CaseSensitive: false,
			Pattern:       "bla",
		})

	if len(res.Files) != 3 {
		t.Fatalf("got %d files, want 3 files. Full data: %v", len(res.Files), res.Files)
	}
	if res.Files[0].FileName != "f2" {
		t.Errorf("got %#v, want 'f2' as top match", res.Files[0])
	}
}

func TestSymbolRankRegexpUTF8(t *testing.T) {
	t.Skip()

	prefix := strings.Repeat(string([]rune{kelvinCodePoint}), 100) + "\n"
	content := []byte(prefix +
		"func bla() blub")
	// ------012345678901234
	b := testIndexBuilder(t, nil,
		Document{
			Name:    "f1",
			Content: content,
		}, Document{
			Name:    "f2",
			Content: content,
			Symbols: []DocumentSection{{uint32(len(prefix) + 5), uint32(len(prefix) + 8)}},
		}, Document{
			Name:    "f3",
			Content: content,
		})

	res := searchForTest(t, b,
		&query.Regexp{
			Regexp: mustParseRE("b.a"),
		})

	if len(res.Files) != 3 {
		t.Fatalf("got %#v, want 3 files", res.Files)
	}
	if res.Files[0].FileName != "f2" {
		t.Errorf("got %#v, want 'f2' as top match", res.Files[0])
	}
}

func TestPartialSymbolRank(t *testing.T) {
	t.Skip()

	content := []byte("func bla() blub")
	// ----------------012345678901234

	b := testIndexBuilder(t, nil,
		Document{
			Name:    "f1",
			Content: content,
			Symbols: []DocumentSection{{4, 9}},
		}, Document{
			Name:    "f2",
			Content: content,
			Symbols: []DocumentSection{{4, 8}},
		}, Document{
			Name:    "f3",
			Content: content,
			Symbols: []DocumentSection{{4, 9}},
		})

	res := searchForTest(t, b,
		&query.Substring{
			Pattern: "bla",
		})

	if len(res.Files) != 3 {
		t.Fatalf("got %#v, want 3 files", res.Files)
	}
	if res.Files[0].FileName != "f2" {
		t.Errorf("got %#v, want 'f2' as top match", res.Files[0])
	}
}

func TestNegativeRepo(t *testing.T) {
	content := []byte("bla the needle")
	// ----------------01234567890123
	b := testIndexBuilder(t, &Repository{
		Name: "bla",
	}, Document{Name: "f1", Content: content})

	sres := searchForTest(t, b,
		query.NewAnd(
			&query.Substring{Pattern: "needle"},
			&query.Not{Child: &query.Repo{Pattern: "bla"}},
		))

	if len(sres.Files) != 0 {
		t.Fatalf("got %v, want 0 matches", sres.Files)
	}
}

func TestListRepos(t *testing.T) {
	content := []byte("bla the needle\n")
	t.Run("default and minimal fallback", func(t *testing.T) {
		// ----------------01234567890123
		repo := &Repository{
			Name:     "reponame",
			Branches: []RepositoryBranch{{Name: "main"}, {Name: "dev"}},
		}
		b := testIndexBuilder(t, repo,
			Document{Name: "f1", Content: content, Branches: []string{"main", "dev"}},
			Document{Name: "f2", Content: content, Branches: []string{"main"}},
			Document{Name: "f2", Content: content, Branches: []string{"dev"}},
			Document{Name: "f3", Content: content, Branches: []string{"dev"}})

		searcher := searcherForTest(t, b)

		for _, opts := range []*ListOptions{
			nil,
			{Minimal: false},
			{Minimal: true},
		} {
			t.Run(fmt.Sprint(opts), func(t *testing.T) {
				q := &query.Repo{Pattern: "epo"}

				res, err := searcher.List(context.Background(), q, opts)
				if err != nil {
					t.Fatalf("List(%v): %v", q, err)
				}

				want := &RepoList{
					Repos: []*RepoListEntry{{
						Repository: *repo,
						Stats: RepoStats{
							Documents:    4,
							ContentBytes: 68,
							Shards:       1,

							NewLinesCount:              4,
							DefaultBranchNewLinesCount: 2,
							OtherBranchesNewLinesCount: 3,
						},
					}},
				}
				ignored := []cmp.Option{
					cmpopts.EquateEmpty(),
					cmpopts.IgnoreFields(RepoListEntry{}, "IndexMetadata"),
					cmpopts.IgnoreFields(RepoStats{}, "IndexBytes"),
					cmpopts.IgnoreFields(Repository{}, "SubRepoMap"),
					cmpopts.IgnoreFields(Repository{}, "priority"),
				}
				if diff := cmp.Diff(want, res, ignored...); diff != "" {
					t.Fatalf("mismatch (-want +got):\n%s", diff)
				}

				q = &query.Repo{Pattern: "bla"}
				res, err = searcher.List(context.Background(), q, nil)
				if err != nil {
					t.Fatalf("List(%v): %v", q, err)
				}
				if len(res.Repos) != 0 || len(res.Minimal) != 0 {
					t.Fatalf("got %v, want 0 matches", res)
				}
			})
		}
	})

	t.Run("minimal", func(t *testing.T) {
		repo := &Repository{
			ID:        1234,
			Name:      "reponame",
			Branches:  []RepositoryBranch{{Name: "main"}, {Name: "dev"}},
			RawConfig: map[string]string{"repoid": "1234"},
		}
		b := testIndexBuilder(t, repo,
			Document{Name: "f1", Content: content, Branches: []string{"main", "dev"}},
			Document{Name: "f2", Content: content, Branches: []string{"main"}},
			Document{Name: "f2", Content: content, Branches: []string{"dev"}},
			Document{Name: "f3", Content: content, Branches: []string{"dev"}})

		searcher := searcherForTest(t, b)

		q := &query.Repo{Pattern: "epo"}
		res, err := searcher.List(context.Background(), q, &ListOptions{Minimal: true})
		if err != nil {
			t.Fatalf("List(%v): %v", q, err)
		}

		want := &RepoList{
			Minimal: map[uint32]*MinimalRepoListEntry{
				repo.ID: {
					HasSymbols: repo.HasSymbols,
					Branches:   repo.Branches,
				},
			},
		}

		if diff := cmp.Diff(want, res); diff != "" {
			t.Fatalf("mismatch (-want +got):\n%s", diff)
		}

		q = &query.Repo{Pattern: "bla"}
		res, err = searcher.List(context.Background(), q, &ListOptions{Minimal: true})
		if err != nil {
			t.Fatalf("List(%v): %v", q, err)
		}
		if len(res.Repos) != 0 || len(res.Minimal) != 0 {
			t.Fatalf("got %v, want 0 matches", res)
		}
	})
}

func TestListReposByContent(t *testing.T) {
	content := []byte("bla the needle")
	// ----------------01234567890123
	b := testIndexBuilder(t, &Repository{
		Name: "reponame",
	},
		Document{Name: "f1", Content: content},
		Document{Name: "f2", Content: content})

	searcher := searcherForTest(t, b)
	q := &query.Substring{Pattern: "needle"}
	res, err := searcher.List(context.Background(), q, nil)
	if err != nil {
		t.Fatalf("List(%v): %v", q, err)
	}
	if len(res.Repos) != 1 || res.Repos[0].Repository.Name != "reponame" {
		t.Fatalf("got %v, want 1 matches", res)
	}
	if got := res.Repos[0].Stats.Shards; got != 1 {
		t.Fatalf("got %d, want 1 shard", got)
	}
	q = &query.Substring{Pattern: "foo"}
	res, err = searcher.List(context.Background(), q, nil)
	if err != nil {
		t.Fatalf("List(%v): %v", q, err)
	}
	if len(res.Repos) != 0 {
		t.Fatalf("got %v, want 0 matches", res)
	}
}

func TestMetadata(t *testing.T) {
	content := []byte("bla the needle")
	// ----------------01234567890123
	b := testIndexBuilder(t, &Repository{
		Name: "reponame",
	}, Document{Name: "f1", Content: content},
		Document{Name: "f2", Content: content})

	var buf bytes.Buffer
	b.Write(&buf)
	f := &memSeeker{buf.Bytes()}

	rd, _, err := ReadMetadata(f)
	if err != nil {
		t.Fatalf("ReadMetadata: %v", err)
	}

	if got, want := rd[0].Name, "reponame"; got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestOr(t *testing.T) {
	b := testIndexBuilder(t, nil,
		Document{Name: "f1", Content: []byte("needle")},
		Document{Name: "f2", Content: []byte("banana")})
	sres := searchForTest(t, b, query.NewOr(
		&query.Substring{Pattern: "needle"},
		&query.Substring{Pattern: "banana"}))

	if len(sres.Files) != 2 {
		t.Fatalf("got %v, want 2 files", sres.Files)
	}
}

func TestImportantCutoff(t *testing.T) {
	t.Skip()

	content := []byte("func bla() blub")
	// ----------------012345678901234
	b := testIndexBuilder(t, nil,
		Document{
			Name:    "f1",
			Content: content,
			Symbols: []DocumentSection{{5, 8}},
		}, Document{
			Name:    "f2",
			Content: content,
		})
	opts := SearchOptions{
		ShardMaxImportantMatch: 1,
	}

	sres := searchForTest(t, b, &query.Substring{Pattern: "bla"}, opts)
	if len(sres.Files) != 1 || sres.Files[0].FileName != "f1" {
		t.Errorf("got %v, wanted 1 match 'f1'", sres.Files)
	}
}

func TestFrequency(t *testing.T) {
	content := []byte("sla _Py_HashDouble(double v sla las las shd dot dot")
	// ----------------012345678901234
	b := testIndexBuilder(t, nil,
		Document{
			Name:    "f1",
			Content: content,
		})

	sres := searchForTest(t, b, &query.Substring{Pattern: "slashdot"})
	if len(sres.Files) != 0 {
		t.Errorf("got %v, wanted 0 matches", sres.Files)
	}
}

func TestMatchNewline(t *testing.T) {
	re, err := syntax.Parse("[^a]a", syntax.ClassNL)
	if err != nil {
		t.Fatalf("syntax.Parse: %v", err)
	}

	content := []byte("pqr\nalex")
	// ----------------0123 4567
	b := testIndexBuilder(t, nil,
		Document{
			Name:    "f1",
			Content: content,
		})

	sres := searchForTest(t, b, &query.Regexp{Regexp: re, CaseSensitive: true})
	if len(sres.Files) != 1 {
		t.Errorf("got %v, wanted 1 matches", sres.Files)
	} else if l := sres.Files[0].LineMatches[0].Line; !bytes.Equal(l, content[len("pqr\n"):]) {
		t.Errorf("got match line %q, want %q", l, content)
	}
}

func TestSubRepo(t *testing.T) {
	subRepos := map[string]*Repository{
		"sub": {
			Name:                 "sub-name",
			LineFragmentTemplate: "sub-line",
		},
	}

	content := []byte("pqr\nalex")
	// ----------------0123 4567

	b := testIndexBuilder(t, &Repository{
		SubRepoMap: subRepos,
	}, Document{
		Name:              "sub/f1",
		Content:           content,
		SubRepositoryPath: "sub",
	})

	sres := searchForTest(t, b, &query.Substring{Pattern: "alex"})
	if len(sres.Files) != 1 {
		t.Fatalf("got %v, wanted 1 matches", sres.Files)
	}

	f := sres.Files[0]
	if f.SubRepositoryPath != "sub" || f.SubRepositoryName != "sub-name" {
		t.Errorf("got %#v, want SubRepository{Path,Name} = {'sub', 'sub-name'}", f)
	}

	if sres.LineFragments["sub-name"] != "sub-line" {
		t.Errorf("got LineFragmentTemplate %v, want {'sub':'sub-line'}", sres.LineFragments)
	}
}

func TestSearchEither(t *testing.T) {
	b := testIndexBuilder(t, nil,
		Document{Name: "f1", Content: []byte("bla needle bla")},
		Document{Name: "needle-file-branch", Content: []byte("bla content")})

	sres := searchForTest(t, b, &query.Substring{Pattern: "needle"})
	if len(sres.Files) != 2 {
		t.Fatalf("got %v, wanted 2 matches", sres.Files)
	}

	sres = searchForTest(t, b, &query.Substring{Pattern: "needle", Content: true})
	if len(sres.Files) != 1 {
		t.Fatalf("got %v, wanted 1 match", sres.Files)
	}

	if got, want := sres.Files[0].FileName, "f1"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestUnicodeExactMatch(t *testing.T) {
	needle := "néédlÉ"
	content := []byte("blá blá " + needle + " blâ")
	// ----------------01234567    8
	b := testIndexBuilder(t, nil,
		Document{Name: "f1", Content: content})

	if res := searchForTest(t, b, &query.Substring{Pattern: needle, CaseSensitive: true}); len(res.Files) != 1 {
		t.Fatalf("case sensitive: got %v, wanted 1 match", res.Files)
	}
}

func TestUnicodeCoverContent(t *testing.T) {
	needle := "néédlÉ"
	content := []byte("blá blá " + needle + " blâ")
	b := testIndexBuilder(t, nil,
		Document{Name: "f1", Content: content})

	if res := searchForTest(t, b, &query.Substring{Pattern: "NÉÉDLÉ", CaseSensitive: true}); len(res.Files) != 0 {
		t.Fatalf("case sensitive: got %v, wanted 0 match", res.Files)
	}

	res := searchForTest(t, b, &query.Substring{Pattern: "NÉÉDLÉ"})
	if len(res.Files) != 1 {
		t.Fatalf("case insensitive: got %v, wanted 1 match", res.Files)
	}

	if got, want := res.Files[0].LineMatches[0].LineFragments[0].Offset, uint32(strings.Index(string(content), needle)); got != want {
		t.Errorf("got %d want %d", got, want)
	}
}

func TestUnicodeNonCoverContent(t *testing.T) {
	needle := "nééáádlÉ"
	//---------01234567
	content := []byte("blá blá " + needle + " blâ")
	// ----------------01234567    8901234   5678
	b := testIndexBuilder(t, nil,
		Document{Name: "f1", Content: content})

	res := searchForTest(t, b, &query.Substring{Pattern: "NÉÉÁÁDLÉ", Content: true})
	if len(res.Files) != 1 {
		t.Fatalf("got %v, wanted 1 match", res.Files)
	}

	if got, want := res.Files[0].LineMatches[0].LineFragments[0].Offset, uint32(strings.Index(string(content), needle)); got != want {
		t.Errorf("got %d want %d", got, want)
	}
}

const kelvinCodePoint = 8490

func TestUnicodeVariableLength(t *testing.T) {
	lower := 'k'
	upper := rune(kelvinCodePoint)

	needle := "nee" + string([]rune{lower}) + "eed"
	corpus := []byte("nee" + string([]rune{upper}) + "eed" +
		" ee" + string([]rune{lower}) + "ee" +
		" ee" + string([]rune{upper}) + "ee")

	b := testIndexBuilder(t, nil,
		Document{Name: "f1", Content: []byte(corpus)})

	res := searchForTest(t, b, &query.Substring{Pattern: needle, Content: true})
	if len(res.Files) != 1 {
		t.Fatalf("got %v, wanted 1 match", res.Files)
	}
}

func TestUnicodeFileStartOffsets(t *testing.T) {
	unicode := "世界"
	wat := "waaaaaat"
	b := testIndexBuilder(t, nil,
		Document{
			Name:    "f1",
			Content: []byte(unicode),
		},
		Document{
			Name:    "f2",
			Content: []byte(wat),
		},
	)
	q := &query.Substring{Pattern: wat, Content: true}
	res := searchForTest(t, b, q)
	if len(res.Files) != 1 {
		t.Fatalf("got %v, wanted 1 match", res.Files)
	}
}

func TestLongFileUTF8(t *testing.T) {
	needle := "neeedle"

	// 6 bytes.
	unicode := "世界"
	content := []byte(strings.Repeat(unicode, 100) + needle)
	b := testIndexBuilder(t, nil,
		Document{
			Name:    "f1",
			Content: []byte(strings.Repeat("a", 50)),
		},
		Document{
			Name:    "f2",
			Content: content,
		})

	q := &query.Substring{Pattern: needle, Content: true}
	res := searchForTest(t, b, q)
	if len(res.Files) != 1 {
		t.Errorf("got %v, want 1 result", res)
	}
}

func TestEstimateDocCount(t *testing.T) {
	content := []byte("bla needle bla")
	b := testIndexBuilder(t, &Repository{Name: "reponame"},
		Document{Name: "f1", Content: content},
		Document{Name: "f2", Content: content},
	)

	if sres := searchForTest(t, b,
		query.NewAnd(
			&query.Substring{Pattern: "needle"},
			&query.Repo{Pattern: "reponame"},
		), SearchOptions{
			EstimateDocCount: true,
		}); sres.Stats.ShardFilesConsidered != 2 {
		t.Errorf("got FilesConsidered = %d, want 2", sres.Stats.FilesConsidered)
	}
	if sres := searchForTest(t, b,
		query.NewAnd(
			&query.Substring{Pattern: "needle"},
			&query.Repo{Pattern: "nomatch"},
		), SearchOptions{
			EstimateDocCount: true,
		}); sres.Stats.ShardFilesConsidered != 0 {
		t.Errorf("got FilesConsidered = %d, want 0", sres.Stats.FilesConsidered)
	}
}

func TestUTF8CorrectCorpus(t *testing.T) {
	needle := "neeedle"

	// 6 bytes.
	unicode := "世界"
	b := testIndexBuilder(t, nil,
		Document{
			Name:    "f1",
			Content: []byte(strings.Repeat(unicode, 100)),
		},
		Document{
			Name:    "xxxxxneeedle",
			Content: []byte("hello"),
		})

	q := &query.Substring{Pattern: needle, FileName: true}
	res := searchForTest(t, b, q)
	if len(res.Files) != 1 {
		t.Errorf("got %v, want 1 result", res)
	}
}

func TestBuilderStats(t *testing.T) {
	b := testIndexBuilder(t, nil,
		Document{
			Name:    "f1",
			Content: []byte(strings.Repeat("abcd", 1024)),
		})
	var buf bytes.Buffer
	b.Write(&buf)

	if got, want := b.ContentSize(), uint32(2+4*1024); got != want {
		t.Errorf("got %d, want %d", got, want)
	}
}

func TestIOStats(t *testing.T) {
	b := testIndexBuilder(t, nil,
		Document{
			Name:    "f1",
			Content: []byte(strings.Repeat("abcd", 1024)),
		})

	q := &query.Substring{Pattern: "abc", CaseSensitive: true, Content: true}
	res := searchForTest(t, b, q)

	// 4096 (content) + 1 (overhead: newlines)
	if got, want := res.Stats.ContentBytesLoaded, int64(4097); got != want {
		t.Errorf("got content I/O %d, want %d", got, want)
	}

	// 1024 entries, each 4 bytes apart. 4 fits into single byte
	// delta encoded.
	if got, want := res.Stats.IndexBytesLoaded, int64(1024); got != want {
		t.Errorf("got index I/O %d, want %d", got, want)
	}
}

func TestStartLineAnchor(t *testing.T) {
	b := testIndexBuilder(t, nil,
		Document{
			Name: "f1",
			Content: []byte(
				`hello
start of middle of line
`),
		})

	q, err := query.Parse("^start")
	if err != nil {
		t.Errorf("parse: %v", err)
	}

	res := searchForTest(t, b, q)
	if len(res.Files) != 1 {
		t.Errorf("got %v, want 1 file", res.Files)
	}

	q, err = query.Parse("^middle")
	if err != nil {
		t.Errorf("parse: %v", err)
	}
	res = searchForTest(t, b, q)
	if len(res.Files) != 0 {
		t.Errorf("got %v, want 0 files", res.Files)
	}
}

func TestAndOrUnicode(t *testing.T) {
	q, err := query.Parse("orange.*apple")
	if err != nil {
		t.Errorf("parse: %v", err)
	}
	finalQ := query.NewAnd(q,
		query.NewOr(query.NewAnd(&query.Repo{Pattern: "name"},
			query.NewOr(&query.Branch{Pattern: "master"}))))

	b := testIndexBuilder(t, &Repository{
		Name:     "name",
		Branches: []RepositoryBranch{{"master", "master-version"}},
	}, Document{
		Name:    "f2",
		Content: []byte("orange\u2318apple"),
		// --------------0123456     78901
		Branches: []string{"master"},
	})

	res := searchForTest(t, b, finalQ)
	if len(res.Files) != 1 {
		t.Errorf("got %v, want 1 result", res.Files)
	}
}

func TestAndShort(t *testing.T) {
	content := []byte("bla needle at orange bla")
	b := testIndexBuilder(t, &Repository{Name: "reponame"},
		Document{Name: "f1", Content: content},
		Document{Name: "f2", Content: []byte("xx at xx")},
		Document{Name: "f3", Content: []byte("yy orange xx")},
	)

	q := query.NewAnd(&query.Substring{Pattern: "at"},
		&query.Substring{Pattern: "orange"})

	res := searchForTest(t, b, q)
	if len(res.Files) != 1 || res.Files[0].FileName != "f1" {
		t.Errorf("got %v, want 1 result", res.Files)
	}
}

func TestNoCollectRegexpSubstring(t *testing.T) {
	content := []byte("bla final bla\nfoo final, foo")
	b := testIndexBuilder(t, &Repository{Name: "reponame"},
		Document{Name: "f1", Content: content},
	)

	q := &query.Regexp{
		Regexp: mustParseRE("final[,.]"),
	}

	res := searchForTest(t, b, q)
	if len(res.Files) != 1 {
		t.Fatalf("got %v, want 1 result", res.Files)
	}
	if f := res.Files[0]; len(f.LineMatches) != 1 {
		t.Fatalf("got line matches %v, want 1 line match", printLineMatches(f.LineMatches))
	}
}

func printLineMatches(ms []LineMatch) string {
	var ss []string
	for _, m := range ms {
		ss = append(ss, fmt.Sprintf("%d:%q %v", m.LineNumber, m.Line, m.LineFragments))
	}

	return strings.Join(ss, ", ")
}

func TestLang(t *testing.T) {
	content := []byte("bla needle bla")
	b := testIndexBuilder(t, &Repository{Name: "reponame"},
		Document{Name: "f1", Content: content},
		Document{Name: "f2", Language: "java", Content: content},
		Document{Name: "f3", Language: "cpp", Content: content},
	)

	q := query.NewAnd(&query.Substring{Pattern: "needle"},
		&query.Language{Language: "cpp"})

	res := searchForTest(t, b, q)
	if len(res.Files) != 1 {
		t.Fatalf("got %v, want 1 result in f3", res.Files)
	}
	f := res.Files[0]
	if f.FileName != "f3" || f.Language != "cpp" {
		t.Fatalf("got %v, want 1 match with language cpp", f)
	}
}

func TestLangShortcut(t *testing.T) {
	content := []byte("bla needle bla")
	b := testIndexBuilder(t, &Repository{Name: "reponame"},
		Document{Name: "f2", Language: "java", Content: content},
		Document{Name: "f3", Language: "cpp", Content: content},
	)

	q := query.NewAnd(&query.Substring{Pattern: "needle"},
		&query.Language{Language: "fortran"})

	res := searchForTest(t, b, q)
	if len(res.Files) != 0 {
		t.Fatalf("got %v, want 0 results", res.Files)
	}
	if res.Stats.IndexBytesLoaded > 0 {
		t.Errorf("got IndexBytesLoaded %d, want 0", res.Stats.IndexBytesLoaded)
	}
}

func TestNoTextMatchAtoms(t *testing.T) {
	content := []byte("bla needle bla")
	b := testIndexBuilder(t, &Repository{Name: "reponame"},
		Document{Name: "f1", Content: content},
		Document{Name: "f2", Language: "java", Content: content},
		Document{Name: "f3", Language: "cpp", Content: content},
	)
	q := query.NewAnd(&query.Language{Language: "java"})
	res := searchForTest(t, b, q)
	if len(res.Files) != 1 {
		t.Fatalf("got %v, want 1 result in f3", res.Files)
	}
}

func TestNoPositiveAtoms(t *testing.T) {
	content := []byte("bla needle bla")
	b := testIndexBuilder(t, &Repository{Name: "reponame"},
		Document{Name: "f1", Content: content},
		Document{Name: "f2", Content: content},
	)

	q := query.NewAnd(
		&query.Not{Child: &query.Substring{Pattern: "xyz"}},
		&query.Repo{Pattern: "reponame"})
	res := searchForTest(t, b, q)
	if len(res.Files) != 2 {
		t.Fatalf("got %v, want 2 results in f3", res.Files)
	}
}

func TestSymbolBoundaryStart(t *testing.T) {
	content := []byte("start\nbla bla\nend")
	// ----------------012345 67890123 456

	b := testIndexBuilder(t, &Repository{Name: "reponame"},
		Document{
			Name:    "f1",
			Content: content,
			Symbols: []DocumentSection{{0, 5}, {14, 17}},
		},
	)
	q := &query.Symbol{
		Expr: &query.Substring{Pattern: "start"},
	}
	res := searchForTest(t, b, q)
	if len(res.Files) != 1 || len(res.Files[0].LineMatches) != 1 {
		t.Fatalf("got %v, want 1 line in 1 file", res.Files)
	}
	m := res.Files[0].LineMatches[0].LineFragments[0]
	if m.Offset != 0 {
		t.Fatalf("got offset %d want 0", m.Offset)
	}
}

func TestSymbolBoundaryEnd(t *testing.T) {
	content := []byte("start\nbla bla\nend")
	// ----------------012345 67890123 456

	b := testIndexBuilder(t, &Repository{Name: "reponame"},
		Document{
			Name:    "f1",
			Content: content,
			Symbols: []DocumentSection{{14, 17}},
		},
	)
	q := &query.Symbol{
		Expr: &query.Substring{Pattern: "end"},
	}
	res := searchForTest(t, b, q)
	if len(res.Files) != 1 || len(res.Files[0].LineMatches) != 1 {
		t.Fatalf("got %v, want 1 line in 1 file", res.Files)
	}
	m := res.Files[0].LineMatches[0].LineFragments[0]
	if m.Offset != 14 {
		t.Fatalf("got offset %d want 0", m.Offset)
	}
}

func TestSymbolSubstring(t *testing.T) {
	content := []byte("bla\nsymblabla\nbla")
	// ----------------0123 456789012

	b := testIndexBuilder(t, &Repository{Name: "reponame"},
		Document{
			Name:    "f1",
			Content: content,
			Symbols: []DocumentSection{{4, 12}},
		},
	)
	q := &query.Symbol{
		Expr: &query.Substring{Pattern: "bla"},
	}
	res := searchForTest(t, b, q)
	if len(res.Files) != 1 || len(res.Files[0].LineMatches) != 1 {
		t.Fatalf("got %v, want 1 line in 1 file", res.Files)
	}
	m := res.Files[0].LineMatches[0].LineFragments[0]
	if m.Offset != 7 || m.MatchLength != 3 {
		t.Fatalf("got offset %d, size %d want 7 size 3", m.Offset, m.MatchLength)
	}
}

func TestSymbolSubstringExact(t *testing.T) {
	content := []byte("bla\nsym\nbla\nsym\nasymb")
	// ----------------0123 4567 89012

	b := testIndexBuilder(t, &Repository{Name: "reponame"},
		Document{
			Name:    "f1",
			Content: content,
			Symbols: []DocumentSection{{4, 7}},
		},
	)
	q := &query.Symbol{
		Expr: &query.Substring{Pattern: "sym"},
	}
	res := searchForTest(t, b, q)
	if len(res.Files) != 1 || len(res.Files[0].LineMatches) != 1 {
		t.Fatalf("got %v, want 1 line in 1 file", res.Files)
	}
	m := res.Files[0].LineMatches[0].LineFragments[0]
	if m.Offset != 4 {
		t.Fatalf("got offset %d, want 7", m.Offset)
	}
}

func TestSymbolRegexpExact(t *testing.T) {
	content := []byte("blah\nbla\nbl")
	// ----------------01234 5678 90

	b := testIndexBuilder(t, &Repository{Name: "reponame"},
		Document{
			Name:    "f1",
			Content: content,
			Symbols: []DocumentSection{{0, 4}, {5, 8}, {9, 11}},
		},
	)
	q := &query.Symbol{
		Expr: &query.Regexp{Regexp: mustParseRE("^bla$")},
	}
	res := searchForTest(t, b, q)
	if len(res.Files) != 1 || len(res.Files[0].LineMatches) != 1 {
		t.Fatalf("got %v, want 1 line in 1 file", res.Files)
	}
	m := res.Files[0].LineMatches[0].LineFragments[0]
	if m.Offset != 5 {
		t.Fatalf("got offset %d, want 5", m.Offset)
	}
}

func TestSymbolRegexpPartial(t *testing.T) {
	content := []byte("abcdef")
	// ----------------012345

	b := testIndexBuilder(t, &Repository{Name: "reponame"},
		Document{
			Name:    "f1",
			Content: content,
			Symbols: []DocumentSection{{0, 6}},
		},
	)
	q := &query.Symbol{
		Expr: &query.Regexp{Regexp: mustParseRE("(b|d)c(d|b)")},
	}
	res := searchForTest(t, b, q)
	if len(res.Files) != 1 || len(res.Files[0].LineMatches) != 1 {
		t.Fatalf("got %v, want 1 line in 1 file", res.Files)
	}
	m := res.Files[0].LineMatches[0].LineFragments[0]
	if m.Offset != 1 {
		t.Fatalf("got offset %d, want 1", m.Offset)
	}
	if m.MatchLength != 3 {
		t.Fatalf("got match length %d, want 3", m.MatchLength)
	}
}

func TestSymbolRegexpAll(t *testing.T) {
	docs := []Document{
		Document{
			Name:    "f1",
			Content: []byte("Hello Zoekt"),
			Symbols: []DocumentSection{{0, 5}, {6, 11}},
		},
		Document{
			Name:    "f2",
			Content: []byte("Second Zoekt Third"),
			Symbols: []DocumentSection{{0, 6}, {7, 12}, {13, 18}},
		},
	}

	b := testIndexBuilder(t, &Repository{Name: "reponame"}, docs...)
	q := &query.Symbol{
		Expr: &query.Regexp{Regexp: mustParseRE(".*")},
	}
	res := searchForTest(t, b, q)
	if len(res.Files) != len(docs) {
		t.Fatalf("got %v, want %d file", res.Files, len(docs))
	}
	for i, want := range docs {
		got := res.Files[i].LineMatches[0].LineFragments
		if len(got) != len(want.Symbols) {
			t.Fatalf("got %d symbols, want %d symbols in doc %s", len(got), len(want.Symbols), want.Name)
		}

		for j, sec := range want.Symbols {
			if sec.Start != got[j].Offset {
				t.Fatalf("got offset %d, want %d in doc %s", got[j].Offset, sec.Start, want.Name)
			}
		}
	}
}

func TestHitIterTerminate(t *testing.T) {
	// contrived input: trigram frequencies forces selecting abc +
	// def for the distance iteration. There is no match, so this
	// will advance the compressedPostingIterator to beyond the
	// end.
	content := []byte("abc bcdbcd cdecde abcabc def efg")
	b := testIndexBuilder(t, nil,
		Document{
			Name:    "f1",
			Content: content,
		},
	)
	searchForTest(t, b, &query.Substring{Pattern: "abcdef"})
}

func TestDistanceHitIterBailLast(t *testing.T) {
	content := []byte("AST AST AST UASH")
	b := testIndexBuilder(t, nil,
		Document{
			Name:    "f1",
			Content: content,
		},
	)
	res := searchForTest(t, b, &query.Substring{Pattern: "UAST"})
	if len(res.Files) != 0 {
		t.Fatalf("got %v, want no results", res.Files)
	}
}

func TestDocumentSectionRuneBoundary(t *testing.T) {
	content := string([]rune{kelvinCodePoint, kelvinCodePoint, kelvinCodePoint})
	b, err := NewIndexBuilder(nil)
	if err != nil {
		t.Fatalf("NewIndexBuilder: %v", err)
	}

	for i, sec := range []DocumentSection{
		{2, 6},
		{3, 7},
	} {
		if err := b.Add(Document{
			Name:    "f1",
			Content: []byte(content),
			Symbols: []DocumentSection{sec},
		}); err == nil {
			t.Errorf("%d: Add succeeded", i)
		}
	}
}

func TestUnicodeQuery(t *testing.T) {
	content := string([]rune{kelvinCodePoint, kelvinCodePoint, kelvinCodePoint})
	b := testIndexBuilder(t, nil,
		Document{
			Name:    "f1",
			Content: []byte(content),
		},
	)

	q := &query.Substring{Pattern: content}
	res := searchForTest(t, b, q)
	if len(res.Files) != 1 {
		t.Fatalf("want 1 match, got %v", res.Files)
	}

	f := res.Files[0]
	if len(f.LineMatches) != 1 {
		t.Fatalf("want 1 line, got %v", f.LineMatches)
	}
	l := f.LineMatches[0]

	if len(l.LineFragments) != 1 {
		t.Fatalf("want 1 line fragment, got %v", l.LineFragments)
	}
	fr := l.LineFragments[0]
	if fr.MatchLength != len(content) {
		t.Fatalf("got MatchLength %d want %d", fr.MatchLength, len(content))
	}
}

func TestSkipInvalidContent(t *testing.T) {
	for _, content := range []string{
		// Binary
		"abc def \x00 abc",
	} {

		b, err := NewIndexBuilder(nil)
		if err != nil {
			t.Fatalf("NewIndexBuilder: %v", err)
		}

		if err := b.Add(Document{
			Name:    "f1",
			Content: []byte(content),
		}); err != nil {
			t.Fatal(err)
		}

		q := &query.Substring{Pattern: "abc def"}
		res := searchForTest(t, b, q)
		if len(res.Files) != 0 {
			t.Fatalf("got %v, want no results", res.Files)
		}

		q = &query.Substring{Pattern: "NOT-INDEXED"}
		res = searchForTest(t, b, q)
		if len(res.Files) != 1 {
			t.Fatalf("got %v, want 1 result", res.Files)
		}
	}
}

func TestCheckText(t *testing.T) {
	for _, text := range []string{"", "simple ascii", "símplé unicödé", "\uFEFFwith utf8 'bom'", "with \uFFFD unicode replacement char"} {
		if err := CheckText([]byte(text), 20000); err != nil {
			t.Errorf("CheckText(%q): %v", text, err)
		}
	}
	for _, text := range []string{"zero\x00byte", "xx", "0123456789abcdefghi"} {
		if err := CheckText([]byte(text), 15); err == nil {
			t.Errorf("CheckText(%q) succeeded", text)
		}
	}
}

func TestLineAnd(t *testing.T) {
	b := testIndexBuilder(t, &Repository{Name: "reponame"},
		Document{Name: "f1", Content: []byte("apple\nbanana\napple banana chocolate apple pudding banana\ngrape")},
		Document{Name: "f2", Content: []byte("apple orange\nbanana")},
		Document{Name: "f3", Content: []byte("banana grape")},
	)
	pattern := "(apple)(?-s:.)*?(banana)"
	r, _ := syntax.Parse(pattern, syntax.Perl)

	q := query.Regexp{
		Regexp:  r,
		Content: true,
	}
	res := searchForTest(t, b, &q)
	wantRegexpCount := 1
	if gotRegexpCount := res.RegexpsConsidered; gotRegexpCount != wantRegexpCount {
		t.Errorf("got %d, wanted %d", gotRegexpCount, wantRegexpCount)
	}
	if len(res.Files) != 1 || res.Files[0].FileName != "f1" {
		t.Errorf("got %v, want 1 result", res.Files)
	}
}

func TestLineAndFileName(t *testing.T) {
	b := testIndexBuilder(t, &Repository{Name: "reponame"},
		Document{Name: "f1", Content: []byte("apple banana\ngrape")},
		Document{Name: "f2", Content: []byte("apple banana\norange")},
		Document{Name: "apple banana", Content: []byte("banana grape")},
	)
	pattern := "(apple)(?-s:.)*?(banana)"
	r, _ := syntax.Parse(pattern, syntax.Perl)

	q := query.Regexp{
		Regexp:   r,
		FileName: true,
	}
	res := searchForTest(t, b, &q)
	wantRegexpCount := 1
	if gotRegexpCount := res.RegexpsConsidered; gotRegexpCount != wantRegexpCount {
		t.Errorf("got %d, wanted %d", gotRegexpCount, wantRegexpCount)
	}
	if len(res.Files) != 1 || res.Files[0].FileName != "apple banana" {
		t.Errorf("got %v, want 1 result", res.Files)
	}
}

func TestMultiLineRegex(t *testing.T) {
	b := testIndexBuilder(t, &Repository{Name: "reponame"},
		Document{Name: "f1", Content: []byte("apple banana\ngrape")},
		Document{Name: "f2", Content: []byte("apple orange")},
		Document{Name: "f3", Content: []byte("grape apple")},
	)
	pattern := "(apple).*?[[:space:]].*?(grape)"
	r, _ := syntax.Parse(pattern, syntax.Perl)

	q := query.Regexp{
		Regexp: r,
	}
	res := searchForTest(t, b, &q)
	wantRegexpCount := 2
	if gotRegexpCount := res.RegexpsConsidered; gotRegexpCount != wantRegexpCount {
		t.Errorf("got %d, wanted %d", gotRegexpCount, wantRegexpCount)
	}
	if len(res.Files) != 1 || res.Files[0].FileName != "f1" {
		t.Errorf("got %v, want 1 result", res.Files)
	}
}

func TestSearchTypeFileName(t *testing.T) {
	b := testIndexBuilder(t, &Repository{
		Name: "reponame",
	},
		Document{Name: "f1", Content: []byte("bla the needle")},
		Document{Name: "f2", Content: []byte("another file another\nneedle")})

	wantSingleMatch := func(res *SearchResult, want string) {
		t.Helper()
		fmatches := res.Files
		if len(fmatches) != 1 {
			t.Errorf("got %v, want 1 matches", len(fmatches))
			return
		}
		if len(fmatches[0].LineMatches) != 1 {
			t.Errorf("got %d line matches", len(fmatches[0].LineMatches))
			return
		}
		var got string
		if fmatches[0].LineMatches[0].FileName {
			got = fmatches[0].FileName
		} else {
			got = fmt.Sprintf("%s:%d", fmatches[0].FileName, fmatches[0].LineMatches[0].LineFragments[0].Offset)
		}

		if got != want {
			t.Errorf("got %s, want %s", got, want)
		}
	}

	// Only return the later match in the second file
	res := searchForTest(t, b, query.NewAnd(
		&query.Type{
			Type:  query.TypeFileName,
			Child: &query.Substring{Pattern: "needle"},
		},
		&query.Substring{Pattern: "file"}))
	wantSingleMatch(res, "f2:8")

	// Only return a filename result
	res = searchForTest(t, b,
		&query.Type{
			Type:  query.TypeFileName,
			Child: &query.Substring{Pattern: "file"},
		})
	wantSingleMatch(res, "f2")
}

func TestSearchTypeLanguage(t *testing.T) {
	b := testIndexBuilder(t, &Repository{
		Name: "reponame",
	},
		Document{Name: "apex.cls", Content: []byte("public class Car extends Vehicle {")},
		Document{Name: "tex.cls", Content: []byte(`\DeclareOption*{`)})

	t.Log(b.languageMap)

	wantSingleMatch := func(res *SearchResult, want string) {
		t.Helper()
		fmatches := res.Files
		if len(fmatches) != 1 {
			t.Errorf("got %v, want 1 matches", len(fmatches))
			return
		}
		if len(fmatches[0].LineMatches) != 1 {
			t.Errorf("got %d line matches", len(fmatches[0].LineMatches))
			return
		}
		var got string
		if fmatches[0].LineMatches[0].FileName {
			got = fmatches[0].FileName
		} else {
			got = fmt.Sprintf("%s:%d", fmatches[0].FileName, fmatches[0].LineMatches[0].LineFragments[0].Offset)
		}

		if got != want {
			t.Errorf("got %s, want %s", got, want)
		}
	}

	res := searchForTest(t, b, &query.Language{Language: "Apex"})
	wantSingleMatch(res, "apex.cls")

	res = searchForTest(t, b, &query.Language{Language: "TeX"})
	wantSingleMatch(res, "tex.cls")
}
