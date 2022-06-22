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

package zoekt

import (
	"testing"

	"github.com/RoaringBitmap/roaring"
	"github.com/google/zoekt/query"
	"github.com/grafana/regexp"
)

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
	}

	for _, tt := range tests {
		q, err := query.Parse(tt.query)
		if err != nil {
			t.Errorf("Error parsing query: %s", "sym:"+tt.query)
			continue
		}

		d := &indexData{}
		mt, err := d.newMatchTree(q)
		if err != nil {
			t.Errorf("Error creating match tree from query: %s", q)
			continue
		}

		visitMatchTree(mt, func(m matchTree) {
			if _, ok := m.(*regexpMatchTree); ok && tt.skip {
				t.Errorf("Expected regexpMatchTree to be skipped for query: %s", q)
			}
		})
	}
}

func TestSymbolMatchRegexAll(t *testing.T) {
	tests := []struct {
		query string
		all   bool
	}{
		{query: ".*", all: true},
		{query: "(a|b)", all: false},
		{query: "b.r", all: false},
	}

	for _, tt := range tests {
		q, err := query.Parse("sym:" + tt.query)
		if err != nil {
			t.Errorf("Error parsing query: %s", "sym:"+tt.query)
			continue
		}

		d := &indexData{}
		mt, err := d.newMatchTree(q)
		if err != nil {
			t.Errorf("Error creating match tree from query: %s", q)
			continue
		}

		regexMT, ok := mt.(*symbolRegexpMatchTree)
		if !ok {
			t.Errorf("Expected symbol regex match tree from query: %s, got %v", q, mt)
			continue
		}

		if regexMT.all != tt.all {
			t.Errorf("Expected property all: %t from query: %s", tt.all, q)
		}
	}
}

func TestRepoSet(t *testing.T) {
	d := &indexData{
		repoMetaData:    []Repository{{Name: "r0"}, {Name: "r1"}, {Name: "r2"}, {Name: "r3"}},
		fileBranchMasks: []uint64{1, 1, 1, 1, 1, 1},
		repos:           []uint16{0, 0, 1, 2, 3, 3},
	}
	mt, err := d.newMatchTree(&query.RepoSet{Set: map[string]bool{"r1": true, "r3": true, "r99": true}})
	if err != nil {
		t.Fatal(err)
	}
	want := []uint32{2, 4, 5}
	for i := 0; i < len(want); i++ {
		nextDoc := mt.nextDoc()
		if nextDoc != want[i] {
			t.Fatalf("want %d, got %d", want[i], nextDoc)
		}
		mt.prepare(nextDoc)
	}
	if mt.nextDoc() != maxUInt32 {
		t.Fatalf("expected %d document, but got at least 1 more", len(want))
	}
}

func TestRepo(t *testing.T) {
	d := &indexData{
		repoMetaData:    []Repository{{Name: "foo"}, {Name: "bar"}},
		fileBranchMasks: []uint64{1, 1, 1, 1, 1},
		repos:           []uint16{0, 0, 1, 0, 1},
	}
	mt, err := d.newMatchTree(&query.Repo{Regexp: regexp.MustCompile("ar")})
	if err != nil {
		t.Fatal(err)
	}
	want := []uint32{2, 4}
	for i := 0; i < len(want); i++ {
		nextDoc := mt.nextDoc()
		if nextDoc != want[i] {
			t.Fatalf("want %d, got %d", want[i], nextDoc)
		}
		mt.prepare(nextDoc)
	}
	if mt.nextDoc() != maxUInt32 {
		t.Fatalf("expect %d documents, but got at least 1 more", len(want))
	}
}

func TestBranchesRepos(t *testing.T) {
	d := &indexData{
		repoMetaData: []Repository{
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
	}})
	if err != nil {
		t.Fatal(err)
	}

	want := []uint32{3, 5}
	for i := 0; i < len(want); i++ {
		nextDoc := mt.nextDoc()
		if nextDoc != want[i] {
			t.Fatalf("want %d, got %d", want[i], nextDoc)
		}
		mt.prepare(nextDoc)
	}

	if mt.nextDoc() != maxUInt32 {
		t.Fatalf("expect %d documents, but got at least 1 more", len(want))
	}
}
