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

package gitindex

import (
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/google/zoekt"
	"github.com/google/zoekt/build"
)

func TestIndexEmptyRepo(t *testing.T) {
	tmp, err := ioutil.TempDir("", "")
	if err != nil {
		t.Fatalf("TempDir %v", err)
	}
	defer os.RemoveAll(tmp)

	cmd := exec.Command("git", "init", "-b", "master", "repo")
	cmd.Dir = tmp

	if err := cmd.Run(); err != nil {
		t.Fatalf("cmd.Run: %v", err)
	}

	desc := zoekt.Repository{
		Name: "repo",
	}
	opts := Options{
		RepoDir: filepath.Join(tmp, "repo", ".git"),
		BuildOptions: build.Options{
			RepositoryDescription: desc,
			IndexDir:              tmp,
		},
	}

	if err := IndexGitRepo(opts); err != nil {
		t.Fatalf("IndexGitRepo: %v", err)
	}
}
