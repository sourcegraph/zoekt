// Copyright 2021 Google Inc. All rights reserved.
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
	"fmt"
	"math/rand"
	"regexp"
	"regexp/syntax"
	"strings"
	"testing"

	"github.com/google/zoekt/query"
)

var randOpsTerminal = []syntax.Op{
	syntax.OpEmptyMatch,
	syntax.OpLiteral,      // matches Runes sequence
	syntax.OpCharClass,    // matches Runes interpreted as range pair list
	syntax.OpAnyCharNotNL, // matches any character except newline
	// syntax.OpAnyChar,      // matches any character
	// syntax.OpBeginLine,      // matches empty string at beginning of line
	// syntax.OpEndLine,        // matches empty string at end of line
	// syntax.OpBeginText,      // matches empty string at beginning of text
	// syntax.OpEndText,        // matches empty string at end of text
	// syntax.OpWordBoundary,   // matches word boundary `\b`
	// syntax.OpNoWordBoundary, // matches word non-boundary `\B`
}

var randOptsDeeper = []syntax.Op{
	syntax.OpCapture,   // capturing subexpression with index Cap, optional name Name
	syntax.OpStar,      // matches Sub[0] zero or more times
	syntax.OpPlus,      // matches Sub[0] one or more times
	syntax.OpQuest,     // matches Sub[0] zero or one times
	syntax.OpRepeat,    // matches Sub[0] at least Min times, at most Max (Max == -1 is no limit)
	syntax.OpConcat,    // matches concatenation of Subs
	syntax.OpAlternate, // matches alternation of Subs
}

var alphanumeric = []rune("0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz")

func randomRegexp(maxdepth, maxwidth int, rng *rand.Rand) *syntax.Regexp {
	if maxdepth > 0 && rng.Intn(2) == 0 {
		re := &syntax.Regexp{
			Op: randOptsDeeper[rng.Intn(len(randOptsDeeper))],
		}
		if re.Op == syntax.OpAlternate || re.Op == syntax.OpConcat {
			subCount := 1 + rng.Intn(maxwidth)
			for i := 0; i < subCount; i++ {
				re.Sub = append(re.Sub, randomRegexp(maxdepth-1, maxwidth, rng))
			}
		} else {
			re.Sub = append(re.Sub, randomRegexp(maxdepth-1, maxwidth, rng))
		}
		if re.Op == syntax.OpRepeat {
			re.Min = rng.Intn(3)
			re.Max = -1
			if rng.Intn(3) < 2 {
				re.Max = re.Min + rng.Intn(3)
			}
		}
		return re
	}
	re := &syntax.Regexp{
		Op: randOpsTerminal[rng.Intn(len(randOpsTerminal))],
	}
	if re.Op == syntax.OpLiteral {
		for i := 1 + rng.Intn(2*maxwidth); i > 0; i-- {
			re.Rune = append(re.Rune, alphanumeric[rng.Intn(len(alphanumeric))])
		}
	} else if re.Op == syntax.OpCharClass {
		for i := 1 + rng.Intn(maxwidth); i > 0; i-- {
			lo := rng.Intn(len(alphanumeric))
			hi := lo
			if rng.Intn(2) == 0 {
				hi += rng.Intn(len(alphanumeric) - hi)
			}
			re.Rune = append(re.Rune, alphanumeric[lo], alphanumeric[hi])
		}
	}

	return re
}

func randomMatch(re *syntax.Regexp, rng *rand.Rand) string {
	sb := strings.Builder{}

	var recur func(re *syntax.Regexp)
	recur = func(re *syntax.Regexp) {
		switch re.Op {
		case syntax.OpEmptyMatch:
		case syntax.OpAnyCharNotNL:
			sb.WriteRune(alphanumeric[rng.Intn(len(alphanumeric))])
		case syntax.OpLiteral:
			sb.WriteString(string(re.Rune))
		case syntax.OpCharClass:
			i := rng.Intn(len(re.Rune)/2) * 2
			lo, hi := re.Rune[i], re.Rune[i+1]
			sb.WriteRune(rune(int(lo) + rng.Intn(int(hi)-int(lo)+1)))
		case syntax.OpConcat, syntax.OpCapture:
			for _, subre := range re.Sub {
				recur(subre)
			}
		case syntax.OpRepeat:
			i := re.Min
			if re.Max > re.Min {
				i = re.Min + rng.Intn(re.Max-re.Min)
			}
			for ; i > 0; i-- {
				recur(re.Sub[0])
			}
		case syntax.OpPlus:
			for i := 1 + rng.Intn(4); i > 0; i-- {
				recur(re.Sub[0])
			}
		case syntax.OpStar:
			for i := rng.Intn(4); i > 0; i-- {
				recur(re.Sub[0])
			}
		case syntax.OpQuest:
			if rng.Intn(4) < 3 {
				recur(re.Sub[0])
			}
		case syntax.OpAlternate:
			recur(re.Sub[rng.Intn(len(re.Sub))])
		default:
			fmt.Println("unknown op:", re.Op.String())
		}
	}

	recur(re)

	return sb.String()
}

func TestRandomMatch(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for i := 0; i < 1000; i++ {
		re := randomRegexp(4, 5, rng)
		ex := randomMatch(re, rng)
		ex2 := randomMatch(re, rng)
		ex3 := randomMatch(re, rng)
		if ex == "" && ex2 == "" && ex3 == "" {
			continue
		}
		re2 := regexp.MustCompile(re.String())
		reStr := strings.ReplaceAll(re.String(), "(?-s:.)", ".")
		// fmt.Println(reStr, ex, ex2, ex3)
		if !re2.MatchString(ex) {
			t.Error(reStr, ex)
		}
		if !re2.MatchString(ex2) {
			t.Error(reStr, ex2)
		}
		if !re2.MatchString(ex3) {
			t.Error(reStr, ex3)
		}
	}
}

func TestFuzzSearcher(t *testing.T) {
	rng := rand.New(rand.NewSource(4))
	for i := 0; i < 10000; i++ {
		re := randomRegexp(4, 5, rng)
		ex := randomMatch(re, rng)
		ex2 := randomMatch(re, rng)
		ex3 := randomMatch(re, rng)
		if ex == "" && ex2 == "" && ex3 == "" {
			continue
		}
		re2 := regexp.MustCompile(re.String())
		reStr := strings.ReplaceAll(re.String(), "(?-s:.)", ".")

		if !re2.MatchString(ex) {
			t.Error(reStr, ex)
		}
		if !re2.MatchString(ex2) {
			t.Error(reStr, ex2)
		}
		if !re2.MatchString(ex3) {
			t.Error(reStr, ex3)
		}

		b := testIndexBuilder(t, nil,
			Document{Name: "f1", Content: []byte(ex + "\n" + ex2 + "\n" + ex3)},
		)

		q := &query.Regexp{
			Regexp:        mustParseRE(re.String()),
			CaseSensitive: true,
		}

		searcher := searcherForTest(t, b)
		opts := SearchOptions{}
		res, err := searcher.Search(context.Background(), q, &opts)
		if err != nil {
			t.Fatalf("Search(%s): %v", q, err)
		}
		clearScores(res)

		exp := 0
		if ex != "" {
			exp++
		}
		if ex2 != "" {
			exp++
		}
		if ex3 != "" {
			exp++
		}

		if res.MatchCount < exp {
			t.Errorf("bad match count: got %d want %d, regexp %v examples %q %q %q",
				res.MatchCount, exp, reStr, ex, ex2, ex3)
		}
	}

}
