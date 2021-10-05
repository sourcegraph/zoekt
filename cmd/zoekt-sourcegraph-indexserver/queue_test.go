package main

import (
	"fmt"
	"strconv"
	"testing"

	"github.com/google/zoekt"
)

func TestQueue(t *testing.T) {
	queue := &Queue{}

	for i := 0; i < 100; i++ {
		queue.AddOrUpdate(mkHEADIndexOptions(fmt.Sprintf("item-%d", i), strconv.Itoa(i)))
	}

	// Odd numbers are already at the same commit
	for i := 1; i < 100; i += 2 {
		queue.SetIndexed(mkHEADIndexOptions(fmt.Sprintf("item-%d", i), strconv.Itoa(i)), indexStateSuccess)
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
		queue.AddOrUpdate(mkHEADIndexOptions(fmt.Sprintf("item-%d", i), strconv.Itoa(i)))
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

	queue.AddOrUpdate(mkHEADIndexOptions("foo", "foo"))
	queue.AddOrUpdate(mkHEADIndexOptions("bar", "bar"))
	queue.MaybeRemoveMissing([]string{"bar"})

	opts, _ := queue.Pop()
	if opts.Name != "bar" {
		t.Fatalf("queue should only contain bar, pop returned %v", opts.Name)
	}
	_, ok := queue.Pop()
	if ok {
		t.Fatal("queue should be empty")
	}
}

func mkHEADIndexOptions(name, version string) IndexOptions {
	return IndexOptions{
		Name:     name,
		Branches: []zoekt.RepositoryBranch{{Name: "HEAD", Version: version}},
	}
}
