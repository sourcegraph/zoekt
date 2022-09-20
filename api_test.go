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

package zoekt // import "github.com/sourcegraph/zoekt"

import (
	"bytes"
	"encoding/gob"
	"strings"
	"testing"
)

/*
BenchmarkMinimalRepoListEncodings/slice-8         	    570	  2145665 ns/op	   753790 bytes	   3981 B/op	      0 allocs/op
BenchmarkMinimalRepoListEncodings/map-8           	    360	  3337522 ns/op	   740778 bytes	 377777 B/op	  13002 allocs/op
*/
func BenchmarkMinimalRepoListEncodings(b *testing.B) {
	size := uint32(13000) // 2021-06-24 rough estimate of number of repos on a replica.

	type Slice struct {
		ID         uint32
		HasSymbols bool
		Branches   []RepositoryBranch
	}

	branches := []RepositoryBranch{{Name: "HEAD", Version: strings.Repeat("a", 40)}}
	mapData := make(map[uint32]*MinimalRepoListEntry, size)
	sliceData := make([]Slice, 0, size)

	for id := uint32(1); id <= size; id++ {
		mapData[id] = &MinimalRepoListEntry{
			HasSymbols: true,
			Branches:   branches,
		}
		sliceData = append(sliceData, Slice{
			ID:         id,
			HasSymbols: true,
			Branches:   branches,
		})
	}

	b.Run("slice", benchmarkEncoding(sliceData))

	b.Run("map", benchmarkEncoding(mapData))
}

func benchmarkEncoding(data interface{}) func(*testing.B) {
	return func(b *testing.B) {
		b.Helper()

		var buf bytes.Buffer
		enc := gob.NewEncoder(&buf)
		err := enc.Encode(data)
		if err != nil {
			b.Fatal(err)
		}

		b.ReportAllocs()
		b.ResetTimer()
		b.ReportMetric(float64(buf.Len()), "bytes")
		for i := 0; i < b.N; i++ {
			_ = enc.Encode(data)
			buf.Reset()
		}
	}
}

func TestSizeBytesSearchResult(t *testing.T) {
	var sr = SearchResult{
		Stats:    Stats{},    // 128 bytes
		Progress: Progress{}, // 16 bytes
		Files: []FileMatch{{ // 24 bytes + 460 bytes
			Score:       0,   // 8 bytes
			Debug:       "",  // 16 bytes
			FileName:    "",  // 16 bytes
			Repository:  "",  // 16 bytes
			Branches:    nil, // 24 bytes
			LineMatches: nil, // 24 bytes
			ChunkMatches: []ChunkMatch{{ // 24 bytes + 208 bytes (see TestSizeByteChunkMatches)
				Content:      []byte("foo"),
				ContentStart: Location{},
				FileName:     false,
				Ranges:       []Range{{}},
				SymbolInfo:   []*Symbol{{}},
				Score:        0,
				DebugScore:   "",
			}},
			RepositoryID:       0,   // 4 bytes
			RepositoryPriority: 0,   // 8 bytes
			Content:            nil, // 24 bytes
			Checksum:           nil, // 24 bytes
			Language:           "",  // 16 bytes
			SubRepositoryName:  "",  // 16 bytes
			SubRepositoryPath:  "",  // 16 bytes
			Version:            "",  // 16 bytes
		}},
		RepoURLs:      nil, // 48 bytes
		LineFragments: nil, // 48 bytes
	}

	var wantBytes uint64 = 724
	if sr.SizeBytes() != wantBytes {
		t.Fatalf("want %d, got %d", wantBytes, sr.SizeBytes())
	}
}

func TestSizeBytesChunkMatches(t *testing.T) {
	cm := ChunkMatch{
		Content:      []byte("foo"), // 24 + 3 bytes
		ContentStart: Location{},    // 12 bytes
		FileName:     false,         // 1 byte
		Ranges:       []Range{{}},   // 24 bytes (slice header) + 24 bytes (content)
		SymbolInfo:   []*Symbol{{}}, // 24 bytes (slice header) + 4 * 16 bytes (string header) + 8 bytes (pointer)
		Score:        0,             // 8 byte
		DebugScore:   "",            // 16 bytes (string header)
	}

	var wantBytes uint64 = 208
	if cm.sizeBytes() != wantBytes {
		t.Fatalf("want %d, got %d", wantBytes, cm.sizeBytes())
	}
}
