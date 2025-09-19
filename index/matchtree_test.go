// Copyright 2018 Google Inc. All rights reserved.
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

package index

import (
	"math"
	"reflect"
	"regexp/syntax"
	"testing"

	"github.com/RoaringBitmap/roaring"
	"github.com/grafana/regexp"

	"github.com/sourcegraph/zoekt"
	"github.com/sourcegraph/zoekt/query"
)

func Test_breakOnNewlines(t *testing.T) {
	type args struct {
		cm   *candidateMatch
		text []byte
	}
	tests := []struct {
		name string
		args args
		want []*candidateMatch
	}{
		{
			name: "trivial case",
			args: args{
				cm: &candidateMatch{
					byteOffset:  0,
					byteMatchSz: 0,
				},
				text: nil,
			},
			want: nil,
		},
		{
			name: "no newlines",
			args: args{
				cm: &candidateMatch{
					byteOffset:  0,
					byteMatchSz: 1,
				},
				text: []byte("a"),
			},
			want: []*candidateMatch{
				{
					byteOffset:  0,
					byteMatchSz: 1,
				},
			},
		},
		{
			name: "newline at start",
			args: args{
				cm: &candidateMatch{
					byteOffset:  0,
					byteMatchSz: 2,
				},
				text: []byte("\na"),
			},
			want: []*candidateMatch{
				{
					byteOffset:  1,
					byteMatchSz: 1,
				},
			},
		},
		{
			name: "newline at end",
			args: args{
				cm: &candidateMatch{
					byteOffset:  0,
					byteMatchSz: 2,
				},
				text: []byte("a\n"),
			},
			want: []*candidateMatch{
				{
					byteOffset:  0,
					byteMatchSz: 1,
				},
			},
		},
		{
			name: "newline in middle",
			args: args{
				cm: &candidateMatch{
					byteOffset:  0,
					byteMatchSz: 3,
				},
				text: []byte("a\nb"),
			},
			want: []*candidateMatch{
				{
					byteOffset:  0,
					byteMatchSz: 1,
				},
				{
					byteOffset:  2,
					byteMatchSz: 1,
				},
			},
		},
		{
			name: "two newlines",
			args: args{
				cm: &candidateMatch{
					byteOffset:  0,
					byteMatchSz: 5,
				},
				text: []byte("a\nb\nc"),
			},
			want: []*candidateMatch{
				{
					byteOffset:  0,
					byteMatchSz: 1,
				},
				{
					byteOffset:  2,
					byteMatchSz: 1,
				},
				{
					byteOffset:  4,
					byteMatchSz: 1,
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := breakOnNewlines(tt.args.cm, tt.args.text); !reflect.DeepEqual(got, tt.want) {
				type PrintableCm struct {
					byteOffset  uint32
					byteMatchSz uint32
				}
				var got2, want2 []PrintableCm
				for _, g := range got {
					got2 = append(got2, PrintableCm{byteOffset: g.byteOffset, byteMatchSz: g.byteMatchSz})
				}
				for _, w := range tt.want {
					want2 = append(want2, PrintableCm{byteOffset: w.byteOffset, byteMatchSz: w.byteMatchSz})
				}
				t.Errorf("breakMatchOnNewlines() = %+v, want %+v", got2, want2)
			}
		})
	}
}

func TestEquivalentQuerySkipRegexpTree(t *testing.T) {
	tests := []struct {
		query string
		skip  bool
	}{
		{query: "^foo", skip: false},
		{query: "foo", skip: true},
		{query: "thread|needle|haystack", skip: true},
		{query: "contain(er|ing)", skip: false},
		{query: "thread (needle|haystack)", skip: true},
		{query: "thread (needle|)", skip: false},
		{query: `\bthread\b case:yes`, skip: true}, // word search
		{query: `\bthread\b case:no`, skip: false},
	}

	for _, tt := range tests {
		q, err := query.Parse(tt.query)
		if err != nil {
			t.Errorf("Error parsing query: %s", "sym:"+tt.query)
			continue
		}

		d := &indexData{}
		mt, err := d.newMatchTree(q, matchTreeOpt{})
		if err != nil {
			t.Errorf("Error creating match tree from query: %s", q)
			continue
		}

		visitMatchTree(mt, func(m matchTree) {
			if _, ok := m.(*regexpMatchTree); ok && tt.skip {
				t.Log(mt)
				t.Errorf("Expected regexpMatchTree to be skipped for query: %s", q)
			}
		})
	}
}

// Test whether we skip the regexp engine for queries like "\bLITERAL\b
// case:yes"
func TestWordSearchSkipRegexpTree(t *testing.T) {
	qStr := "\\bfoo\\b case:yes"
	q, err := query.Parse(qStr)
	if err != nil {
		t.Fatalf("Error parsing query: %s", "sym:"+qStr)
	}

	d := &indexData{}
	mt, err := d.newMatchTree(q, matchTreeOpt{})
	if err != nil {
		t.Fatalf("Error creating match tree from query: %s", q)
	}

	countRegexMatchTree, countWordMatchTree := 0, 0
	visitMatchTree(mt, func(m matchTree) {
		switch m.(type) {
		case *regexpMatchTree:
			countRegexMatchTree++
		case *wordMatchTree:
			countWordMatchTree++
		}
	})

	if countRegexMatchTree != 0 {
		t.Fatalf("expected to find 0 regexMatchTree, found %d", countRegexMatchTree)
	}

	if countWordMatchTree != 1 {
		t.Fatalf("expected to find 1 wordMatchTree, found %d", countWordMatchTree)
	}
}

func TestSymbolMatchTree(t *testing.T) {
	tests := []struct {
		query    string
		substr   string
		regex    string
		regexAll bool
	}{
		{query: "sym:.*", regex: "(?i)(?-s:.)*", regexAll: true},
		{query: "sym:(ab|cd)", regex: "(?i)ab|cd"},
		{query: "sym:b.r", regex: "(?i)b(?-s:.)r"},
		{query: "sym:horse", substr: "horse"},
		{query: `sym:\bthread\b case:yes`, regex: `\bthread\b`}, // check we disable word search opt
		{query: `sym:\bthread\b case:no`, regex: `(?i)\bthread\b`},
	}

	for _, tt := range tests {
		q, err := query.Parse(tt.query)
		if err != nil {
			t.Errorf("Error parsing query: %s", tt.query)
			continue
		}

		d := &indexData{}
		mt, err := d.newMatchTree(q, matchTreeOpt{})
		if err != nil {
			t.Errorf("Error creating match tree from query: %s", q)
			continue
		}

		var (
			substr   string
			regex    string
			regexAll bool
		)
		if substrMT, ok := mt.(*symbolSubstrMatchTree); ok {
			substr = substrMT.query.Pattern
		}
		if regexMT, ok := mt.(*symbolRegexpMatchTree); ok {
			regex = regexMT.regexp.String()
			regexAll = regexMT.all
		}

		if substr != tt.substr {
			t.Errorf("%s has unexpected substring:\nwant: %q\ngot:  %q", tt.query, tt.substr, substr)
		}
		if regex != tt.regex {
			t.Errorf("%s has unexpected regex:\nwant: %q\ngot:  %q", tt.query, tt.regex, regex)
		}
		if regexAll != tt.regexAll {
			t.Errorf("%s has unexpected regexAll: want=%t got=%t", tt.query, tt.regexAll, regexAll)
		}
	}
}

func TestRepoSet(t *testing.T) {
	d := &indexData{
		repoMetaData:    []zoekt.Repository{{Name: "r0"}, {Name: "r1"}, {Name: "r2"}, {Name: "r3"}},
		fileBranchMasks: []uint64{1, 1, 1, 1, 1, 1},
		repos:           []uint16{0, 0, 1, 2, 3, 3},
	}
	mt, err := d.newMatchTree(&query.RepoSet{Set: map[string]bool{"r1": true, "r3": true, "r99": true}}, matchTreeOpt{})
	if err != nil {
		t.Fatal(err)
	}
	want := []uint32{2, 4, 5}
	for i := range want {
		nextDoc := mt.nextDoc()
		if nextDoc != want[i] {
			t.Fatalf("want %d, got %d", want[i], nextDoc)
		}
		mt.prepare(nextDoc)
	}
	if mt.nextDoc() != math.MaxUint32 {
		t.Fatalf("expected %d document, but got at least 1 more", len(want))
	}
}

func TestRepo(t *testing.T) {
	d := &indexData{
		repoMetaData:    []zoekt.Repository{{Name: "foo"}, {Name: "bar"}},
		fileBranchMasks: []uint64{1, 1, 1, 1, 1},
		repos:           []uint16{0, 0, 1, 0, 1},
	}
	mt, err := d.newMatchTree(&query.Repo{Regexp: regexp.MustCompile("ar")}, matchTreeOpt{})
	if err != nil {
		t.Fatal(err)
	}
	want := []uint32{2, 4}
	for i := range want {
		nextDoc := mt.nextDoc()
		if nextDoc != want[i] {
			t.Fatalf("want %d, got %d", want[i], nextDoc)
		}
		mt.prepare(nextDoc)
	}
	if mt.nextDoc() != math.MaxUint32 {
		t.Fatalf("expect %d documents, but got at least 1 more", len(want))
	}
}

func TestBranchesRepos(t *testing.T) {
	d := &indexData{
		repoMetaData: []zoekt.Repository{
			{ID: hash("foo"), Name: "foo"},
			{ID: hash("bar"), Name: "bar"},
		},
		fileBranchMasks: []uint64{1, 1, 1, 2, 1, 2, 1},
		repos:           []uint16{0, 0, 1, 1, 1, 1, 1},
		branchIDs:       []map[string]uint{{"HEAD": 1}, {"HEAD": 1, "b1": 2}},
	}

	mt, err := d.newMatchTree(&query.BranchesRepos{List: []query.BranchRepos{
		{Branch: "b1", Repos: roaring.BitmapOf(hash("bar"))},
		{Branch: "b2", Repos: roaring.BitmapOf(hash("bar"))},
	}}, matchTreeOpt{})
	if err != nil {
		t.Fatal(err)
	}

	want := []uint32{3, 5}
	for i := range want {
		nextDoc := mt.nextDoc()
		if nextDoc != want[i] {
			t.Fatalf("want %d, got %d", want[i], nextDoc)
		}
		mt.prepare(nextDoc)
	}

	if mt.nextDoc() != math.MaxUint32 {
		t.Fatalf("expect %d documents, but got at least 1 more", len(want))
	}
}

func TestRepoIDs(t *testing.T) {
	d := &indexData{
		repoMetaData:    []zoekt.Repository{{Name: "r0", ID: 0}, {Name: "r1", ID: 1}, {Name: "r2", ID: 2}, {Name: "r3", ID: 3}},
		fileBranchMasks: []uint64{1, 1, 1, 1, 1, 1},
		repos:           []uint16{0, 0, 1, 2, 3, 3},
	}
	q := &query.RepoIDs{Repos: roaring.BitmapOf(1, 3, 99)}
	mt, err := d.newMatchTree(q, matchTreeOpt{})
	if err != nil {
		t.Fatal(err)
	}

	want := []uint32{2, 4, 5}
	for i := range want {
		nextDoc := mt.nextDoc()
		if nextDoc != want[i] {
			t.Fatalf("want %d, got %d", want[i], nextDoc)
		}
		mt.prepare(nextDoc)
	}
	if mt.nextDoc() != math.MaxUint32 {
		t.Fatalf("expected %d document, but got at least 1 more", len(want))
	}
}

func TestIsRegexpAll(t *testing.T) {
	valid := []string{
		".*",
		"(.*)",
		"(?-s:.*)",
		"(?s:.*)",
		"(?i)(?-s:.*)",
	}
	invalid := []string{
		".",
		"foo",
		"(foo.*)",
	}

	for _, s := range valid {
		r, err := syntax.Parse(s, syntax.Perl)
		if err != nil {
			t.Fatal(err)
		}
		if !isRegexpAll(r) {
			t.Errorf("expected %q to match all", s)
		}
	}

	for _, s := range invalid {
		r, err := syntax.Parse(s, syntax.Perl)
		if err != nil {
			t.Fatal(err)
		}
		if isRegexpAll(r) {
			t.Errorf("expected %q to not match all", s)
		}
	}
}

func TestMetaQueryMatchTree(t *testing.T) {
	d := &indexData{
		repoMetaData: []zoekt.Repository{
			{Name: "r0", Metadata: map[string]string{"license": "Apache-2.0"}},
			{Name: "r1", Metadata: map[string]string{"license": "MIT"}},
			{Name: "r2"}, // no metadata
			{Name: "r3", Metadata: map[string]string{"haystack": "needle"}},
			{Name: "r4", Metadata: map[string]string{"note": "test"}},
		},
		fileBranchMasks: []uint64{1, 1, 1, 1, 1}, // 5 docs
		repos:           []uint16{0, 1, 2, 3, 4}, // map docIDs to repos
	}

	q := &query.Meta{
		Field: "license",
		Value: regexp.MustCompile("M.T"),
	}

	mt, err := d.newMatchTree(q, matchTreeOpt{})
	if err != nil {
		t.Fatalf("failed to build matchTree: %v", err)
	}

	// Check that the docMatchTree cache is populated correctly
	checksum := queryMetaChecksum("license", regexp.MustCompile("M.T"))
	cacheKeyField := "Meta"
	if _, ok := d.docMatchTreeCache.get(cacheKeyField, checksum); !ok {
		t.Errorf("expected docMatchTreeCache to be populated for key (%q, %q)", cacheKeyField, checksum)
	}

	var matched []uint32
	for {
		doc := mt.nextDoc()
		if doc == math.MaxUint32 {
			break
		}
		matched = append(matched, doc)
		mt.prepare(doc)
	}

	want := []uint32{1} // only doc from r1 should match
	if !reflect.DeepEqual(matched, want) {
		t.Errorf("meta match failed: got %v, want %v", matched, want)
	}
}

func Test_queryMetaCacheKey(t *testing.T) {
	cases := []struct {
		field   string
		pattern string
		wantKey string
	}{
		{"metaField", "foo.*bar", "24e88a5ffec04af0"},
		{"metaField", "foo.*baz", "d8d6f6a7f0725b61"},
		{"otherField", "foo.*bar", "c9d07e17c028364"},
	}
	for _, tc := range cases {
		re := regexp.MustCompile(tc.pattern)
		key := queryMetaChecksum(tc.field, re)
		if key != tc.wantKey {
			t.Errorf("unexpected key for field=%q pattern=%q: got %q, want %q", tc.field, tc.pattern, key, tc.wantKey)
		}
	}
}
