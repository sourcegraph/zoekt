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
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/grafana/regexp"
)

/*
BenchmarkMinimalRepoListEncodings/slice-8								570		2145665 ns/op		 753790 bytes		 3981 B/op				0 allocs/op
BenchmarkMinimalRepoListEncodings/map-8									360		3337522 ns/op		 740778 bytes	 377777 B/op		13002 allocs/op
*/
func BenchmarkMinimalRepoListEncodings(b *testing.B) {
	size := uint32(13000) // 2021-06-24 rough estimate of number of repos on a replica.

	type Slice struct {
		ID            uint32
		HasSymbols    bool
		Branches      []RepositoryBranch
		IndexTimeUnix int64
	}

	branches := []RepositoryBranch{{Name: "HEAD", Version: strings.Repeat("a", 40)}}
	mapData := make(map[uint32]*MinimalRepoListEntry, size)
	sliceData := make([]Slice, 0, size)
	indexTime := time.Now().Unix()

	for id := uint32(1); id <= size; id++ {
		mapData[id] = &MinimalRepoListEntry{
			HasSymbols:    true,
			Branches:      branches,
			IndexTimeUnix: indexTime,
		}
		sliceData = append(sliceData, Slice{
			ID:            id,
			HasSymbols:    true,
			Branches:      branches,
			IndexTimeUnix: indexTime,
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
	sr := SearchResult{
		Stats:    Stats{},    // 129 bytes
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
		RepoURLs:       nil, // 48 bytes
		RepoEditURLs:   nil, // 48 bytes
		RepoBrowseURLs: nil, // 48 bytes
		LineFragments:  nil, // 48 bytes
	}

	var wantBytes uint64 = 821
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

func TestMatchSize(t *testing.T) {
	cases := []struct {
		v    any
		size int
	}{{
		v:    FileMatch{},
		size: 256,
	}, {
		v:    ChunkMatch{},
		size: 112,
	}, {
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

func TestSearchOptions_String(t *testing.T) {
	// To make sure we don't forget to update the string implementation we use
	// reflection to generate a SearchOptions with every field being non
	// default. We then check that the field name is present in the output.
	opts := SearchOptions{}
	var fieldNames []string
	rv := reflect.ValueOf(&opts).Elem()
	for i := 0; i < rv.NumField(); i++ {
		f := rv.Field(i)
		name := rv.Type().Field(i).Name
		fieldNames = append(fieldNames, name)
		switch f.Kind() {
		case reflect.Bool:
			f.SetBool(true)
		case reflect.Int:
			f.SetInt(1)
		case reflect.Int64:
			f.SetInt(1)
		case reflect.Float64:
			f.SetFloat(1)
		case reflect.Map:
			// Only map is SpanContext
			f.Set(reflect.ValueOf(map[string]string{"key": "value"}))
		default:
			t.Fatalf("add support for %s field (%s)", f.Kind(), name)
		}
	}

	s := opts.String()
	for _, name := range fieldNames {
		found, err := regexp.MatchString("\\b"+regexp.QuoteMeta(name)+"\\b", s)
		if err != nil {
			t.Fatal(err)
		}
		if !found {
			t.Errorf("could not find field %q in string output of SearchOptions:\n%s", name, s)
		}
	}

	webDefaults := SearchOptions{
		MaxWallTime: 10 * time.Second,
	}
	webDefaults.SetDefaults()

	// Now we hand craft a few corner and common cases
	cases := []struct {
		Opts SearchOptions
		Want string
	}{{
		// Empty
		Opts: SearchOptions{},
		Want: "zoekt.SearchOptions{ }",
	}, {
		// healthz options
		Opts: SearchOptions{ShardMaxMatchCount: 1, TotalMaxMatchCount: 1, MaxDocDisplayCount: 1},
		Want: "zoekt.SearchOptions{ ShardMaxMatchCount=1 TotalMaxMatchCount=1 MaxDocDisplayCount=1 }",
	}, {
		// zoekt-webserver defaults
		Opts: webDefaults,
		Want: "zoekt.SearchOptions{ ShardMaxMatchCount=100000 TotalMaxMatchCount=1000000 MaxWallTime=10s }",
	}}

	for _, tc := range cases {
		got := tc.Opts.String()
		if got != tc.Want {
			t.Errorf("unexpected String for %#v:\ngot:  %s\nwant: %s", tc.Opts, got, tc.Want)
		}
	}
}

func TestRepositoryMergeMutable(t *testing.T) {
	a := Repository{
		ID:   0,
		Name: "name",
		Branches: []RepositoryBranch{
			{
				Name:    "branchName",
				Version: "branchVersion",
			},
		},
		RawConfig:            nil,
		URL:                  "url",
		CommitURLTemplate:    "commitUrlTemplate",
		FileURLTemplate:      "fileUrlTemplate",
		LineFragmentTemplate: "lineFragmentTemplate",
	}

	t.Run("different ID", func(t *testing.T) {
		b := a
		b.ID = 1
		mutated, err := a.MergeMutable(&b)
		if err == nil {
			t.Fatalf("want err, got mutated=%t", mutated)
		}
	})
	t.Run("different Name", func(t *testing.T) {
		b := a
		b.Name = "otherName"
		mutated, err := a.MergeMutable(&b)
		if err == nil {
			t.Fatalf("want err, got mutated=%t", mutated)
		}
	})
	t.Run("different Branches", func(t *testing.T) {
		b := a
		b.Branches = []RepositoryBranch{
			{
				Name:    "otherBranchName",
				Version: "branchVersion",
			},
		}
		mutated, err := a.MergeMutable(&b)
		if err == nil {
			t.Fatalf("want err, got mutated=%t", mutated)
		}
	})
	t.Run("different RawConfig", func(t *testing.T) {
		b := a
		b.RawConfig = map[string]string{"foo": "bar"}
		mutated, err := a.MergeMutable(&b)
		if err != nil {
			t.Fatalf("got err %v", err)
		}
		if !mutated {
			t.Fatalf("want mutated=true, got false")
		}
		if !reflect.DeepEqual(a.RawConfig, b.RawConfig) {
			t.Fatalf("got different RawConfig, %v vs %v", a.RawConfig, b.RawConfig)
		}
	})
	t.Run("different URL", func(t *testing.T) {
		b := a
		b.URL = "otherURL"
		mutated, err := a.MergeMutable(&b)
		if err != nil {
			t.Fatalf("got err %v", err)
		}
		if !mutated {
			t.Fatalf("want mutated=true, got false")
		}
		if a.URL != b.URL {
			t.Fatalf("got different URL, %s vs %s", a.URL, b.URL)
		}
	})
	t.Run("different CommitURLTemplate", func(t *testing.T) {
		b := a
		b.CommitURLTemplate = "otherCommitUrlTemplate"
		mutated, err := a.MergeMutable(&b)
		if err != nil {
			t.Fatalf("got err %v", err)
		}
		if !mutated {
			t.Fatalf("want mutated=true, got false")
		}
		if a.CommitURLTemplate != b.CommitURLTemplate {
			t.Fatalf("got different CommitURLTemplate, %s vs %s", a.CommitURLTemplate, b.CommitURLTemplate)
		}
	})
	t.Run("different FileURLTemplate", func(t *testing.T) {
		b := a
		b.FileURLTemplate = "otherFileUrlTemplate"
		mutated, err := a.MergeMutable(&b)
		if err != nil {
			t.Fatalf("got err %v", err)
		}
		if !mutated {
			t.Fatalf("want mutated=true, got false")
		}
		if a.FileURLTemplate != b.FileURLTemplate {
			t.Fatalf("got different FileURLTemplate, %s vs %s", a.FileURLTemplate, b.FileURLTemplate)
		}
	})
	t.Run("different LineFragmentTemplate", func(t *testing.T) {
		b := a
		b.LineFragmentTemplate = "otherLineFragmentTemplate"
		mutated, err := a.MergeMutable(&b)
		if err != nil {
			t.Fatalf("got err %v", err)
		}
		if !mutated {
			t.Fatalf("want mutated=true, got false")
		}
		if a.LineFragmentTemplate != b.LineFragmentTemplate {
			t.Fatalf("got different LineFragmentTemplate, %s vs %s", a.LineFragmentTemplate, b.LineFragmentTemplate)
		}
	})
	t.Run("all same", func(t *testing.T) {
		b := a
		mutated, err := a.MergeMutable(&b)
		if err != nil {
			t.Fatalf("got err %v", err)
		}
		if mutated {
			t.Fatalf("want mutated=false, got true")
		}
		if !reflect.DeepEqual(a, b) {
			t.Fatalf("got different Repository, %v vs %v", a, b)
		}
	})
}

func TestMonthsSince1970(t *testing.T) {
	tests := []struct {
		name     string
		input    time.Time
		expected uint16
	}{
		{"Before 1970", time.Date(1950, 12, 31, 0, 0, 0, 0, time.UTC), 0},
		{"Unix 0", time.Date(1970, 1, 1, 0, 0, 0, 0, time.UTC), 0},
		{"Feb 1970", time.Date(1970, 2, 1, 0, 0, 0, 0, time.UTC), 1},
		{"Year 1989", time.Date(1989, 12, 13, 0, 0, 0, 0, time.UTC), 239},
		{"Sep 2024", time.Date(2024, 9, 20, 0, 0, 0, 0, time.UTC), 656},
		{"Oct 2024", time.Date(2024, 10, 20, 0, 0, 0, 0, time.UTC), 657},
		{"Apr 7431", time.Date(7431, 4, 1, 0, 0, 0, 0, time.UTC), 65535},
		{"9999", time.Date(9999, 0, 0, 0, 0, 0, 0, time.UTC), 65535},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := monthsSince1970(tt.input)
			if result != tt.expected {
				t.Errorf("expected %d, got %d", tt.expected, result)
			}
		})
	}
}
