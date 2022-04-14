// Copyright 2019 Google Inc. All rights reserved.
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
	"os/exec"
	"testing"

	git "github.com/go-git/go-git/v5"
)

func TestSetRemote(t *testing.T) {
	dir := t.TempDir()
	script := `mkdir orig
cd orig
git init
cd ..
git clone orig/.git clone.git
`

	cmd := exec.Command("/bin/sh", "-euxc", script)
	cmd.Dir = dir

	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("execution error: %v, output %s", err, out)
	}

	r := dir + "/clone.git"
	if err := setFetch(r, "origin", "+refs/heads/*:refs/heads/*"); err != nil {
		t.Fatalf("addFetch: %v", err)
	}

	repo, err := git.PlainOpen(r)
	if err != nil {
		t.Fatal("PlainOpen", err)
	}

	rm, err := repo.Remote("origin")
	if err != nil {
		t.Fatal("Remote", err)
	}
	if got, want := rm.Config().Fetch[0].String(), "+refs/heads/*:refs/heads/*"; got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}
