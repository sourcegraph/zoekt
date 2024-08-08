package main

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/sourcegraph/log/logtest"
	"github.com/sourcegraph/zoekt"
)

func TestQueue(t *testing.T) {
	backoffDuration := 1 * time.Millisecond
	queue := NewQueue(backoffDuration, backoffDuration, logtest.Scoped(t))

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
		item, ok := queue.Pop()
		if !ok {
			break
		}
		got, _ := strconv.Atoi(item.Opts.Branches[0].Version)
		if got != want {
			t.Fatalf("got %v, want %v", got, want)
		}
		want += 2
		if want == 100 {
			// We now switch to processing the odd numbers
			want = 1
		}

		// sanity check the date added
		if item.DateAddedToQueue.Unix() <= 0 {
			t.Fatalf("invalid DateAddedToQueue %v", item.DateAddedToQueue)
		}

		// update current, shouldn't put the job in the queue
		queue.SetIndexed(item.Opts, indexStateSuccess)
	}
	if want != 101 {
		t.Fatalf("only popped %d items", want)
	}
}

func TestQueueFIFO(t *testing.T) {
	// Tests that the queue fallbacks to FIFO if everything has the same
	// priority
	backoffDuration := 1 * time.Millisecond
	queue := NewQueue(backoffDuration, backoffDuration, logtest.Scoped(t))

	for i := 0; i < 100; i++ {
		queue.AddOrUpdate(mkHEADIndexOptions(i, strconv.Itoa(i)))
	}

	want := 0
	for {
		item, ok := queue.Pop()
		if !ok {
			break
		}
		got, _ := strconv.Atoi(item.Opts.Branches[0].Version)
		if got != want {
			t.Fatalf("got %v, want %v", item, want)
		}
		queue.SetIndexed(item.Opts, indexStateSuccess)
		want++
	}
	if want != 100 {
		t.Fatalf("only popped %d items", want)
	}
}

func TestQueue_MaybeRemoveMissing(t *testing.T) {
	backoffDuration := 1 * time.Millisecond
	queue := NewQueue(backoffDuration, backoffDuration, logtest.Scoped(t))

	queue.AddOrUpdate(IndexOptions{RepoID: 1, Name: "foo"})
	queue.AddOrUpdate(IndexOptions{RepoID: 2, Name: "bar"})
	queue.MaybeRemoveMissing([]uint32{2})

	item, _ := queue.Pop()
	if item.Opts.Name != "bar" {
		t.Fatalf("queue should only contain bar, pop returned %v", item.Opts.Name)
	}
	_, ok := queue.Pop()
	if ok {
		t.Fatal("queue should be empty")
	}
}

func TestQueue_Bump(t *testing.T) {
	backoffDuration := 1 * time.Millisecond
	queue := NewQueue(backoffDuration, backoffDuration, logtest.Scoped(t))

	queue.AddOrUpdate(IndexOptions{RepoID: 1, Name: "foo"})
	queue.AddOrUpdate(IndexOptions{RepoID: 2, Name: "bar"})

	emptyQueue(queue)

	// Bump 2 and 3. 3 doesn't exist, so only 2 should exist.
	missing := queue.Bump([]uint32{2, 3})
	if d := cmp.Diff([]uint32{3}, missing); d != "" {
		t.Errorf("unexpected missing (-want, +got):\n%s", d)
	}

	want := []IndexOptions{{RepoID: 2, Name: "bar"}}
	var got []IndexOptions
	for {
		item, ok := queue.Pop()
		if !ok {
			break
		}
		got = append(got, item.Opts)
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
		output = strings.TrimSpace(output)

		var outputLines []string
		for i, line := range strings.Split(output, "\n") {
			columns := []string{"Position", "Name", "ID", "IsOnQueue", "Age", "Branches"}
			parts := strings.Fields(line) // Note: splitting on spaces like this would break for repositories that have more than one branch, but it's fine for just this test
			if len(columns) != len(parts) {
				t.Fatalf("normalizeDebugOutput: line %d: expected %d columns, got %d columns: %q", i, len(columns), len(parts), line)
			}

			if i > 0 { // skip past the first line which just contains the column headings

				// The debug output contains time.Durations for tracking the amount of time an indexing job
				// spent in the queue, but it's not reasonable to assert on this kind of timing minutia.
				// So, for comparison purposes, we massage the contents of this field in the following manner:
				//
				// - "1m30s" -> "*" (for jobs that are still enqueued)
				// - "-"     -> "-" (for jobs that are tracked, but are not currently enqueued)

				if parts[4] != "-" {
					parts[4] = "*"
				}
			}

			outputLines = append(outputLines, strings.Join(parts, " "))
		}

		return strings.Join(outputLines, "\n")
	}

	backoffDuration := 1 * time.Millisecond
	queue := NewQueue(backoffDuration, backoffDuration, logtest.Scoped(t))

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

	// test: send a request to the queue's debug endpoint
	response, err := http.Get(server.URL)
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
Position        Name            ID              IsOnQueue       Age                    Branches
0               item-1          1               true            *                      HEAD@stillQueued
1               item-0          0               false           -                      HEAD@popped
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
