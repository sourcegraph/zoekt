// Copyright 2017 Google Inc. All rights reserved.
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

package gitindex

import (
	"net/url"
	"reflect"
	"sort"
	"testing"
)

func TestListReposNonExistent(t *testing.T) {
	u, err := url.Parse("https://gerrit.googlesource.com/")
	if err != nil {
		t.Fatalf("url.Parse: %v", err)
	}

	rs, err := ListRepos("/doesnotexist", u)
	if err == nil {
		t.Fatalf("ListRepos(/doesnotexist): %v", rs)
	}
}

func TestListRepos(t *testing.T) {
	dir := t.TempDir()

	if err := createSubmoduleRepo(dir); err != nil {
		t.Fatalf("createSubmoduleRepo %v", err)
	}

	u, err := url.Parse("https://gerrit.googlesource.com/")
	if err != nil {
		t.Fatalf("url.Parse: %v", err)
	}
	rs, err := ListRepos(dir, u)
	if err != nil {
		t.Fatalf("ListRepos(%s): %v", u, err)
	}

	want := []string{
		"gerrit.googlesource.com/adir.git",
		"gerrit.googlesource.com/bdir.git",
		"gerrit.googlesource.com/sub/bdir.git",
		"gerrit.googlesource.com/team/scope/repoa.git",
		"gerrit.googlesource.com/team/scope/repob.git",
	}
	sort.Strings(rs)

	if !reflect.DeepEqual(rs, want) {
		t.Fatalf("got %v, want %v", rs, want)
	}
}
