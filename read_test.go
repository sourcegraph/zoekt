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
	"encoding/json"
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
	var want []struct {
		FormatVersion  int
		FeatureVersion int
		FileMatches    [][]FileMatch
	}

	golden := "testdata/golden/TestReadSearch.golden"
	f, err := os.Open(golden)
	if err != nil {
		t.Fatalf("error opening golden file %s", golden)
	}
	defer f.Close()

	if err := json.NewDecoder(f).Decode(&want); err != nil {
		t.Fatalf("error reading golden file %s:\n %v", "testdata/golden/TestReadSearch.golden", err)
	}

	qs := []query.Q{
		&query.Substring{Pattern: "func main", Content: true},
		&query.Regexp{Regexp: mustParseRE("^package"), Content: true},
		&query.Symbol{&query.Substring{Pattern: "num"}},
		&query.Symbol{&query.Regexp{Regexp: mustParseRE("sage$")}},
	}

	shards := []string{"ctagsrepo_v16.00000", "repo_v15.00000", "repo_v16.00000"}
	for i, name := range shards {
		shard, err := loadShard("testdata/shards/" + name + ".zoekt")
		if err != nil {
			t.Fatalf("error loading shard %s %v", name, err)
		}

		index, ok := shard.(*indexData)
		if !ok {
			t.Fatalf("expected *indexData for %s", name)
		}

		if index.metaData.IndexFormatVersion != want[i].FormatVersion {
			t.Errorf("got %d index format version, want %d for %s", index.metaData.IndexFormatVersion, want[i].FormatVersion, name)
		}

		if index.metaData.IndexFeatureVersion != want[i].FeatureVersion {
			t.Errorf("got %d index feature version, want %d for %s", index.metaData.IndexFeatureVersion, want[i].FeatureVersion, name)
		}

		for j, q := range qs {
			res, err := shard.Search(context.Background(), q, &SearchOptions{})
			if err != nil {
				t.Fatalf("failed search %s on %s: %v", q, name, err)
			}

			if len(res.Files) != len(want[i].FileMatches[j]) {
				t.Fatalf("got %d file matches for %s on %s, want %d", len(res.Files), q, name, len(want[i].FileMatches[j]))
			}

			if len(want[i].FileMatches[j]) == 0 {
				continue
			}

			got := []FileMatch{{
				FileName:    res.Files[0].FileName,
				Language:    res.Files[0].Language,
				LineMatches: res.Files[0].LineMatches,
			}}

			if !reflect.DeepEqual(got, want[i].FileMatches[j]) {
				t.Errorf("matches for %s on %s\ngot:\n%v\nwant:\n%v", q, name, got, want[i].FileMatches[j])
			}
		}
	}
}
