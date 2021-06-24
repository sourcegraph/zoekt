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

package zoekt // import "github.com/google/zoekt"

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
