package gitindex

import (
	"io/ioutil"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestDeleteRepos(t *testing.T) {
	dir, err := ioutil.TempDir("", "")
	if err != nil {
		t.Fatalf("TempDir: %v", err)
	}
	defer os.RemoveAll(dir)

	if err := createSubmoduleRepo(dir); err != nil {
		t.Error("createSubmoduleRepo", err)
	}

	reposBefore, err := FindGitRepos(dir)
	if err != nil {
		t.Error("FindGitRepos", err)
	}

	gotBefore := map[string]struct{}{}
	for _, r := range reposBefore {
		p, err := filepath.Rel(dir, r)
		if err != nil {
			t.Fatalf("Relative: %v", err)
		}

		gotBefore[p] = struct{}{}
	}

	wantBefore := map[string]struct{}{
		"adir/.git":                                    {},
		"bdir/.git":                                    {},
		"gerrit.googlesource.com/adir.git":             {},
		"gerrit.googlesource.com/bdir.git":             {},
		"gerrit.googlesource.com/sub/bdir.git":         {},
		"gerrit.googlesource.com/team/scope/repoa.git": {},
		"gerrit.googlesource.com/team/scope/repob.git": {},
	}

	if !reflect.DeepEqual(gotBefore, wantBefore) {
		t.Fatalf("got %v want %v", gotBefore, wantBefore)
	}

	aURL, _ := url.Parse("http://gerrit.googlesource.com")
	aURL.Path = "sub"
	names := map[string]struct{}{
		"bdir/.git":                        {},
		"gerrit.googlesource.com/adir.git": {},
	}
	filter, _ := NewFilter("", "")

	err = DeleteRepos(dir, aURL, names, filter)
	if err != nil {
		t.Fatalf("DeleteRepos: %T", err)
	}

	bURL, _ := url.Parse("http://gerrit.googlesource.com")
	bURL.Path = ""
	names = map[string]struct{}{
		"gerrit.googlesource.com/adir.git":             {},
		"gerrit.googlesource.com/bdir.git":             {},
		"gerrit.googlesource.com/team/scope/repob.git": {},
	}

	err = DeleteRepos(dir, bURL, names, filter)
	if err != nil {
		t.Fatalf("DeleteRepos: %T", err)
	}

	reposAfter, err := FindGitRepos(dir)
	if err != nil {
		t.Error("FindGitRepos", err)
	}

	gotAfter := map[string]struct{}{}
	for _, r := range reposAfter {
		p, err := filepath.Rel(dir, r)
		if err != nil {
			t.Fatalf("Relative: %v", err)
		}

		gotAfter[p] = struct{}{}
	}
	wantAfter := map[string]struct{}{
		"adir/.git":                                    {},
		"bdir/.git":                                    {},
		"gerrit.googlesource.com/adir.git":             {},
		"gerrit.googlesource.com/bdir.git":             {},
		"gerrit.googlesource.com/team/scope/repob.git": {},
	}

	if !reflect.DeepEqual(gotAfter, wantAfter) {
		t.Errorf("got %v want %v", gotAfter, wantAfter)
	}
}
