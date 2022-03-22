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
	"flag"
	"fmt"
	"io/fs"
	"io/ioutil"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/zoekt/query"
)

var update = flag.Bool("update", false, "update golden files")

func TestReadWrite(t *testing.T) {
	b, err := NewIndexBuilder(nil)
	if err != nil {
		t.Fatalf("NewIndexBuilder: %v", err)
	}

	if err := b.AddFile("filename", []byte("abcde")); err != nil {
		t.Fatalf("AddFile: %v", err)
	}

	var buf bytes.Buffer
	if err := b.Write(&buf); err != nil {
		t.Fatal(err)
	}
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

	if len(data.ngrams.DumpMap()) != 3 {
		t.Fatalf("got ngrams %v, want 3 ngrams", data.ngrams)
	}

	if sec := data.ngrams.Get(stringToNGram("bcq")); sec.sz > 0 {
		t.Errorf("found ngram bcq (%v) in %v", uint64(stringToNGram("bcq")), data.ngrams)
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
	if err := b.Write(&buf); err != nil {
		t.Fatal(err)
	}
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
	if got := data.fileNameNgrams[stringToNGram("bCd")]; !reflect.DeepEqual(got, []byte{1}) {
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
	type out struct {
		FormatVersion  int
		FeatureVersion int
		FileMatches    [][]FileMatch
	}

	qs := []query.Q{
		&query.Substring{Pattern: "func main", Content: true},
		&query.Regexp{Regexp: mustParseRE("^package"), Content: true},
		&query.Symbol{Expr: &query.Substring{Pattern: "num"}},
		&query.Symbol{Expr: &query.Regexp{Regexp: mustParseRE("sage$")}},
	}

	shards, err := filepath.Glob("testdata/shards/*.zoekt")
	if err != nil {
		t.Fatal(err)
	}

	for _, path := range shards {
		name := filepath.Base(path)
		name = strings.TrimSuffix(name, ".zoekt")

		shard, err := loadShard(path)
		if err != nil {
			t.Fatalf("error loading shard %s %v", name, err)
		}

		index, ok := shard.(*indexData)
		if !ok {
			t.Fatalf("expected *indexData for %s", name)
		}

		golden := "testdata/golden/TestReadSearch/" + name + ".golden"

		if *update {
			got := out{
				FormatVersion:  index.metaData.IndexFormatVersion,
				FeatureVersion: index.metaData.IndexFeatureVersion,
			}
			for _, q := range qs {
				res, err := shard.Search(context.Background(), q, &SearchOptions{})
				if err != nil {
					t.Fatalf("failed search %s on %s during updating: %v", q, name, err)
				}
				got.FileMatches = append(got.FileMatches, res.Files)
			}

			if raw, err := json.MarshalIndent(got, "", "  "); err != nil {
				t.Errorf("failed marshalling search results for %s during updating: %v", name, err)
				continue
			} else if err := ioutil.WriteFile(golden, raw, 0644); err != nil {
				t.Errorf("failed writing search results for %s during updating: %v", name, err)
				continue
			}
		}

		var want out
		if buf, err := ioutil.ReadFile(golden); err != nil {
			t.Fatalf("failed reading search results for %s: %v", name, err)
		} else if err := json.Unmarshal(buf, &want); err != nil {
			t.Fatalf("failed unmarshalling search results for %s: %v", name, err)
		}

		if index.metaData.IndexFormatVersion != want.FormatVersion {
			t.Errorf("got %d index format version, want %d for %s", index.metaData.IndexFormatVersion, want.FormatVersion, name)
		}

		if index.metaData.IndexFeatureVersion != want.FeatureVersion {
			t.Errorf("got %d index feature version, want %d for %s", index.metaData.IndexFeatureVersion, want.FeatureVersion, name)
		}

		for j, q := range qs {
			res, err := shard.Search(context.Background(), q, &SearchOptions{})
			if err != nil {
				t.Fatalf("failed search %s on %s: %v", q, name, err)
			}

			if len(res.Files) != len(want.FileMatches[j]) {
				t.Fatalf("got %d file matches for %s on %s, want %d", len(res.Files), q, name, len(want.FileMatches[j]))
			}

			if len(want.FileMatches[j]) == 0 {
				continue
			}

			if d := cmp.Diff(res.Files, want.FileMatches[j]); d != "" {
				t.Errorf("matches for %s on %s\n%s", q, name, d)
			}
		}
	}
}

func TestEncodeRawConfig(t *testing.T) {
	mustParse := func(s string) uint8 {
		i, err := strconv.ParseInt(s, 2, 8)
		if err != nil {
			t.Fatalf("failed to parse %s", s)
		}
		return uint8(i)
	}

	cases := []struct {
		rawConfig map[string]string
		want      string
	}{
		{
			rawConfig: map[string]string{"public": "1"},
			want:      "101001",
		},
		{
			rawConfig: map[string]string{"fork": "1"},
			want:      "100110",
		},
		{
			rawConfig: map[string]string{"public": "1", "fork": "1"},
			want:      "100101",
		},
		{
			rawConfig: map[string]string{"public": "1", "fork": "1", "archived": "1"},
			want:      "010101",
		},
		{
			rawConfig: map[string]string{},
			want:      "101010",
		},
	}
	for _, c := range cases {
		t.Run(c.want, func(t *testing.T) {
			if got := encodeRawConfig(c.rawConfig); got != mustParse(c.want) {
				t.Fatalf("want %s, got %s", c.want, strconv.FormatInt(int64(got), 2))
			}
		})
	}
}

func TestBackwardsCompat(t *testing.T) {
	if *update {
		b, err := NewIndexBuilder(nil)
		if err != nil {
			t.Fatalf("NewIndexBuilder: %v", err)
		}

		if err := b.AddFile("filename", []byte("abcde")); err != nil {
			t.Fatalf("AddFile: %v", err)
		}

		var buf bytes.Buffer
		if err := b.Write(&buf); err != nil {
			t.Fatal(err)
		}

		outname := fmt.Sprintf("testdata/backcompat/new_v%d.%05d.zoekt", IndexFormatVersion, 0)
		t.Log("writing new file", outname)

		err = os.WriteFile(outname, buf.Bytes(), 0644)
		if err != nil {
			t.Fatalf("Creating output file: %v", err)
		}
	}

	compatibleFiles, err := fs.Glob(os.DirFS("."), "testdata/backcompat/*.zoekt")
	if err != nil {
		t.Fatalf("fs.Glob: %v", err)
	}

	for _, fname := range compatibleFiles {
		t.Run(path.Base(fname),
			func(t *testing.T) {
				f, err := os.Open(fname)
				if err != nil {
					t.Fatal("os.Open", err)
				}
				idx, err := NewIndexFile(f)
				if err != nil {
					t.Fatal("NewIndexFile", err)
				}
				r := reader{r: idx}

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

				if len(data.ngrams.DumpMap()) != 3 {
					t.Fatalf("got ngrams %v, want 3 ngrams", data.ngrams)
				}

				if sec := data.ngrams.Get(stringToNGram("bcq")); sec.sz > 0 {
					t.Errorf("found ngram bcd in %v", data.ngrams)
				}
			},
		)
	}
}

func TestBackfillIDIsDeterministic(t *testing.T) {
	repo := "github.com/a/b"
	have1 := backfillID(repo)
	have2 := backfillID(repo)

	if have1 != have2 {
		t.Fatalf("%s != %s ", have1, have2)
	}
}
