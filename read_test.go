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
	"os"
	"path"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"testing/quick"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/sourcegraph/zoekt/query"
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

	contentNgrams := data.contentNgrams.DumpMap()
	if len(contentNgrams) != 3 {
		t.Fatalf("got ngrams %v, want 3 ngrams", contentNgrams)
	}

	if sec := data.contentNgrams.Get(stringToNGram("bcq")); sec.sz > 0 {
		t.Errorf("found ngram bcq (%v) in %v", uint64(stringToNGram("bcq")), contentNgrams)
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

	gotSec := data.fileNameNgrams.Get(stringToNGram("bCd"))
	if err != nil {
		t.Fatalf("fileNameNgrams.GetBlob: %v", err)
	}

	if !reflect.DeepEqual(buf.Bytes()[gotSec.off:gotSec.off+gotSec.sz], []byte{1}) {
		t.Errorf("got trigram bcd at bits %v, want sz 2", data.fileNameNgrams)
	}
}

func TestGet(t *testing.T) {
	b, err := NewIndexBuilder(nil)
	if err != nil {
		t.Fatalf("NewIndexBuilder: %v", err)
	}

	if err := b.AddFile("file_name", []byte("aaa bbbaaa")); err != nil {
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

	id, err := r.readIndexData(&toc)
	if err != nil {
		t.Fatalf("readIndexData: %v", err)
	}

	var off uint32 = 96

	cases := []struct {
		ng              string
		wantPostingList simpleSection
	}{
		{
			ng:              " bb",
			wantPostingList: simpleSection{off: off, sz: 1},
		},
		{
			ng:              "a b",
			wantPostingList: simpleSection{off: off + 1, sz: 1},
		},
		{
			ng:              "aa ",
			wantPostingList: simpleSection{off: off + 2, sz: 1},
		},
		{
			ng:              "aaa",
			wantPostingList: simpleSection{off: off + 3, sz: 2},
		},
		{
			ng:              "baa",
			wantPostingList: simpleSection{off: off + 5, sz: 1},
		},
		{
			ng:              "bba",
			wantPostingList: simpleSection{off: off + 6, sz: 1},
		},
		{
			ng:              "bbb",
			wantPostingList: simpleSection{off: off + 7, sz: 1},
		},
	}

	for _, tt := range cases {
		t.Run(tt.ng, func(t *testing.T) {
			havePostingList := id.contentNgrams.Get(stringToNGram(tt.ng))
			if !reflect.DeepEqual(tt.wantPostingList, havePostingList) {
				t.Fatalf("\nwant:%+v\ngot: %+v", tt.wantPostingList, havePostingList)
			}
		})
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
			} else if err := os.WriteFile(golden, raw, 0o644); err != nil {
				t.Errorf("failed writing search results for %s during updating: %v", name, err)
				continue
			}
		}

		var want out
		if buf, err := os.ReadFile(golden); err != nil {
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

			if d := cmp.Diff(want.FileMatches[j], res.Files); d != "" {
				t.Errorf("matches for %s on %s (-want +got)\n%s", q, name, d)
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

		err = os.WriteFile(outname, buf.Bytes(), 0o644)
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

				contentNgrams := data.contentNgrams.DumpMap()
				if len(data.contentNgrams.DumpMap()) != 3 {
					t.Fatalf("got ngrams %v, want 3 ngrams", contentNgrams)
				}

				if sec := data.contentNgrams.Get(stringToNGram("bcq")); sec.sz > 0 {
					t.Errorf("found ngram bcd in %v", contentNgrams)
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

func TestEncodeRanks(t *testing.T) {
	quick.Check(func(ranks [][]float64) bool {
		buf := bytes.Buffer{}
		w := &writer{w: &buf}

		if err := encodeRanks(w, ranks); err != nil {
			return false
		}

		// In case all rank vectors are empty, IE {{}, {}, ...}, we won't write anything
		// to w and gob decode will decode this as "nil", which will fail the
		// comparison even with cmpopts.EquateEmpty().
		if w.off == 0 {
			return true
		}

		d := &indexData{}
		if err := decodeRanks(buf.Bytes(), &d.ranks); err != nil {
			t.Fatal(err)
		}

		if d := cmp.Diff(ranks, d.ranks, cmpopts.EquateEmpty()); d != "" {
			t.Fatalf("-want, +got:\n%s\n", d)
		}

		return true
	}, nil)
}

func BenchmarkReadMetadata(b *testing.B) {
	file, err := os.Open("testdata/benchmark/zoekt_v16.00000.zoekt")
	if err != nil {
		b.Fatalf("Failed to open test file: %v", err)
	}
	defer file.Close()

	indexFile, err := NewIndexFile(file)
	if err != nil {
		b.Fatalf("could not open index: %v", err)
	}

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		repos, metadata, err := ReadMetadata(indexFile)
		if err != nil {
			b.Fatalf("ReadMetadata failed: %v", err)
		}
		if len(repos) != 1 {
			b.Fatalf("expected 1 repository")
		}
		if metadata == nil {
			b.Fatalf("expected non-nil metadata")
		}
	}
}
