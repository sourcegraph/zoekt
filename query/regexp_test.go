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
	"regexp/syntax"
	"strings"
	"testing"
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

func TestLowerRegexp(t *testing.T) {
	in := "[a-zA-Z]fooBAR"
	re := mustParseRE(in)
	in = re.String()
	got := LowerRegexp(re)
	want := "[a-za-z]foobar"
	if got.String() != want {
		printRegexp(t, re, 0)
		printRegexp(t, got, 0)
		t.Errorf("got %s, want %s", got, want)
	}

	if re.String() != in {
		t.Errorf("got mutated original %s want %s", re.String(), in)
	}
}

func TestConvertCapture(t *testing.T) {
	in := "(hello)world"
	re := mustParseRE(in)

	got, err := ConvertCapture(re)
	if err != nil {
		t.Fatal(err)
	}

	want := mustParseRE("(?:hello)world")
	if !got.Equal(want) {
		printRegexp(t, got, 0)
		printRegexp(t, want, 0)
		t.Errorf("got %s, want %s", got, want)
	}
}
