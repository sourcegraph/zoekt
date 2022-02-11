// Copyright 2020 Google Inc. All rights reserved.
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
	"context"
	"hash/fnv"
	"reflect"
	"regexp/syntax"
	"strings"
	"testing"

	"github.com/RoaringBitmap/roaring"
	"github.com/google/go-cmp/cmp"
	"github.com/google/zoekt/query"
	"github.com/grafana/regexp"
)

var opnames = map[syntax.Op]string{
	syntax.OpNoMatch:        "OpNoMatch",
	syntax.OpEmptyMatch:     "OpEmptyMatch",
	syntax.OpLiteral:        "OpLiteral",
	syntax.OpCharClass:      "OpCharClass",
	syntax.OpAnyCharNotNL:   "OpAnyCharNotNL",
	syntax.OpAnyChar:        "OpAnyChar",
	syntax.OpBeginLine:      "OpBeginLine",
	syntax.OpEndLine:        "OpEndLine",
	syntax.OpBeginText:      "OpBeginText",
	syntax.OpEndText:        "OpEndText",
	syntax.OpWordBoundary:   "OpWordBoundary",
	syntax.OpNoWordBoundary: "OpNoWordBoundary",
	syntax.OpCapture:        "OpCapture",
	syntax.OpStar:           "OpStar",
	syntax.OpPlus:           "OpPlus",
	syntax.OpQuest:          "OpQuest",
	syntax.OpRepeat:         "OpRepeat",
	syntax.OpConcat:         "OpConcat",
	syntax.OpAlternate:      "OpAlternate",
}

func printRegexp(t *testing.T, r *syntax.Regexp, lvl int) {
	t.Logf("%s%s ch: %d", strings.Repeat(" ", lvl), opnames[r.Op], len(r.Sub))
	for _, s := range r.Sub {
		printRegexp(t, s, lvl+1)
	}
}

func substrMT(pattern string) matchTree {
	d := &indexData{}
	mt, _ := d.newSubstringMatchTree(&query.Substring{
		Pattern: pattern,
	})
	return mt
}

func TestRegexpParse(t *testing.T) {
	type testcase struct {
		in           string
		query        matchTree
		isEquivalent bool
	}

	cases := []testcase{
		{"(foo|)bar", substrMT("bar"), false},
		{"(foo|)", &bruteForceMatchTree{}, false},
		{"(foo|bar)baz.*bla", &andMatchTree{[]matchTree{
			&orMatchTree{[]matchTree{
				substrMT("foo"),
				substrMT("bar"),
			}},
			substrMT("baz"),
			substrMT("bla"),
		}}, false},
		{
			"^[a-z](People)+barrabas$",
			&andMatchTree{[]matchTree{
				substrMT("People"),
				substrMT("barrabas"),
			}}, false,
		},
		{"foo", substrMT("foo"), true},
		{"^foo", substrMT("foo"), false},
		{"(foo) (bar)", &andMatchTree{[]matchTree{substrMT("foo"), substrMT("bar")}}, false},
		{"(thread|needle|haystack)", &orMatchTree{[]matchTree{
			substrMT("thread"),
			substrMT("needle"),
			substrMT("haystack"),
		}}, true},
		{"(foo)(?-s:.)*?(bar)", &andLineMatchTree{andMatchTree{[]matchTree{
			substrMT("foo"),
			substrMT("bar"),
		}}}, false},
		{"(foo)(?-s:.)*?[[:space:]](?-s:.)*?(bar)", &andMatchTree{[]matchTree{
			substrMT("foo"),
			substrMT("bar"),
		}}, false},
		{"(foo){2,}", substrMT("foo"), false},
		{"(...)(...)", &bruteForceMatchTree{}, false},
	}

	for _, c := range cases {
		r, err := syntax.Parse(c.in, syntax.Perl)
		if err != nil {
			t.Errorf("Parse(%q): %v", c.in, err)
			continue
		}
		d := indexData{}
		q := query.Regexp{
			Regexp: r,
		}
		gotQuery, isEq, _, _ := d.regexpToMatchTreeRecursive(q.Regexp, 3, q.FileName, q.CaseSensitive)
		if !reflect.DeepEqual(c.query, gotQuery) {
			printRegexp(t, r, 0)
			t.Errorf("regexpToQuery(%q): got %v, want %v", c.in, gotQuery, c.query)
		}
		if isEq != c.isEquivalent {
			printRegexp(t, r, 0)
			t.Errorf("regexpToQuery(%q): got %v, want %v", c.in, isEq, c.isEquivalent)
		}
	}
}

func TestSearch_ShardRepoMaxMatchCountOpt(t *testing.T) {
	cs := compoundReposShard(t, "foo", "bar")

	ctx := context.Background()
	q := &query.Const{Value: true}
	opts := &SearchOptions{ShardRepoMaxMatchCount: 1}

	sr, err := cs.Search(ctx, q, opts)
	if err != nil {
		t.Fatal(err)
	}

	t.Run("matches", func(t *testing.T) {
		var filenames []string
		for _, r := range sr.Files {
			filenames = append(filenames, r.FileName)
		}

		got, want := filenames, []string{"foo.txt", "bar.txt"}
		if diff := cmp.Diff(want, got); diff != "" {
			t.Errorf("mismatch (-want, +got): %s", diff)
		}
	})

	t.Run("stats", func(t *testing.T) {
		got, want := sr.Stats, Stats{
			ContentBytesLoaded: 2,
			FileCount:          2,
			FilesConsidered:    2,
			FilesSkipped:       2,
			ShardsScanned:      1,
			MatchCount:         2,
		}
		if diff := cmp.Diff(want, got); diff != "" {
			t.Errorf("mismatch (-want, +got): %s", diff)
		}
	})
}

// compoundReposShard returns a compound shard where each repo has 1 document.
func compoundReposShard(t *testing.T, names ...string) *indexData {
	t.Helper()
	b := newIndexBuilder()
	b.indexFormatVersion = NextIndexFormatVersion
	for _, name := range names {
		if err := b.setRepository(&Repository{ID: hash(name), Name: name}); err != nil {
			t.Fatal(err)
		}
		if err := b.AddFile(name+".txt", []byte(name+" content")); err != nil {
			t.Fatal(err)
		}
		if err := b.AddFile(name+".2.txt", []byte(name+" content 2")); err != nil {
			t.Fatal(err)
		}
	}
	s := searcherForTest(t, b)
	return s.(*indexData)
}

func TestSimplifyRepoSet(t *testing.T) {
	d := compoundReposShard(t, "foo", "bar")
	all := &query.RepoSet{Set: map[string]bool{"foo": true, "bar": true}}
	some := &query.RepoSet{Set: map[string]bool{"foo": true, "banana": true}}
	none := &query.RepoSet{Set: map[string]bool{"banana": true}}

	got := d.simplify(all)
	if d := cmp.Diff(&query.Const{Value: true}, got); d != "" {
		t.Fatalf("-want, +got:\n%s", d)
	}

	got = d.simplify(some)
	if d := cmp.Diff(some, got); d != "" {
		t.Fatalf("-want, +got:\n%s", d)
	}

	got = d.simplify(none)
	if d := cmp.Diff(&query.Const{Value: false}, got); d != "" {
		t.Fatalf("-want, +got:\n%s", d)
	}
}

func TestSimplifyRepo(t *testing.T) {
	re := func(pat string) *query.Repo {
		t.Helper()
		re, err := regexp.Compile(pat)
		if err != nil {
			t.Fatal(err)
		}
		return &query.Repo{
			Regexp: re,
		}
	}
	d := compoundReposShard(t, "foo", "fool")
	cases := []struct {
		name string
		q    query.Q
		want query.Q
	}{{
		name: "all",
		q:    re("f.*"),
		want: &query.Const{Value: true},
	}, {
		name: "some",
		q:    re("foo."),
		want: re("foo."),
	}, {
		name: "none",
		q:    re("banana"),
		want: &query.Const{Value: false},
	}}

	for _, tc := range cases {
		got := d.simplify(tc.q)
		if d := cmp.Diff(tc.want.String(), got.String()); d != "" {
			t.Errorf("%s: -want, +got:\n%s", tc.name, d)
		}
	}
}

func TestSimplifyRepoRegexp(t *testing.T) {
	re := func(pat string) *query.RepoRegexp {
		t.Helper()
		re, err := regexp.Compile(pat)
		if err != nil {
			t.Fatal(err)
		}
		return &query.RepoRegexp{
			Regexp: re,
		}
	}
	d := compoundReposShard(t, "foo", "fool")
	cases := []struct {
		name string
		q    query.Q
		want query.Q
	}{{
		name: "all",
		q:    re("f.*"),
		want: &query.Const{Value: true},
	}, {
		name: "some",
		q:    re("foo."),
		want: re("foo."),
	}, {
		name: "none",
		q:    re("banana"),
		want: &query.Const{Value: false},
	}}

	for _, tc := range cases {
		got := d.simplify(tc.q)
		if d := cmp.Diff(tc.want.String(), got.String()); d != "" {
			t.Errorf("%s: -want, +got:\n%s", tc.name, d)
		}
	}
}

func TestSimplifyRepoBranch(t *testing.T) {
	d := compoundReposShard(t, "foo", "bar")

	some := &query.RepoBranches{Set: map[string][]string{"bar": {"branch1"}}}
	none := &query.Repo{Regexp: regexp.MustCompile("banana")}

	got := d.simplify(some)
	if d := cmp.Diff(some, got); d != "" {
		t.Fatalf("-want, +got:\n%s", d)
	}

	got = d.simplify(none)
	if d := cmp.Diff(&query.Const{Value: false}, got); d != "" {
		t.Fatalf("-want, +got:\n%s", d)
	}
}

func TestSimplifyBranchesRepos(t *testing.T) {
	d := compoundReposShard(t, "foo", "bar")

	some := &query.BranchesRepos{
		List: []query.BranchRepos{
			{Branch: "branch1", Repos: roaring.BitmapOf(hash("bar"))},
		},
	}
	none := &query.Repo{Regexp: regexp.MustCompile("banana")}

	got := d.simplify(some)
	tr := cmp.Transformer("", func(b *roaring.Bitmap) []uint32 { return b.ToArray() })
	if d := cmp.Diff(some, got, tr); d != "" {
		t.Fatalf("-want, +got:\n%s", d)
	}

	got = d.simplify(none)
	if d := cmp.Diff(&query.Const{Value: false}, got); d != "" {
		t.Fatalf("-want, +got:\n%s", d)
	}
}

func TestSimplifyRepoBranchSimple(t *testing.T) {
	d := compoundReposShard(t, "foo")
	q := &query.RepoBranches{Set: map[string][]string{"foo": {"HEAD", "b1"}, "bar": {"HEAD"}}}

	want := &query.Or{[]query.Q{&query.Branch{
		Pattern: "HEAD",
		Exact:   true,
	}, &query.Branch{
		Pattern: "b1",
		Exact:   true,
	}}}

	got := d.simplify(q)
	if d := cmp.Diff(want, got); d != "" {
		t.Fatalf("-want, +got:\n%s", d)
	}
}

func hash(name string) uint32 {
	h := fnv.New32()
	h.Write([]byte(name))
	return h.Sum32()
}
