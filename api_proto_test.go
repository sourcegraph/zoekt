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

package zoekt // import "github.com/sourcegraph/zoekt"

import (
	"bytes"
	_ "embed"
	"encoding/gob"
	"fmt"
	"math/rand"
	"reflect"
	"testing"
	"testing/quick"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"google.golang.org/protobuf/proto"

	v1 "github.com/sourcegraph/zoekt/grpc/v1"
)

func TestProtoRoundtrip(t *testing.T) {
	t.Run("FileMatch", func(t *testing.T) {
		f := func(f1 FileMatch) bool {
			p1 := f1.ToProto()
			f2 := FileMatchFromProto(p1)
			return reflect.DeepEqual(f1, f2)
		}
		if err := quick.Check(f, nil); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("ChunkMatch", func(t *testing.T) {
		f := func(f1 ChunkMatch) bool {
			p1 := f1.ToProto()
			f2 := ChunkMatchFromProto(p1)
			return reflect.DeepEqual(f1, f2)
		}
		if err := quick.Check(f, nil); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("Range", func(t *testing.T) {
		f := func(f1 Range) bool {
			p1 := f1.ToProto()
			f2 := RangeFromProto(p1)
			return reflect.DeepEqual(f1, f2)
		}
		if err := quick.Check(f, nil); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("Location", func(t *testing.T) {
		f := func(f1 Range) bool {
			p1 := f1.ToProto()
			f2 := RangeFromProto(p1)
			return reflect.DeepEqual(f1, f2)
		}
		if err := quick.Check(f, nil); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("LineMatch", func(t *testing.T) {
		f := func(f1 LineMatch) bool {
			p1 := f1.ToProto()
			f2 := LineMatchFromProto(p1)
			return reflect.DeepEqual(f1, f2)
		}
		if err := quick.Check(f, nil); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("Symbol", func(t *testing.T) {
		f := func(f1 *Symbol) bool {
			p1 := f1.ToProto()
			f2 := SymbolFromProto(p1)
			return reflect.DeepEqual(f1, f2)
		}
		if err := quick.Check(f, nil); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("FlushReson", func(t *testing.T) {
		f := func(f1 FlushReason) bool {
			p1 := f1.ToProto()
			f2 := FlushReasonFromProto(p1)
			return reflect.DeepEqual(f1.String(), f2.String())
		}
		if err := quick.Check(f, nil); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("Stats", func(t *testing.T) {
		f := func(f1 Stats) bool {
			p1 := f1.ToProto()
			f2 := StatsFromProto(p1)
			return reflect.DeepEqual(f1, f2)
		}
		if err := quick.Check(f, nil); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("Progress", func(t *testing.T) {
		f := func(f1 Progress) bool {
			p1 := f1.ToProto()
			f2 := ProgressFromProto(p1)
			return reflect.DeepEqual(f1, f2)
		}
		if err := quick.Check(f, nil); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("SearchResult", func(t *testing.T) {
		f := func(f1 *SearchResult) bool {
			p1 := f1.ToProto()
			f2 := SearchResultFromProto(p1)
			return reflect.DeepEqual(f1, f2)
		}
		if err := quick.Check(f, nil); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("Repository", func(t *testing.T) {
		f := func(f1 *Repository) bool {
			p1 := f1.ToProto()
			f2 := RepositoryFromProto(p1)
			if diff := cmp.Diff(f1, &f2, cmpopts.IgnoreUnexported(Repository{})); diff != "" {
				fmt.Printf("got diff: %s", diff)
				return false
			}
			return true
		}
		if err := quick.Check(f, nil); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("IndexMetadata", func(t *testing.T) {
		f := func(f1 *IndexMetadata) bool {
			p1 := f1.ToProto()
			f2 := IndexMetadataFromProto(p1)
			if diff := cmp.Diff(f1, &f2); diff != "" {
				fmt.Printf("got diff: %s", diff)
				return false
			}
			return true
		}
		if err := quick.Check(f, nil); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("RepoStats", func(t *testing.T) {
		f := func(f1 RepoStats) bool {
			p1 := f1.ToProto()
			f2 := RepoStatsFromProto(p1)
			if diff := cmp.Diff(f1, f2); diff != "" {
				fmt.Printf("got diff: %s", diff)
				return false
			}
			return true
		}
		if err := quick.Check(f, nil); err != nil {
			t.Fatal(err)
		}
	})

	// t.Run("RepoListEntry", func(t *testing.T) {
	// 	f := func(f1 *RepoListEntry) bool {
	// 		p1 := f1.ToProto()
	// 		f2 := RepoListEntryFromProto(p1)
	// 		if diff := cmp.Diff(f1, f2); diff != "" {
	// 			fmt.Printf("got diff: %s", diff)
	// 			return false
	// 		}
	// 		return true
	// 	}
	// 	if err := quick.Check(f, nil); err != nil {
	// 		t.Fatal(err)
	// 	}
	// })
	//
	t.Run("RepositoryBranch", func(t *testing.T) {
		f := func(f1 RepositoryBranch) bool {
			p1 := f1.ToProto()
			f2 := RepositoryBranchFromProto(p1)
			if diff := cmp.Diff(f1, f2); diff != "" {
				fmt.Printf("got diff: %s", diff)
				return false
			}
			return true
		}
		if err := quick.Check(f, nil); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("MinimalRepoListEntry", func(t *testing.T) {
		f := func(f1 MinimalRepoListEntry) bool {
			p1 := f1.ToProto()
			f2 := MinimalRepoListEntryFromProto(p1)
			if diff := cmp.Diff(f1, f2); diff != "" {
				fmt.Printf("got diff: %s", diff)
				return false
			}
			return true
		}
		if err := quick.Check(f, nil); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("ListOptions", func(t *testing.T) {
		f := func(f1 *ListOptions) bool {
			p1 := f1.ToProto()
			f2 := ListOptionsFromProto(p1)
			if diff := cmp.Diff(f1, f2); diff != "" {
				fmt.Printf("got diff: %s", diff)
				return false
			}
			return true
		}
		if err := quick.Check(f, nil); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("SearchOptions", func(t *testing.T) {
		f := func(f1 *SearchOptions) bool {
			if f1 != nil {
				// Ignore deprecated and unimplemented fields
				f1.ShardMaxImportantMatch = 0
				f1.TotalMaxImportantMatch = 0
			}
			p1 := f1.ToProto()
			f2 := SearchOptionsFromProto(p1)
			if diff := cmp.Diff(f1, f2); diff != "" {
				fmt.Printf("got diff: %s", diff)
				return false
			}
			return true
		}
		if err := quick.Check(f, nil); err != nil {
			t.Fatal(err)
		}
	})
}

func (*IndexMetadata) Generate(r *rand.Rand, size int) reflect.Value {
	indexTime := time.Now().Add(time.Duration(r.Int63n(1000)) * time.Hour)
	var i IndexMetadata
	i.IndexFormatVersion = gen(i.IndexFormatVersion, r)
	i.IndexFeatureVersion = gen(i.IndexFeatureVersion, r)
	i.IndexMinReaderVersion = gen(i.IndexMinReaderVersion, r)
	i.IndexTime = indexTime
	i.PlainASCII = gen(i.PlainASCII, r)
	i.LanguageMap = gen(i.LanguageMap, r)
	i.ZoektVersion = gen(i.ZoektVersion, r)
	i.ID = gen(i.ID, r)
	return reflect.ValueOf(&i)
}

func (*Repository) Generate(rng *rand.Rand, size int) reflect.Value {
	latestCommitDate := time.Now().Add(time.Duration(rng.Int63n(1000)) * time.Hour)
	var r Repository
	v := &Repository{
		ID:                   gen(r.ID, rng),
		Name:                 gen(r.Name, rng),
		URL:                  gen(r.URL, rng),
		Source:               gen(r.Source, rng),
		Branches:             gen(r.Branches, rng),
		SubRepoMap:           map[string]*Repository{},
		CommitURLTemplate:    gen(r.CommitURLTemplate, rng),
		FileURLTemplate:      gen(r.FileURLTemplate, rng),
		LineFragmentTemplate: gen(r.LineFragmentTemplate, rng),
		priority:             gen(r.priority, rng),
		RawConfig:            gen(r.RawConfig, rng),
		Rank:                 gen(r.Rank, rng),
		IndexOptions:         gen(r.IndexOptions, rng),
		HasSymbols:           gen(r.HasSymbols, rng),
		Tombstone:            gen(r.Tombstone, rng),
		LatestCommitDate:     latestCommitDate,
		FileTombstones:       gen(r.FileTombstones, rng),
	}
	return reflect.ValueOf(v)
}

func (RepoListField) Generate(rng *rand.Rand, size int) reflect.Value {
	switch rng.Int() % 3 {
	case 0:
		return reflect.ValueOf(RepoListField(RepoListFieldRepos))
	case 1:
		return reflect.ValueOf(RepoListField(RepoListFieldMinimal))
	default:
		return reflect.ValueOf(RepoListField(RepoListFieldReposMap))
	}
}

func gen[T any](sample T, r *rand.Rand) T {
	var t T
	v, _ := quick.Value(reflect.TypeOf(t), r)
	return v.Interface().(T)
}

// This is a real search result that is intended to be a reasonable representative
// for serialization benchmarks.
var (
	//go:embed testdata/search_result_1.pb
	exampleSearchResultBytes []byte

	// The proto struct representation of the search result
	exampleSearchResultProto = func() *v1.SearchResponse {
		sr := new(v1.SearchResponse)
		err := proto.Unmarshal(exampleSearchResultBytes, sr)
		if err != nil {
			panic(err)
		}
		return sr
	}()

	// The non-proto struct representation of the search result
	exampleSearchResultGo = SearchResultFromProto(exampleSearchResultProto)
)

// The number of times to do a roundtrip for each benchmark.
// A high number favors gob more because it amortizes the cost
// of encoding the type
const count = 1000

func BenchmarkGobRoundtrip(b *testing.B) {
	for i := 0; i < b.N; i++ {
		var buf bytes.Buffer
		enc := gob.NewEncoder(&buf)

		for i := 0; i < count; i++ {
			err := enc.Encode(exampleSearchResultGo)
			if err != nil {
				panic(err)
			}

		}

		dec := gob.NewDecoder(&buf)
		for i := 0; i < count; i++ {
			var res SearchResult
			err := dec.Decode(&res)
			if err != nil {
				panic(err)
			}
		}
	}
}

func BenchmarkProtoRoundtrip(b *testing.B) {
	for i := 0; i < b.N; i++ {
		buffers := make([][]byte, 0, count)
		for i := 0; i < count; i++ {
			buf, err := proto.Marshal(exampleSearchResultProto)
			if err != nil {
				b.Fatal(err)
			}
			buffers = append(buffers, buf)
		}

		for _, buf := range buffers {
			res := new(v1.SearchResponse)
			err := proto.Unmarshal(buf, res)
			if err != nil {
				b.Fatal(err)
			}
		}
	}
}
