package main

import (
	"sort"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestPublicSet(t *testing.T) {
	// We test by creating a sequence of updates, and testing at each step that
	// what we read back matches what we simulate.
	steps := []struct{ desc, public, failed, want string }{{
		desc: "empty",
		want: "",
	}, {
		desc:   "add 1 repo",
		public: "public",
		want:   "public",
	}, {
		desc:   "noop",
		public: "public",
		want:   "public",
	}, {
		desc:   "ignore failed repo, we don't know if it is public",
		public: "public",
		failed: "failed",
		want:   "public",
	}, {
		desc:   "failed to update public, but keep it in the set",
		public: "",
		failed: "failed public",
		want:   "public",
	}, {
		desc:   "add in new_public",
		public: "new_public",
		failed: "failed public",
		want:   "public new_public",
	}, {
		desc:   "everything changes",
		public: "new_world",
		want:   "new_world",
	}, {
		desc:   "test going to nothing public",
		failed: "public",
		want:   "",
	}}

	dir := t.TempDir()
	for i, step := range steps {
		public := strings.Split(step.public, " ")
		failed := map[string]struct{}{}
		for _, v := range strings.Split(step.failed, " ") {
			failed[v] = struct{}{}
		}

		err := writePublicSet(dir, public, failed)
		if err != nil {
			t.Fatalf("failed at step %d: %s: %v", i, step.desc, err)
		}

		var got []string
		err = readPublicSetFunc(dir, func(v string) {
			got = append(got, v)
		})
		if err != nil {
			t.Fatalf("failed at step %d: %s: %v", i, step.desc, err)
		}

		want := strings.Split(step.want, " ")
		if step.want == "" {
			want = nil
		}

		sort.Strings(got)
		sort.Strings(want)
		if d := cmp.Diff(want, got); d != "" {
			t.Fatalf("failed at step %d: %s: (-want, +got):\n%s", i, step.desc, d)
		}
	}
}
