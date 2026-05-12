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
	"reflect"
	"strings"
	"testing"
)

func TestMatchSize(t *testing.T) {
	cases := []struct {
		v    any
		size int
	}{{
		v:    candidateMatch{},
		size: 80,
	}, {
		v:    candidateChunk{},
		size: 40,
	}}
	for _, c := range cases {
		got := reflect.TypeOf(c.v).Size()
		if int(got) != c.size {
			t.Errorf(`sizeof struct %T has changed from %d to %d.
These are match structs that occur a lot in memory, so we optimize size.
When changing, please ensure there isn't unnecessary padding via the
tool fieldalignment then update this test.`, c.v, c.size, got)
		}
	}
}

func TestCandidateMatchContentOutOfBounds(t *testing.T) {
	for _, tc := range []struct {
		name  string
		match candidateMatch
	}{
		{
			name: "offset past content",
			match: candidateMatch{
				byteOffset:    4,
				substrLowered: []byte("x"),
			},
		},
		{
			name: "case-sensitive match extends past content",
			match: candidateMatch{
				byteOffset:    2,
				substrBytes:   []byte("cd"),
				caseSensitive: true,
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.match.matchContent([]byte("abc")) {
				t.Fatal("matchContent returned true for an out-of-bounds match")
			}
		})
	}
}

func TestFindOffsetRejectsByteOffsetBeforeFileStart(t *testing.T) {
	cp := contentProvider{
		id: &indexData{
			fileNameContent:  []byte("previous/current"),
			fileNameIndex:    []uint32{9, 16},
			fileNameEndRunes: []uint32{7},
		},
	}

	if got, want := cp.findOffset(true, 0), uint32(7); got != want {
		t.Fatalf("findOffset returned %d, want file size %d", got, want)
	}
	if cp.err == nil || !strings.Contains(cp.err.Error(), "before file start") {
		t.Fatalf("findOffset error = %v", cp.err)
	}
}
