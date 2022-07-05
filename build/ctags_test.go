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
	"reflect"
	"testing"

	"github.com/google/zoekt"
	"github.com/google/zoekt/ctags"
)

func TestTagsToSections(t *testing.T) {
	c := []byte("package foo\nfunc bar(j int) {}\n//bla")
	// ----------01234567890 1234567890123456789 012345

	tags := []*ctags.Entry{
		{
			Name: "bar",
			Line: 2,
		},
	}

	secs, _, err := tagsToSections(c, tags)
	if err != nil {
		t.Fatal("tagsToSections", err)
	}

	if len(secs) != 1 || secs[0].Start != 17 || secs[0].End != 20 {
		t.Fatalf("got %#v, want 1 section (17,20)", secs)
	}
}

func TestTagsToSectionsMultiple(t *testing.T) {
	c := []byte("class Foo { int x; int b; }")
	// ----------0123456789012345678901234567

	tags := []*ctags.Entry{
		{
			Name: "x",
			Line: 1,
		},
		{
			Name: "b",
			Line: 1,
		},
	}

	got, _, err := tagsToSections(c, tags)
	if err != nil {
		t.Fatal("tagsToSections", err)
	}

	want := []zoekt.DocumentSection{
		{Start: 16, End: 17},
		{Start: 23, End: 24},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestTagsToSectionsEOF(t *testing.T) {
	c := []byte("package foo\nfunc bar(j int) {}")
	// ----------01234567890 1234567890123456789 012345

	tags := []*ctags.Entry{
		{
			Name: "bar",
			Line: 2,
		},
	}

	secs, _, err := tagsToSections(c, tags)
	if err != nil {
		t.Fatal("tagsToSections", err)
	}

	if len(secs) != 1 || secs[0].Start != 17 || secs[0].End != 20 {
		t.Fatalf("got %#v, want 1 section (17,20)", secs)
	}
}

func TestOverlaps(t *testing.T) {
	tests := []struct {
		srs symbolRanges
		sr  [2]uint32
		pos int
	}{
		//
		// overlap
		//
		{
			srs: [][2]uint32{{0, 3}, {5, 10}},
			sr:  [2]uint32{6, 9},
			pos: -1,
		},
		{
			srs: [][2]uint32{{0, 3}, {5, 10}},
			sr:  [2]uint32{6, 12},
			pos: -1,
		},
		{
			srs: [][2]uint32{{0, 3}, {5, 10}},
			sr:  [2]uint32{4, 6},
			pos: -1,
		},
		{
			srs: [][2]uint32{{0, 3}, {5, 10}},
			sr:  [2]uint32{1, 6},
			pos: -1,
		},
		{
			srs: [][2]uint32{{0, 3}, {5, 10}},
			sr:  [2]uint32{0, 1},
			pos: -1,
		},
		//
		// NO overlap
		//
		{
			srs: [][2]uint32{{2, 3}, {5, 10}},
			sr:  [2]uint32{0, 1},
			pos: 0,
		},
		{
			srs: [][2]uint32{{0, 3}, {5, 10}},
			sr:  [2]uint32{3, 4},
			pos: 1,
		},
		{
			srs: [][2]uint32{{0, 3}, {5, 10}},
			sr:  [2]uint32{3, 4},
			pos: 1,
		},
		{
			srs: [][2]uint32{{0, 3}, {5, 10}},
			sr:  [2]uint32{11, 12},
			pos: 2,
		},
		{
			srs: [][2]uint32{{0, 3}, {5, 10}, {14, 15}},
			sr:  [2]uint32{11, 12},
			pos: 2,
		},
		{
			srs: nil,
			sr:  [2]uint32{11, 12},
			pos: 0,
		},
		{
			srs: [][2]uint32{{0, 3}},
			sr:  [2]uint32{0, 3},
			pos: -1,
		},
	}

	for _, tt := range tests {
		t.Run("", func(t *testing.T) {
			got := tt.srs.overlaps(tt.sr)
			if got != tt.pos {
				t.Fatalf("want %d, got %d", tt.pos, got)
			}
		})
	}
}
