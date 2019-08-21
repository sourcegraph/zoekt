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

package zoekt

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"reflect"
	"testing"

	"github.com/google/zoekt/query"
)

func TestReadWrite(t *testing.T) {
	b, err := NewIndexBuilder(nil)
	if err != nil {
		t.Fatalf("NewIndexBuilder: %v", err)
	}

	if err := b.AddFile("filename", []byte("abcde")); err != nil {
		t.Fatalf("AddFile: %v", err)
	}

	var buf bytes.Buffer
	b.Write(&buf)
	f := &memSeeker{buf.Bytes()}

	r := reader{r: f}

	var toc indexTOC
	err = r.readTOC(&toc)

	if err != nil {
		t.Errorf("got read error %v", err)
	}
	if toc.fileContents.data.sz != 5 {
		t.Errorf("got contents size %d, want 5", toc.fileContents.data.sz)
	}

	data, err := r.readIndexData(&toc)
	if err != nil {
		t.Fatalf("readIndexData: %v", err)
	}
	if got := data.fileName(0); string(got) != "filename" {
		t.Errorf("got filename %q, want %q", got, "filename")
	}

	if len(data.ngrams) != 3 {
		t.Fatalf("got ngrams %v, want 3 ngrams", data.ngrams)
	}

	if _, ok := data.ngrams[stringToNGram("bcq")]; ok {
		t.Errorf("found ngram bcd in %v", data.ngrams)
	}
}

func TestReadWriteNames(t *testing.T) {
	b, err := NewIndexBuilder(nil)
	if err != nil {
		t.Fatalf("NewIndexBuilder: %v", err)
	}

	if err := b.AddFile("abCd", []byte("")); err != nil {
		t.Fatalf("AddFile: %v", err)
	}

	var buf bytes.Buffer
	b.Write(&buf)
	f := &memSeeker{buf.Bytes()}

	r := reader{r: f}

	var toc indexTOC
	if err := r.readTOC(&toc); err != nil {
		t.Errorf("got read error %v", err)
	}
	if toc.fileNames.data.sz != 4 {
		t.Errorf("got contents size %d, want 4", toc.fileNames.data.sz)
	}

	data, err := r.readIndexData(&toc)
	if err != nil {
		t.Fatalf("readIndexData: %v", err)
	}
	if !reflect.DeepEqual([]uint32{0, 4}, data.fileNameIndex) {
		t.Errorf("got index %v, want {0,4}", data.fileNameIndex)
	}
	if got := data.fileNameNgrams[stringToNGram("bCd")]; !reflect.DeepEqual(got, []uint32{1}) {
		t.Errorf("got trigram bcd at bits %v, want sz 2", data.fileNameNgrams)
	}
}

func loadShard(fn string) (Searcher, error) {
	f, err := os.Open(fn)
	if err != nil {
		return nil, err
	}

	iFile, err := NewIndexFile(f)
	if err != nil {
		return nil, err
	}
	s, err := NewSearcher(iFile)
	if err != nil {
		iFile.Close()
		return nil, fmt.Errorf("NewSearcher(%s): %v", fn, err)
	}

	return s, nil
}

func TestReadSearch(t *testing.T) {
	tcs := []struct {
		file           string
		formatVersion  int
		featureVersion int
		fileMatches    [][]FileMatch
	}{
		{
			"ctags_zoekt_v16.00000.zoekt",
			16, 9,
			[][]FileMatch{{{
				FileName: "cmd/zoekt/main.go",
				Language: "go",
				LineMatches: []LineMatch{{
					Line:          []byte("func main() {"),
					LineStart:     1472,
					LineEnd:       1485,
					LineNumber:    63,
					LineFragments: []LineFragmentMatch{{0, 1472, 9, nil}},
				}},
			}}, {{
				FileName: "cmd/zoekt/main.go",
				Language: "go",
				LineMatches: []LineMatch{{
					Line:          []byte("package main"),
					LineStart:     609,
					LineEnd:       621,
					LineNumber:    15,
					LineFragments: []LineFragmentMatch{{0, 609, 7, nil}},
				}},
			}}, {{
				FileName: "cmd/zoekt/main.go",
				Language: "go",
				LineMatches: []LineMatch{{
					Line:          []byte("func loadShard(fn string) (zoekt.Searcher, error) {"),
					LineStart:     1135,
					LineEnd:       1186,
					LineNumber:    44,
					LineFragments: []LineFragmentMatch{{9, 1144, 5, &Symbol{"loadShard", "func", "main", "package"}}},
				}},
			}}, {{
				FileName: "cmd/zoekt/main.go",
				Language: "go",
				LineMatches: []LineMatch{{
					Line:          []byte("func loadShard(fn string) (zoekt.Searcher, error) {"),
					LineStart:     1135,
					LineEnd:       1186,
					LineNumber:    44,
					LineFragments: []LineFragmentMatch{{9, 1144, 5, &Symbol{"loadShard", "func", "main", "package"}}},
				}},
			}}},
		},
		{
			"zoekt_v15.00000.zoekt",
			15, 8,
			[][]FileMatch{{{
				FileName: "cmd/zoekt/main.go",
				Language: "",
				LineMatches: []LineMatch{{
					Line:          []byte("func main() {"),
					LineStart:     1472,
					LineEnd:       1485,
					LineNumber:    63,
					LineFragments: []LineFragmentMatch{{0, 1472, 9, nil}},
				}},
			}}, {{
				FileName: "cmd/zoekt/main.go",
				LineMatches: []LineMatch{{
					Line:          []byte("package main"),
					LineStart:     609,
					LineEnd:       621,
					LineNumber:    15,
					LineFragments: []LineFragmentMatch{{0, 609, 7, nil}},
				}},
			}}, {}, {}},
		},
		{
			"zoekt_v16.00000.zoekt",
			16, 9,
			[][]FileMatch{{{
				FileName: "cmd/zoekt/main.go",
				Language: "",
				LineMatches: []LineMatch{{
					Line:          []byte("func main() {"),
					LineStart:     1472,
					LineEnd:       1485,
					LineNumber:    63,
					LineFragments: []LineFragmentMatch{{0, 1472, 9, nil}},
				}},
			}}, {{
				FileName: "cmd/zoekt/main.go",
				LineMatches: []LineMatch{{
					Line:          []byte("package main"),
					LineStart:     609,
					LineEnd:       621,
					LineNumber:    15,
					LineFragments: []LineFragmentMatch{{0, 609, 7, nil}},
				}},
			}}, {}, {}},
		},
	}

	qs := []query.Q{
		query.NewAnd(&query.Substring{Pattern: "func main", Content: true}, &query.Substring{Pattern: "zoekt/main.go", FileName: true}),
		query.NewAnd(&query.Regexp{Regexp: mustParseRE("^package"), Content: true}, &query.Substring{Pattern: "zoekt/main.go", FileName: true}),
		query.NewAnd(&query.Symbol{&query.Substring{Pattern: "shard"}}, &query.Substring{Pattern: "zoekt/main.go", FileName: true}),
		query.NewAnd(&query.Symbol{&query.Regexp{Regexp: mustParseRE("shard$")}}, &query.Substring{Pattern: "zoekt/main.go", FileName: true}),
	}

	for _, tc := range tcs {
		shard, err := loadShard("test-index/" + tc.file)
		if err != nil {
			t.Fatalf("failed loading shard %s %v", tc.file, err)
		}

		index, ok := shard.(*indexData)
		if !ok {
			t.Fatalf("expected *indexData for %s", tc.file)
		}

		if index.metaData.IndexFormatVersion != tc.formatVersion {
			t.Errorf("got %d index format version, want %d for %s", index.metaData.IndexFormatVersion, tc.formatVersion, tc.file)
		}

		if index.metaData.IndexFeatureVersion != tc.featureVersion {
			t.Errorf("got %d index feature version, want %d for %s", index.metaData.IndexFeatureVersion, tc.featureVersion, tc.file)
		}

		for i, q := range qs {
			res, err := shard.Search(context.Background(), q, &SearchOptions{})
			if err != nil {
				t.Fatalf("failed search %s on %s: %v", q, tc.file, err)
			}

			if len(res.Files) != len(tc.fileMatches[i]) {
				t.Fatalf("got %d file matches for %s on %s, want %d", len(res.Files), q, tc.file, len(tc.fileMatches[i]))
			}

			if len(tc.fileMatches[i]) == 0 {
				continue
			}

			want := tc.fileMatches[i][0]
			got := res.Files[0]

			if got.FileName != want.FileName {
				t.Errorf("got %s file name for %s on %s, want %s", got.FileName, q, tc.file, want.FileName)
			}

			if got.Language != want.Language {
				t.Errorf("got %s language for %s on %s, want %s", got.Language, q, tc.file, want.Language)
			}

			for i, _ := range got.LineMatches {
				got.LineMatches[i].Score = 0
			}

			if !reflect.DeepEqual(got.LineMatches, want.LineMatches) {
				t.Errorf("line matches for %s on %s\ngot:\n%v\nwant:\n%v", q, tc.file, got.LineMatches, want.LineMatches)
			}
		}
	}
}
