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

package gitindex

import (
	"bytes"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"sort"

	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
)

// CloneRepo clones one repository, adding the given config
// settings. It returns the bare repo directory. The `name` argument
// determines where the repo is stored relative to `destDir`. Returns
// the directory of the repository.
func CloneRepo(destDir, name, cloneURL string, settings map[string]string) (string, error) {
	parent := filepath.Join(destDir, filepath.Dir(name))
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return "", err
	}

	repoDest := filepath.Join(parent, filepath.Base(name)+".git")
	if _, err := os.Lstat(repoDest); err == nil {
		return "", nil
	}

	var keys []string
	for k := range settings {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var config []string
	for _, k := range keys {
		if settings[k] != "" {
			config = append(config, "--config", k+"="+settings[k])
		}
	}

	cmd := exec.Command(
		"git", "clone", "--bare", "--verbose", "--progress",
	)
	cmd.Args = append(cmd.Args, config...)
	cmd.Args = append(cmd.Args, cloneURL, repoDest)

	// Prevent prompting
	cmd.Stdin = &bytes.Buffer{}
	log.Println("running:", cmd.Args)
	if err := cmd.Run(); err != nil {
		return "", err
	}

	if err := setFetch(repoDest, "origin", "+refs/heads/*:refs/heads/*"); err != nil {
		log.Printf("addFetch: %v", err)
	}
	return repoDest, nil
}

func setFetch(repoDir, remote, refspec string) error {
	repo, err := git.PlainOpen(repoDir)
	if err != nil {
		return err
	}

	cfg, err := repo.Config()
	if err != nil {
		return err
	}

	rm := cfg.Remotes[remote]
	if rm != nil {
		rm.Fetch = []config.RefSpec{config.RefSpec(refspec)}
	}
	if err := repo.Storer.SetConfig(cfg); err != nil {
		return err
	}

	return nil
}
