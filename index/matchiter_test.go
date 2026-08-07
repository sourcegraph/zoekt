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
	"fmt"
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

func TestCandidateMatchContentBounds(t *testing.T) {
	for _, tc := range []struct {
		name  string
		match candidateMatch
		want  string
	}{
		{
			name: "offset after content",
			match: candidateMatch{
				byteOffset:    4,
				substrLowered: []byte("x"),
			},
			want: "after content size",
		},
		{
			name: "case-sensitive span after filename",
			match: candidateMatch{
				byteOffset:    2,
				substrBytes:   []byte("cd"),
				caseSensitive: true,
				fileName:      true,
			},
			want: "exceeds filename size",
		},
		{
			name: "case-insensitive span after content",
			match: candidateMatch{
				byteOffset:    2,
				substrLowered: []byte("cd"),
			},
			want: "extends beyond content size",
		},
		{
			name: "case-insensitive mismatch before truncated content end",
			match: candidateMatch{
				byteOffset:    2,
				substrLowered: []byte("dx"),
			},
			want: "extends beyond content size",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			matched, err := tc.match.matchContent([]byte("abc"))
			if matched || err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("matchContent = (%t, %v), want false and error containing %q", matched, err, tc.want)
			}
		})
	}

	t.Run("case-insensitive mismatch with complete span", func(t *testing.T) {
		match := candidateMatch{byteOffset: 1, substrLowered: []byte("dx")}
		matched, err := match.matchContent([]byte("abc"))
		if matched || err != nil {
			t.Fatalf("matchContent = (%t, %v), want ordinary non-match", matched, err)
		}
	})
}

func TestFindOffsetReportsInvalidMappings(t *testing.T) {
	for _, tc := range []struct {
		name string
		id   indexData
		idx  uint32
		r    uint32
		want string
	}{
		{
			name: "before file start",
			id: indexData{
				fileNameContent:  []byte("previous/current"),
				fileNameIndex:    []uint32{9, 16},
				fileNameEndRunes: []uint32{7},
			},
			want: "before file start",
		},
		{
			name: "after file end",
			id: indexData{
				fileNameContent:     []byte("abc-extra"),
				fileNameIndex:       []uint32{0, 3},
				fileNameEndRunes:    []uint32{3},
				fileNameRuneOffsets: runeOffsetMap{{runeOffset: 0, byteOffset: 4}},
			},
			r:    1,
			want: "after file end",
		},
		{
			name: "unavailable decode bytes",
			id: indexData{
				fileNameContent:  []byte("a"),
				fileNameIndex:    []uint32{0, 1},
				fileNameEndRunes: []uint32{1},
			},
			r:    2,
			want: "no decode bytes",
		},
		{
			name: "interpolation overflow",
			id: indexData{
				fileNameContent:     []byte("abc"),
				fileNameIndex:       []uint32{0, 3},
				fileNameEndRunes:    []uint32{3},
				fileNameRuneOffsets: runeOffsetMap{{runeOffset: 0, byteOffset: ^uint32(0)}},
			},
			r:    1,
			want: "past filename data size",
		},
		{
			name: "absolute rune offset overflow",
			id: indexData{
				fileNameContent:  []byte("ab"),
				fileNameIndex:    []uint32{0, 1, 2},
				fileNameEndRunes: []uint32{^uint32(0), 0},
			},
			idx:  1,
			r:    1,
			want: "overflows the corpus rune offset",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cp := contentProvider{id: &tc.id, idx: tc.idx}
			var recovered any
			func() {
				defer func() { recovered = recover() }()
				cp.findOffset(true, tc.r)
			}()
			panicText := fmt.Sprint(recovered)
			if recovered == nil || !strings.Contains(panicText, tc.want) {
				t.Fatalf("findOffset panic = %q, want panic containing %q", panicText, tc.want)
			}
		})
	}
}
