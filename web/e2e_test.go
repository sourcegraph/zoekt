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

package web

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/sourcegraph/zoekt/index"

	"github.com/sourcegraph/zoekt"
	"github.com/sourcegraph/zoekt/query"
)

// TODO(hanwen): cut & paste from ../ . Should create internal test
// util package.
type memSeeker struct {
	data []byte
}

func (s *memSeeker) Close() {}
func (s *memSeeker) Read(off, sz uint32) ([]byte, error) {
	return s.data[off : off+sz], nil
}

func (s *memSeeker) Size() (uint32, error) {
	return uint32(len(s.data)), nil
}

func (s *memSeeker) Name() string {
	return "memSeeker"
}

func searcherForTest(t *testing.T, b *index.ShardBuilder) zoekt.Streamer {
	var buf bytes.Buffer
	if err := b.Write(&buf); err != nil {
		t.Fatal(err)
	}
	f := &memSeeker{buf.Bytes()}

	searcher, err := index.NewSearcher(f)
	if err != nil {
		t.Fatalf("NewSearcher: %v", err)
	}

	return adapter{Searcher: searcher}
}

type adapter struct {
	zoekt.Searcher
}

func (a adapter) StreamSearch(ctx context.Context, q query.Q, opts *zoekt.SearchOptions, sender zoekt.Sender) (err error) {
	sr, err := a.Searcher.Search(ctx, q, opts)
	if err != nil {
		return err
	}
	sender.Send(sr)
	return nil
}

func TestBasic(t *testing.T) {
	b, err := index.NewShardBuilder(&zoekt.Repository{
		Name:                 "name",
		URL:                  "repo-url",
		CommitURLTemplate:    `{{ URLJoinPath "https://github.com/org/repo/commit/" .Version}}`,
		FileURLTemplate:      `{{ URLJoinPath "https://github.com/org/repo/blob/" .Version .Path}}`,
		LineFragmentTemplate: "#L{{.LineNumber}}",
		Branches:             []zoekt.RepositoryBranch{{Name: "master", Version: "1234"}},
	})
	if err != nil {
		t.Fatalf("NewShardBuilder: %v", err)
	}
	if err := b.Add(index.Document{
		// use a name which requires correct escaping. https://github.com/sourcegraph/zoekt/issues/807
		Name:    "foo/bar+baz",
		Content: []byte("to carry water in the no later bla"),
		// --------------0123456789012345678901234567890123
		// --------------0         1         2         3
		Branches: []string{"master"},
	}); err != nil {
		t.Fatalf("Add: %v", err)
	}

	s := searcherForTest(t, b)
	srv := Server{
		Searcher: s,
		Top:      Top,
		HTML:     true,
	}

	mux, err := NewMux(&srv)
	if err != nil {
		t.Fatalf("NewMux: %v", err)
	}

	ts := httptest.NewServer(mux)
	defer ts.Close()

	nowStr := time.Now().UTC().Format("Jan 02, 2006 15:04")
	for req, needles := range map[string][]string{
		"/": {"from 1 repositories"},
		"/search?q=water": {
			`href="https://github.com/org/repo/blob/1234/foo/bar%2Bbaz"`,
			"carry <b>water</b>",
		},
		"/search?q=r:": {
			"1234\">master",
			"Found 1 repositories",
			nowStr,
			"repo-url\">name",
			"1 files (45B)",
		},
		"/search?q=magic": {
			`value=magic`,
		},
		"/search?q=foo+type:file": {
			`value=foo`,
		},
		"/robots.txt": {
			"disallow: /search",
		},
	} {
		checkNeedles(t, ts, req, needles)
	}
}

func TestPrint(t *testing.T) {
	b, err := index.NewShardBuilder(&zoekt.Repository{
		Name:                 "name",
		URL:                  "repo-url",
		CommitURLTemplate:    "{{.Version}}",
		FileURLTemplate:      "file-url",
		LineFragmentTemplate: "line",
		Branches:             []zoekt.RepositoryBranch{{Name: "master", Version: "1234"}},
	})
	if err != nil {
		t.Fatalf("NewShardBuilder: %v", err)
	}
	if err := b.Add(index.Document{
		Name:     "f2",
		Content:  []byte("to carry water in the no later bla"),
		Branches: []string{"master"},
	}); err != nil {
		t.Fatalf("Add: %v", err)
	}

	if err := b.Add(index.Document{
		Name:     "dir/f2",
		Content:  []byte("blabla"),
		Branches: []string{"master"},
	}); err != nil {
		t.Fatalf("Add: %v", err)
	}

	s := searcherForTest(t, b)
	srv := Server{
		Searcher: s,
		Top:      Top,
		HTML:     true,
		Print:    true,
	}

	mux, err := NewMux(&srv)
	if err != nil {
		t.Fatalf("NewMux: %v", err)
	}

	ts := httptest.NewServer(mux)
	defer ts.Close()

	for req, needles := range map[string][]string{
		"/print?q=bla&r=name&f=f2": {
			`pre id="l1" class="inline-pre"><span class="noselect"><a href="#l1">`,
		},
	} {
		checkNeedles(t, ts, req, needles)
	}
}

func TestPrintDefault(t *testing.T) {
	b, err := index.NewShardBuilder(&zoekt.Repository{
		Name:     "name",
		URL:      "repo-url",
		Branches: []zoekt.RepositoryBranch{{Name: "master", Version: "1234"}},
	})
	if err != nil {
		t.Fatalf("NewShardBuilder: %v", err)
	}
	if err := b.Add(index.Document{
		Name:     "f2",
		Content:  []byte("to carry water in the no later bla"),
		Branches: []string{"master"},
	}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	s := searcherForTest(t, b)
	srv := Server{
		Searcher: s,
		Top:      Top,
		HTML:     true,
	}

	mux, err := NewMux(&srv)
	if err != nil {
		t.Fatalf("NewMux: %v", err)
	}

	ts := httptest.NewServer(mux)
	defer ts.Close()

	for req, needles := range map[string][]string{
		"/search?q=water": {
			`href="print?`,
		},
	} {
		checkNeedles(t, ts, req, needles)
	}
}

func checkNeedles(t *testing.T, ts *httptest.Server, req string, needles []string) {
	res, err := http.Get(ts.URL + req)
	if err != nil {
		t.Fatal(err)
	}
	resultBytes, err := io.ReadAll(res.Body)
	res.Body.Close()
	if err != nil {
		log.Fatal(err)
	}

	result := string(resultBytes)
	for _, want := range needles {
		if !strings.Contains(result, want) {
			t.Errorf("query %q: result did not have %q: %s", req, want, result)
		}
	}
	if notWant := "crashed"; strings.Contains(result, notWant) {
		t.Errorf("result has %q: %s", notWant, result)
	}
	if notWant := "bytes skipped)..."; strings.Contains(result, notWant) {
		t.Errorf("result has %q: %s", notWant, result)
	}
}

type Expectation struct {
	title     string
	fileMatch FileMatch
}

func TestFormatJson(t *testing.T) {
	b, err := index.NewShardBuilder(&zoekt.Repository{
		Name:     "name",
		URL:      "repo-url",
		Branches: []zoekt.RepositoryBranch{{Name: "master", Version: "1234"}},
	})
	if err != nil {
		t.Fatalf("NewShardBuilder: %v", err)
	}
	if err := b.Add(index.Document{
		Name:     "f2",
		Content:  []byte("to carry water in the no later bla"),
		Branches: []string{"master"},
	}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	s := searcherForTest(t, b)
	srv := Server{
		Searcher: s,
		Top:      Top,
		HTML:     true,
	}

	mux, err := NewMux(&srv)
	if err != nil {
		t.Fatalf("NewMux: %v", err)
	}

	ts := httptest.NewServer(mux)
	defer ts.Close()

	expected := Expectation{
		"json basic test",
		FileMatch{
			FileName: "f2",
			Repo:     "name",
			Matches: []Match{
				{
					FileName: "f2",
					LineNum:  1,
					Fragments: []Fragment{
						{
							Pre:   "to carry ",
							Match: "water",
							Post:  " in the no later bla",
						},
					},
				},
			},
		},
	}

	checkResultMatches(t, ts, "/search?q=water&format=json", expected)
}

func TestContextLines(t *testing.T) {
	b, err := index.NewShardBuilder(&zoekt.Repository{
		Name:     "name",
		URL:      "repo-url",
		Branches: []zoekt.RepositoryBranch{{Name: "master", Version: "1234"}},
	})
	if err != nil {
		t.Fatalf("NewShardBuilder: %v", err)
	}
	if err := b.Add(index.Document{
		Name:     "f2",
		Content:  []byte("one line\nsecond snippet\nthird thing\nfourth\nfifth block\nsixth example\nseventh"),
		Branches: []string{"master"},
	}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := b.Add(index.Document{
		Name:     "f3",
		Content:  []byte("\n\n\n\nto carry water in the no later bla\n\n\n\n"),
		Branches: []string{"master"},
	}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := b.Add(index.Document{
		Name:     "f4",
		Content:  []byte("un   \n \n\ttrois\n     \n\nsix\n     "),
		Branches: []string{"master"},
	}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := b.Add(index.Document{
		Name:     "f5",
		Content:  []byte("\ngreen\npastures\n\nhere"),
		Branches: []string{"master"},
	}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	s := searcherForTest(t, b)
	srv := Server{
		Searcher: s,
		Top:      Top,
		HTML:     true,
	}

	mux, err := NewMux(&srv)
	if err != nil {
		t.Fatalf("NewMux: %v", err)
	}

	ts := httptest.NewServer(mux)
	defer ts.Close()

	for req, expected := range map[string]Expectation{
		"/search?q=our&format=json&ctx=0": {
			"no context doesn't return Before or After",
			FileMatch{
				FileName: "f2",
				Repo:     "name",
				Matches: []Match{
					{
						FileName: "f2",
						LineNum:  4,
						Fragments: []Fragment{
							{
								Pre:   "f",
								Match: "our",
								Post:  "th\n",
							},
						},
					},
				},
			},
		},
		"/search?q=f:f2&format=json&ctx=2": {
			"filename does not return Before or After",
			FileMatch{
				FileName: "f2",
				Repo:     "name",
				Matches: []Match{
					{
						FileName: "f2",
						LineNum:  0,
						Fragments: []Fragment{
							{
								Match: "f2",
							},
						},
					},
				},
			},
		},
		"/search?q=our&format=json&ctx=2": {
			"context returns Before and After",
			FileMatch{
				FileName: "f2",
				Repo:     "name",
				Matches: []Match{
					{
						FileName: "f2",
						LineNum:  4,
						Fragments: []Fragment{
							{
								Pre:   "f",
								Match: "our",
								Post:  "th\n",
							},
						},
						Before: "second snippet\nthird thing\n",
						After:  "fifth block\nsixth example\n",
					},
				},
			},
		},
		"/search?q=one&format=json&ctx=2": {
			"index at start returns After but no Before",
			FileMatch{
				FileName: "f2",
				Repo:     "name",
				Matches: []Match{
					{
						FileName: "f2",
						LineNum:  1,
						Fragments: []Fragment{
							{
								Pre:   "",
								Match: "one",
								Post:  " line\n",
							},
						},
						After: "second snippet\nthird thing\n",
					},
				},
			},
		},
		"/search?q=seventh&format=json&ctx=2": {
			"index at end returns Before but no After",
			FileMatch{
				FileName: "f2",
				Repo:     "name",
				Matches: []Match{
					{
						FileName: "f2",
						LineNum:  7,
						Fragments: []Fragment{
							{
								Pre:   "",
								Match: "seventh",
								Post:  "",
							},
						},
						Before: "fifth block\nsixth example\n",
					},
				},
			},
		},
		"/search?q=seventh&format=json&ctx=10": {
			"index with large context at end returns whole document",
			FileMatch{
				FileName: "f2",
				Repo:     "name",
				Matches: []Match{
					{
						FileName: "f2",
						LineNum:  7,
						Fragments: []Fragment{
							{
								Pre:   "",
								Match: "seventh",
								Post:  "",
							},
						},
						Before: "one line\nsecond snippet\nthird thing\nfourth\nfifth block\nsixth example\n",
					},
				},
			},
		},
		"/search?q=one&format=json&ctx=10": {
			"index with large context at start returns whole document",
			FileMatch{
				FileName: "f2",
				Repo:     "name",
				Matches: []Match{
					{
						FileName: "f2",
						LineNum:  1,
						Fragments: []Fragment{
							{
								Pre:   "",
								Match: "one",
								Post:  " line\n",
							},
						},
						After: "second snippet\nthird thing\nfourth\nfifth block\nsixth example\nseventh",
					},
				},
			},
		},
		"/search?q=trois&format=json&ctx=2": {
			"context returns whitespaces lines",
			FileMatch{
				FileName: "f4",
				Repo:     "name",
				Matches: []Match{
					{
						FileName: "f4",
						LineNum:  3,
						Fragments: []Fragment{
							{
								Pre:   "\t",
								Match: "trois",
								Post:  "\n",
							},
						},
						Before: "un   \n \n",
						After:  "     \n\n",
					},
				},
			},
		},
		"/search?q=water&format=json&ctx=4": {
			"context returns new lines",
			FileMatch{
				FileName: "f3",
				Repo:     "name",
				Matches: []Match{
					{
						FileName: "f3",
						LineNum:  5,
						Fragments: []Fragment{
							{
								Pre:   "to carry ",
								Match: "water",
								Post:  " in the no later bla\n",
							},
						},
						Before: "\n\n\n\n",
						After:  "\n\n\n",
					},
				},
			},
		},
		"/search?q=pastures&format=json&ctx=1": {
			"context returns empty end line",
			FileMatch{
				FileName: "f5",
				Repo:     "name",
				Matches: []Match{
					{
						FileName: "f5",
						LineNum:  3,
						Fragments: []Fragment{
							{
								Pre:   "",
								Match: "pastures",
								Post:  "\n",
							},
						},
						Before: "green\n",
						After:  "\n",
					},
				},
			},
		},
	} {
		checkResultMatches(t, ts, req, expected)
	}
}

func matchesPartiallyEqual(a, b []Match) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].FileName != b[i].FileName {
			return false
		}
		if a[i].LineNum != b[i].LineNum {
			return false
		}
		if !reflect.DeepEqual(a[i].Before, b[i].Before) {
			return false
		}
		if !reflect.DeepEqual(a[i].After, b[i].After) {
			return false
		}
		if !reflect.DeepEqual(a[i].Fragments, b[i].Fragments) {
			return false
		}
	}
	return true
}

func checkResultMatches(t *testing.T, ts *httptest.Server, req string, expected Expectation) {
	res, err := http.Get(ts.URL + req)
	if err != nil {
		t.Fatal(err)
	}
	resultBytes, err := io.ReadAll(res.Body)
	res.Body.Close()
	if err != nil {
		log.Fatal(err)
	}

	var result ApiSearchResult
	if err := json.Unmarshal(resultBytes, &result); err != nil {
		log.Fatal(err)
	}

	if len(result.Result.FileMatches) != 1 {
		t.Fatalf("Expected search to return just one result but it was %d", len(result.Result.FileMatches))
	}
	match := result.Result.FileMatches[0]
	if match.FileName == expected.fileMatch.FileName && match.Repo == expected.fileMatch.Repo {
		if matchesPartiallyEqual(match.Matches, expected.fileMatch.Matches) {
			return
		}
	}

	t.Errorf(
		"result doesn't match case <%s>:\nDiff:\n %v",
		expected.title,
		cmp.Diff(expected.fileMatch.Matches, result.Result.FileMatches[0].Matches))
}

func TestContextLinesMustBeValid(t *testing.T) {
	b, err := index.NewShardBuilder(&zoekt.Repository{
		Name:     "name",
		URL:      "repo-url",
		Branches: []zoekt.RepositoryBranch{{Name: "master", Version: "1234"}},
	})
	if err != nil {
		t.Fatalf("NewShardBuilder: %v", err)
	}
	if err := b.Add(index.Document{
		Name:     "f2",
		Content:  []byte("to carry water in the no later bla"),
		Branches: []string{"master"},
	}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	s := searcherForTest(t, b)
	srv := Server{
		Searcher: s,
		Top:      Top,
		HTML:     true,
	}

	mux, err := NewMux(&srv)
	if err != nil {
		t.Fatalf("NewMux: %v", err)
	}

	ts := httptest.NewServer(mux)
	defer ts.Close()

	// Don't care about ctx if format is not json
	code := getHttpStatusCode(t, ts, "/search?q=water&ctx=10")
	if code != 200 {
		t.Errorf("Expected 200 but got %v", code)
	}

	// ctx must be a valid integer in the right range.
	for _, want := range []string{"foo", "-1", "20"} {
		code := getHttpStatusCode(t, ts, "/search?q=water&format=json&ctx="+want)
		if code != 418 {
			t.Errorf("Expected 418 but got %v", code)
		}
	}
}

func getHttpStatusCode(t *testing.T, ts *httptest.Server, req string) int {
	res, err := http.Get(ts.URL + req)
	if err != nil {
		t.Fatal(err)
	}
	return res.StatusCode
}

type crashSearcher struct {
	zoekt.Streamer
}

func (s *crashSearcher) Search(ctx context.Context, q query.Q, opts *zoekt.SearchOptions) (*zoekt.SearchResult, error) {
	res := zoekt.SearchResult{}
	res.Stats.Crashes = 1
	return &res, nil
}

func TestCrash(t *testing.T) {
	srv := Server{
		Searcher: &crashSearcher{},
		Top:      Top,
		HTML:     true,
	}

	mux, err := NewMux(&srv)
	if err != nil {
		t.Fatalf("NewMux: %v", err)
	}

	ts := httptest.NewServer(mux)
	defer ts.Close()

	res, err := http.Get(ts.URL + "/search?q=water")
	if err != nil {
		t.Fatal(err)
	}
	resultBytes, err := io.ReadAll(res.Body)
	res.Body.Close()
	if err != nil {
		t.Fatal(err)
	}

	result := string(resultBytes)
	if want := "1 shards crashed"; !strings.Contains(result, want) {
		t.Errorf("result did not have %q: %s", want, result)
	}
}

func TestHostCustomization(t *testing.T) {
	b, err := index.NewShardBuilder(&zoekt.Repository{
		Name: "name",
	})
	if err != nil {
		t.Fatalf("NewShardBuilder: %v", err)
	}
	if err := b.Add(index.Document{
		Name:    "file",
		Content: []byte("bla"),
	}); err != nil {
		t.Fatalf("Add: %v", err)
	}

	s := searcherForTest(t, b)
	srv := Server{
		Searcher: s,
		Top:      Top,
		HTML:     true,
		HostCustomQueries: map[string]string{
			"myproject.io": "r:myproject",
		},
	}

	mux, err := NewMux(&srv)
	if err != nil {
		t.Fatalf("NewMux: %v", err)
	}

	ts := httptest.NewServer(mux)
	defer ts.Close()

	req, err := http.NewRequest("GET", ts.URL, &bytes.Buffer{})
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Host = "myproject.io"
	res, err := (&http.Client{}).Do(req)
	if err != nil {
		t.Fatalf("Do(%v): %v", req, err)
	}
	resultBytes, err := io.ReadAll(res.Body)
	res.Body.Close()
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}

	if got, want := string(resultBytes), "r:myproject"; !strings.Contains(got, want) {
		t.Fatalf("got %s, want substring %q", got, want)
	}
}

func TestDupResult(t *testing.T) {
	b, err := index.NewShardBuilder(&zoekt.Repository{
		Name: "name",
	})
	if err != nil {
		t.Fatalf("NewShardBuilder: %v", err)
	}

	for i := range 2 {
		if err := b.Add(index.Document{
			Name:    fmt.Sprintf("file%d", i),
			Content: []byte("bla"),
		}); err != nil {
			t.Fatalf("Add: %v", err)
		}
	}
	s := searcherForTest(t, b)
	srv := Server{
		Searcher: s,
		Top:      Top,
		HTML:     true,
	}

	mux, err := NewMux(&srv)
	if err != nil {
		t.Fatalf("NewMux: %v", err)
	}

	ts := httptest.NewServer(mux)
	defer ts.Close()

	req, err := http.NewRequest("GET", ts.URL+"/search?q=bla", &bytes.Buffer{})
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	res, err := (&http.Client{}).Do(req)
	if err != nil {
		t.Fatalf("Do(%v): %v", req, err)
	}
	resultBytes, err := io.ReadAll(res.Body)
	res.Body.Close()
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}

	if got, want := string(resultBytes), "Duplicate result"; !strings.Contains(got, want) {
		t.Fatalf("got %s, want substring %q", got, want)
	}
}

func TestTruncateLine(t *testing.T) {
	b, err := index.NewShardBuilder(&zoekt.Repository{
		Name: "name",
	})
	if err != nil {
		t.Fatalf("NewShardBuilder: %v", err)
	}

	largePadding := bytes.Repeat([]byte{'a'}, 100*1000) // 100kb
	if err := b.Add(index.Document{
		Name:    "file",
		Content: append(append(largePadding, []byte("helloworld")...), largePadding...),
	}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	s := searcherForTest(t, b)
	srv := Server{
		Searcher: s,
		Top:      Top,
		HTML:     true,
	}

	mux, err := NewMux(&srv)
	if err != nil {
		t.Fatalf("NewMux: %v", err)
	}

	ts := httptest.NewServer(mux)
	defer ts.Close()

	req, err := http.NewRequest("GET", ts.URL+"/search?q=helloworld", &bytes.Buffer{})
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	res, err := (&http.Client{}).Do(req)
	if err != nil {
		t.Fatalf("Do(%v): %v", req, err)
	}
	resultBytes, err := io.ReadAll(res.Body)
	res.Body.Close()
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}

	if got, want := len(resultBytes)/1000, 10; got > want {
		t.Fatalf("got %dkb response, want <= %dkb", got, want)
	}
	result := string(resultBytes)
	if want := "aa<b>helloworld</b>aa"; !strings.Contains(result, want) {
		t.Fatalf("got %s, want substring %q", result, want)
	}
	if want := "bytes skipped)..."; !strings.Contains(result, want) {
		t.Fatalf("got %s, want substring %q", result, want)
	}
}

func TestHealthz(t *testing.T) {
	b, err := index.NewShardBuilder(&zoekt.Repository{
		Name: "name",
	})
	if err != nil {
		t.Fatalf("NewShardBuilder: %v", err)
	}

	for i := range 2 {
		if err := b.Add(index.Document{
			Name:    fmt.Sprintf("file%d", i),
			Content: []byte("bla"),
		}); err != nil {
			t.Fatalf("Add: %v", err)
		}
	}
	s := searcherForTest(t, b)
	srv := Server{
		Searcher: s,
		Top:      Top,
		HTML:     true,
	}

	mux, err := NewMux(&srv)
	if err != nil {
		t.Fatalf("NewMux: %v", err)
	}

	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)

	req, err := http.NewRequest("GET", ts.URL+"/healthz", nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do(%v): %v", req, err)
	}

	t.Cleanup(func() {
		res.Body.Close()
	})

	if res.StatusCode != http.StatusOK {
		t.Fatalf("want 200 status code, got: %v", res.StatusCode)
	}

	var result zoekt.SearchResult
	err = json.NewDecoder(res.Body).Decode(&result)
	if err != nil {
		t.Fatalf("json.Decode: %v", err)
	}

	if reflect.DeepEqual(result, zoekt.SearchResult{}) {
		t.Fatal("empty result in response")
	}
}

func assertResults(t *testing.T, files []zoekt.FileMatch, want string) {
	t.Helper()

	var lines []string
	for _, fm := range files {
		for _, cm := range fm.ChunkMatches {
			lines = append(lines, fmt.Sprintf("%s: %s", fm.FileName, string(cm.Content)))
		}
	}
	sort.Strings(lines)
	got := strings.TrimSpace(strings.Join(lines, "\n"))
	want = strings.TrimSpace(want)

	if d := cmp.Diff(want, got); d != "" {
		t.Fatalf("unexpected results (-want, +got):\n%s", d)
	}
}
