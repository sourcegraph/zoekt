package main

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"unicode"

	"github.com/google/go-cmp/cmp"
	"github.com/google/zoekt"
)

func TestQueue(t *testing.T) {
	queue := &Queue{}

	for i := 0; i < 100; i++ {
		queue.AddOrUpdate(mkHEADIndexOptions(i, strconv.Itoa(i)))
	}

	// Odd numbers are already at the same commit
	for i := 1; i < 100; i += 2 {
		queue.SetIndexed(mkHEADIndexOptions(i, strconv.Itoa(i)), indexStateSuccess)
	}

	// Ensure we process all the even commits first, then odd.
	want := 0
	for {
		opts, ok := queue.Pop()
		if !ok {
			break
		}
		got, _ := strconv.Atoi(opts.Branches[0].Version)
		if got != want {
			t.Fatalf("got %v, want %v", opts, want)
		}
		want += 2
		if want == 100 {
			// We now switch to processing the odd numbers
			want = 1
		}
		// update current, shouldn't put the job in the queue
		queue.SetIndexed(opts, indexStateSuccess)
	}
	if want != 101 {
		t.Fatalf("only popped %d items", want)
	}
}

func TestQueueFIFO(t *testing.T) {
	// Tests that the queue fallbacks to FIFO if everything has the same
	// priority
	queue := &Queue{}

	for i := 0; i < 100; i++ {
		queue.AddOrUpdate(mkHEADIndexOptions(i, strconv.Itoa(i)))
	}

	want := 0
	for {
		opts, ok := queue.Pop()
		if !ok {
			break
		}
		got, _ := strconv.Atoi(opts.Branches[0].Version)
		if got != want {
			t.Fatalf("got %v, want %v", opts, want)
		}
		queue.SetIndexed(opts, indexStateSuccess)
		want++
	}
	if want != 100 {
		t.Fatalf("only popped %d items", want)
	}
}

func TestQueue_MaybeRemoveMissing(t *testing.T) {
	queue := &Queue{}

	queue.AddOrUpdate(IndexOptions{RepoID: 1, Name: "foo"})
	queue.AddOrUpdate(IndexOptions{RepoID: 2, Name: "bar"})
	queue.MaybeRemoveMissing([]uint32{2})

	opts, _ := queue.Pop()
	if opts.Name != "bar" {
		t.Fatalf("queue should only contain bar, pop returned %v", opts.Name)
	}
	_, ok := queue.Pop()
	if ok {
		t.Fatal("queue should be empty")
	}
}

func TestQueue_Bump(t *testing.T) {
	queue := &Queue{}

	queue.AddOrUpdate(IndexOptions{RepoID: 1, Name: "foo"})
	queue.AddOrUpdate(IndexOptions{RepoID: 2, Name: "bar"})

	// Empty queue
	for ok := true; ok; _, ok = queue.Pop() {
	}

	// Bump 2 and 3. 3 doesn't exist, so only 2 should exist.
	missing := queue.Bump([]uint32{2, 3})
	if d := cmp.Diff([]uint32{3}, missing); d != "" {
		t.Errorf("unexpected missing (-want, +got):\n%s", d)
	}

	want := []IndexOptions{{RepoID: 2, Name: "bar"}}
	var got []IndexOptions
	for {
		opts, ok := queue.Pop()
		if !ok {
			break
		}
		got = append(got, opts)
	}

	if d := cmp.Diff(want, got); d != "" {
		t.Errorf("unexpected items bumped into the queue (-want, +got):\n%s", d)
	}
}

func TestQueue_Integration_DebugQueue(t *testing.T) {
	// helper function to normalize the queue's debug output - this makes the test less brittle
	// + makes it much less annoying to make edits to the expected output in a way that doesn't
	// materially affect the caller
	normalizeDebugOutput := func(output string) string {
		// remove any leading or trailing whitespace
		output = strings.TrimSpace(output)

		// remove trailing whitespace at the of each row (could come from column padding)
		var lines []string
		for _, l := range strings.Split(output, "\n") {
			lines = append(lines, strings.TrimRightFunc(l, unicode.IsSpace))
		}

		return strings.Join(lines, "\n")
	}

	queue := &Queue{}

	// setup: add two repositories to the queue and pop one of them
	poppedRepository := mkHEADIndexOptions(0, "popped")
	queuedRepository := mkHEADIndexOptions(1, "stillQueued")

	queue.AddOrUpdate(poppedRepository)
	queue.Pop()

	queue.AddOrUpdate(queuedRepository)

	// setup: start test http server that forwards requests to the
	// queue instance
	server := httptest.NewServer(http.HandlerFunc(queue.handleDebugQueue))
	defer server.Close()

	// setup: add the ?header=true query parameter to ensure that we fetch column headers
	address, _ := url.Parse(server.URL)
	params := address.Query()
	params.Set("header", "true")
	address.RawQuery = params.Encode()

	// test: send a request to the queue's debug endpoint
	response, err := http.Get(address.String())
	if err != nil {
		t.Fatalf(err.Error())
	}

	defer response.Body.Close()
	raw, err := io.ReadAll(response.Body)
	if err != nil {
		t.Errorf("reading response body: %s", err)
	}

	actualOutput := normalizeDebugOutput(string(raw))

	expectedOutput := `
Position        Name            ID              IsOnQueue       Branches
0               item-1          1               true            HEAD@stillQueued
1               item-0          0               false           HEAD@popped
`

	expectedOutput = normalizeDebugOutput(expectedOutput)

	// verify: ensure that the received output matches what we expect
	if diff := cmp.Diff(expectedOutput, actualOutput); diff != "" {
		t.Errorf("unexpected diff in output (-want +got):\n%s", diff)
	}
}

func mkHEADIndexOptions(id int, version string) IndexOptions {
	return IndexOptions{
		RepoID:   uint32(id),
		Name:     fmt.Sprintf("item-%d", id),
		Branches: []zoekt.RepositoryBranch{{Name: "HEAD", Version: version}},
	}
}
