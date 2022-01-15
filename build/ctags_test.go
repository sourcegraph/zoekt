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
	c := []byte("class Foob { int x; int b; }")
	// ----------012345678901234567890123456789

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
		{Start: 17, End: 18},
		{Start: 24, End: 25},
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
