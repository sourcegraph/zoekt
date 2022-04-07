package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

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
	queue := &Queue{}

	numRepositories := 100
	expectedRepositories := make([]IndexOptions, numRepositories)

	for i := 0; i < numRepositories; i++ {
		// setup: initialize repositories
		expectedRepositories[i] = mkHEADIndexOptions(i, strconv.Itoa(i))
	}

	// setup: add expectedRepositories to queue
	for _, r := range expectedRepositories {
		queue.AddOrUpdate(r)
	}

	// setup: start test http server that forwards requests to the
	// queue instance
	server := httptest.NewServer(http.HandlerFunc(queue.handleDebugQueue))
	defer server.Close()

	// test: create stream of queueItems from test server
	stream, err := createQueueItemStream(context.Background(), server.URL)
	if err != nil {
		t.Fatalf("creating queueItem stream: %s", err)
	}
	defer stream.Close()

	// test: decode stream of gob entries and extract
	// all the index options for later comparison
	decoder := newQueueItemStreamDecoder(stream)

	var receivedRepositories []IndexOptions
	for decoder.Next() {
		item := decoder.Item()
		receivedRepositories = append(receivedRepositories, item.Opts)
	}
	if decoder.Err() != nil {
		t.Fatalf("newQueueItemStreamDecoder.Decoder: %s", err)
	}

	// verify: ensure that we get the same repositories (modeled by indexOptions) in the same order
	// after a round of gob-encoding and gob-decoding
	//
	// note: I thought it would be brittle to compare the queueItems directly (since we can't add a queueItem
	// via the queue API), so I thought only looking at the indexOptions was a good substitute
	if diff := cmp.Diff(expectedRepositories, receivedRepositories); diff != "" {
		t.Errorf("unexpected diff in recieved indexOptions (-want +got):\n%s", diff)
	}
}

func mkHEADIndexOptions(id int, version string) IndexOptions {
	return IndexOptions{
		RepoID:   uint32(id),
		Name:     fmt.Sprintf("item-%d", id),
		Branches: []zoekt.RepositoryBranch{{Name: "HEAD", Version: version}},
	}
}
