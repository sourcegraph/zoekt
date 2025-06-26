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

package query

import (
	"log"
	"reflect"
	"regexp/syntax"
	"testing"

	"github.com/grafana/regexp"
)

var _ = log.Println

func TestQueryString(t *testing.T) {
	q := &Or{[]Q{
		&And{[]Q{
			&Substring{Pattern: "hoi"},
			&Not{&Substring{Pattern: "hai"}},
		}},
	}}
	got := q.String()
	want := `(or (and substr:"hoi" (not substr:"hai")))`

	if got != want {
		t.Errorf("got %s, want %s", got, want)
	}
}

func TestSimplify(t *testing.T) {
	type testcase struct {
		in   Q
		want Q
	}

	cases := []testcase{
		{
			in: NewOr(
				NewOr(
					NewAnd(&Substring{Pattern: "hoi"},
						&Not{&Substring{Pattern: "hai"}}),
					NewOr(
						&Substring{Pattern: "zip"},
						&Substring{Pattern: "zap"},
					))),
			want: NewOr(
				NewAnd(
					&Substring{Pattern: "hoi"},
					&Not{&Substring{Pattern: "hai"}}),
				&Substring{Pattern: "zip"},
				&Substring{Pattern: "zap"}),
		},
		{in: &And{}, want: &Const{true}},
		{in: &Or{}, want: &Const{false}},
		{in: NewAnd(&Const{true}, &Const{false}), want: &Const{false}},
		{in: NewOr(&Const{false}, &Const{true}), want: &Const{true}},
		{in: &Not{&Const{true}}, want: &Const{false}},
		{
			in: NewAnd(
				&Substring{Pattern: "byte"},
				&Not{NewAnd(&Substring{Pattern: "byte"})}),
			want: NewAnd(
				&Substring{Pattern: "byte"},
				&Not{&Substring{Pattern: "byte"}}),
		},
		{
			in: NewAnd(
				NewSingleBranchesRepos("HEAD"), // Empty list matches nothing
				&Not{&Type{Type: TypeRepo, Child: &Substring{Pattern: "hi"}}}),
			want: &Const{false},
		},
		{
			in: NewAnd(
				NewSingleBranchesRepos("HEAD", 1),
				&Not{&Type{Type: TypeRepo, Child: &Substring{Pattern: "hi"}}}),
			want: NewAnd(
				NewSingleBranchesRepos("HEAD", 1),
				&Not{&Type{Type: TypeRepo, Child: &Substring{Pattern: "hi"}}}),
		},
	}

	for _, c := range cases {
		got := Simplify(c.in)
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("got %s, want %s", got, c.want)
		}
	}
}

func TestMap(t *testing.T) {
	in := NewAnd(&Substring{Pattern: "bla"}, &Not{&Repo{Regexp: regexp.MustCompile("foo")}})
	out := NewAnd(&Substring{Pattern: "bla"}, &Not{&Const{false}})

	f := func(q Q) Q {
		if _, ok := q.(*Repo); ok {
			return &Const{false}
		}
		return q
	}
	got := Map(in, f)
	if !reflect.DeepEqual(got, out) {
		t.Errorf("got %v, want %v", got, out)
	}
}

func TestVisitAtoms(t *testing.T) {
	in := NewAnd(&Substring{}, &Repo{}, &Not{&Const{}})
	count := 0
	VisitAtoms(in, func(q Q) {
		count++
	})
	if count != 3 {
		t.Errorf("got %d, want 3", count)
	}
}

func TestExpandFileContent(t *testing.T) {
	re, _ := syntax.Parse("foo", syntax.Perl)

	cases := []struct {
		q    Q
		want string
	}{
		{
			q:    &Substring{FileName: true, Content: true},
			want: "(or file_substr:\"\" content_substr:\"\")",
		},
		{
			q:    &Substring{FileName: false, Content: false},
			want: "(or file_substr:\"\" content_substr:\"\")",
		},

		{
			q:    &Substring{FileName: true, Content: false},
			want: "file_substr:\"\"",
		},
		{
			q:    &Substring{FileName: false, Content: true},
			want: "content_substr:\"\"",
		},
		{
			q:    &Regexp{Regexp: re, FileName: true, Content: true},
			want: "(or file_regex:\"foo\" regex:\"foo\")",
		},
		{
			q:    &Regexp{Regexp: re, FileName: false, Content: false},
			want: "(or file_regex:\"foo\" regex:\"foo\")",
		},

		{
			q:    &Regexp{Regexp: re, FileName: true, Content: false},
			want: "file_regex:\"foo\"",
		},
		{
			q:    &Regexp{Regexp: re, FileName: false, Content: true},
			want: "regex:\"foo\"",
		},
	}

	for _, tt := range cases {
		t.Run("", func(t *testing.T) {
			if got := ExpandFileContent(tt.q); got.String() != tt.want {
				t.Fatalf("got %s, want %s\n", got.String(), tt.want)
			}
		})
	}
}
