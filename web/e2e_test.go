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
	"io/ioutil"
	"log"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/zoekt"
	"github.com/google/zoekt/query"
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

func searcherForTest(t *testing.T, b *zoekt.IndexBuilder) zoekt.Streamer {
	var buf bytes.Buffer
	if err := b.Write(&buf); err != nil {
		t.Fatal(err)
	}
	f := &memSeeker{buf.Bytes()}

	searcher, err := zoekt.NewSearcher(f)
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
	b, err := zoekt.NewIndexBuilder(&zoekt.Repository{
		Name:                 "name",
		URL:                  "repo-url",
		CommitURLTemplate:    "{{.Version}}",
		FileURLTemplate:      "file-url",
		LineFragmentTemplate: "#line",
		Branches:             []zoekt.RepositoryBranch{{Name: "master", Version: "1234"}},
	})
	if err != nil {
		t.Fatalf("NewIndexBuilder: %v", err)
	}
	if err := b.Add(zoekt.Document{
		Name:    "f2",
		Content: []byte("to carry water in the no later bla"),
		// ------------- 0123456789012345678901234567890123
		// ------------- 0         1         2         3
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

	nowStr := time.Now().Format("Jan 02, 2006 15:04")
	for req, needles := range map[string][]string{
		"/": {"from 1 repositories"},
		"/search?q=water": {
			"href=\"file-url#line",
			"carry <b>water</b>",
		},
		"/search?q=r:": {
			"1234\">master",
			"Found 1 repositories",
			nowStr,
			"repo-url\">name",
			"1 files (36B)",
		},
		"/search?q=magic": {
			`value=magic`,
		},
		"/robots.txt": {
			"disallow: /search",
		},
	} {
		checkNeedles(t, ts, req, needles)
	}
}

func TestPrint(t *testing.T) {
	b, err := zoekt.NewIndexBuilder(&zoekt.Repository{
		Name:                 "name",
		URL:                  "repo-url",
		CommitURLTemplate:    "{{.Version}}",
		FileURLTemplate:      "file-url",
		LineFragmentTemplate: "line",
		Branches:             []zoekt.RepositoryBranch{{Name: "master", Version: "1234"}},
	})
	if err != nil {
		t.Fatalf("NewIndexBuilder: %v", err)
	}
	if err := b.Add(zoekt.Document{
		Name:     "f2",
		Content:  []byte("to carry water in the no later bla"),
		Branches: []string{"master"},
	}); err != nil {
		t.Fatalf("Add: %v", err)
	}

	if err := b.Add(zoekt.Document{
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
	b, err := zoekt.NewIndexBuilder(&zoekt.Repository{
		Name:     "name",
		URL:      "repo-url",
		Branches: []zoekt.RepositoryBranch{{Name: "master", Version: "1234"}},
	})
	if err != nil {
		t.Fatalf("NewIndexBuilder: %v", err)
	}
	if err := b.Add(zoekt.Document{
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
	resultBytes, err := ioutil.ReadAll(res.Body)
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

func TestFormatJson(t *testing.T) {
	b, err := zoekt.NewIndexBuilder(&zoekt.Repository{
		Name:     "name",
		URL:      "repo-url",
		Branches: []zoekt.RepositoryBranch{{Name: "master", Version: "1234"}},
	})
	if err != nil {
		t.Fatalf("NewIndexBuilder: %v", err)
	}
	if err := b.Add(zoekt.Document{
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

	for req, needles := range map[string][]FileMatch{
		"/search?q=water&format=json": {
			{
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
		},
	} {
		checkResultMatches(t, ts, req, needles)
	}
}

func TestContextLines(t *testing.T) {
	b, err := zoekt.NewIndexBuilder(&zoekt.Repository{
		Name:     "name",
		URL:      "repo-url",
		Branches: []zoekt.RepositoryBranch{{Name: "master", Version: "1234"}},
	})
	if err != nil {
		t.Fatalf("NewIndexBuilder: %v", err)
	}
	if err := b.Add(zoekt.Document{
		Name:     "f2",
		Content:  []byte("one line\nsecond snippet\nthird thing\nfourth\nfifth block\nsixth example\nseventh"),
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

	for req, needles := range map[string][]FileMatch{
		"/search?q=our&format=json&ctx=0": {
			{
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
								Post:  "th",
							},
						},
					},
				},
			},
		},
		"/search?q=our&format=json&ctx=2": {
			{
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
								Post:  "th",
							},
						},
						Before: []string{
							"second snippet",
							"third thing",
						},
						After: []string{
							"fifth block",
							"sixth example",
						},
					},
				},
			},
		},
		"/search?q=one&format=json&ctx=2": {
			{
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
								Post:  " line",
							},
						},
						After: []string{
							"second snippet",
							"third thing",
						},
					},
				},
			},
		},
		"/search?q=seventh&format=json&ctx=2": {
			{
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
						Before: []string{
							"fifth block",
							"sixth example",
						},
					},
				},
			},
		},
		"/search?q=seventh&format=json&ctx=10": {
			{
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
						Before: []string{
							"one line",
							"second snippet",
							"third thing",
							"fourth",
							"fifth block",
							"sixth example",
						},
					},
				},
			},
		},
		"/search?q=one&format=json&ctx=10": {
			{
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
								Post:  " line",
							},
						},
						After: []string{
							"second snippet",
							"third thing",
							"fourth",
							"fifth block",
							"sixth example",
							"seventh",
						},
					},
				},
			},
		},
	} {
		checkResultMatches(t, ts, req, needles)
	}
}

func fragmentsPartiallyEqual(a, b []Fragment) bool {
	if len(a) != len(b) {
		return false
	}
	for i, _ := range a {
		if !reflect.DeepEqual(a[i], b[i]) {
			return false
		}
	}
	return true
}

func matchesPartiallyEqual(a, b []Match) bool {
	if len(a) != len(b) {
		return false
	}
	for i, _ := range a {
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
		if !fragmentsPartiallyEqual(a[i].Fragments, b[i].Fragments) {
			return false
		}
	}
	return true
}

func checkResultMatches(t *testing.T, ts *httptest.Server, req string, needles []FileMatch) {
	res, err := http.Get(ts.URL + req)
	if err != nil {
		t.Fatal(err)
	}
	resultBytes, err := ioutil.ReadAll(res.Body)
	res.Body.Close()
	if err != nil {
		log.Fatal(err)
	}

	var result ApiSearchResult
	if err := json.Unmarshal(resultBytes, &result); err != nil {
		log.Fatal(err)
	}
	for i, want := range needles {
		found := false
		for _, match := range result.Result.FileMatches {
			if match.FileName != want.FileName || match.Repo != want.Repo {
				continue
			}
			if !matchesPartiallyEqual(match.Matches, want.Matches) {
				continue
			}
			found = true
			break
		}
		if !found {
			w, _ := json.MarshalIndent(want, "", " ")
			t.Errorf("result doesn't have needles[%d]: %v", i, string(w))
		}
	}
}

func TestConextLinesMustBeValid(t *testing.T) {
	b, err := zoekt.NewIndexBuilder(&zoekt.Repository{
		Name:     "name",
		URL:      "repo-url",
		Branches: []zoekt.RepositoryBranch{{Name: "master", Version: "1234"}},
	})
	if err != nil {
		t.Fatalf("NewIndexBuilder: %v", err)
	}
	if err := b.Add(zoekt.Document{
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
	resultBytes, err := ioutil.ReadAll(res.Body)
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
	b, err := zoekt.NewIndexBuilder(&zoekt.Repository{
		Name: "name",
	})
	if err != nil {
		t.Fatalf("NewIndexBuilder: %v", err)
	}
	if err := b.Add(zoekt.Document{
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
	resultBytes, err := ioutil.ReadAll(res.Body)
	res.Body.Close()
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}

	if got, want := string(resultBytes), "r:myproject"; !strings.Contains(got, want) {
		t.Fatalf("got %s, want substring %q", got, want)
	}
}

func TestDupResult(t *testing.T) {
	b, err := zoekt.NewIndexBuilder(&zoekt.Repository{
		Name: "name",
	})
	if err != nil {
		t.Fatalf("NewIndexBuilder: %v", err)
	}

	for i := 0; i < 2; i++ {
		if err := b.Add(zoekt.Document{
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
	resultBytes, err := ioutil.ReadAll(res.Body)
	res.Body.Close()
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}

	if got, want := string(resultBytes), "Duplicate result"; !strings.Contains(got, want) {
		t.Fatalf("got %s, want substring %q", got, want)
	}
}

func TestTruncateLine(t *testing.T) {
	b, err := zoekt.NewIndexBuilder(&zoekt.Repository{
		Name: "name",
	})
	if err != nil {
		t.Fatalf("NewIndexBuilder: %v", err)
	}

	largePadding := bytes.Repeat([]byte{'a'}, 100*1000) // 100kb
	if err := b.Add(zoekt.Document{
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
	resultBytes, err := ioutil.ReadAll(res.Body)
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
	b, err := zoekt.NewIndexBuilder(&zoekt.Repository{
		Name: "name",
	})
	if err != nil {
		t.Fatalf("NewIndexBuilder: %v", err)
	}

	for i := 0; i < 2; i++ {
		if err := b.Add(zoekt.Document{
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
