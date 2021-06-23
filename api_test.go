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
	"testing"
)

/*
BenchmarkMinimalRepoListEncodings
BenchmarkMinimalRepoListEncodings/slice
    benchmark.go:192: bytes: 205
BenchmarkMinimalRepoListEncodings/slice-16         	 2129965	       561.4 ns/op	       0 B/op	       0 allocs/op
BenchmarkMinimalRepoListEncodings/map
    benchmark.go:192: bytes: 191
BenchmarkMinimalRepoListEncodings/map-16           	 1516594	       792.1 ns/op	     152 B/op	       4 allocs/op
PASS
*/
func BenchmarkMinimalRepoListEncodings(b *testing.B) {
	branches := []RepositoryBranch{{Name: "main"}, {Name: "dev"}}

	type Slice []struct {
		ID         uint32
		HasSymbols bool
		Branches   []RepositoryBranch
	}

	b.Run("slice", benchmarkEncoding(Slice{
		{
			ID:         1,
			HasSymbols: true,
			Branches:   branches,
		},
		{
			ID:         2,
			HasSymbols: false,
			Branches:   branches,
		},
	}))

	b.Run("map", benchmarkEncoding(map[uint32]*MinimalRepoListEntry{
		1: {
			HasSymbols: true,
			Branches:   branches,
		},
		2: {
			HasSymbols: false,
			Branches:   branches,
		},
	}))
}

func benchmarkEncoding(data interface{}) func(*testing.B) {
	return func(b *testing.B) {
		b.StopTimer()
		b.Helper()

		var buf bytes.Buffer
		enc := gob.NewEncoder(&buf)
		err := enc.Encode(data)
		if err != nil {
			b.Fatal(err)
		}

		b.ReportAllocs()
		b.Logf("bytes: %d", buf.Len())
		b.StartTimer()
		for i := 0; i < b.N; i++ {
			enc.Encode(data)
			buf.Reset()
		}
	}
}
