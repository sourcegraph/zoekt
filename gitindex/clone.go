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
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
)

// returns a list of all the git zoekt settings that have changed or are
// new
func getZoektSettingsToUpdate(repoDest string, newSettings map[string]string, newSettingsKeys []string) ([]string, error) {
	// get the existing config for this repo - and turn it into a map. Used to check
	// diff between old/new config
	cmd := exec.Command("git", "-C", repoDest, "config", "--local", "--get-regexp", "zoekt")
	outBuf := &bytes.Buffer{}
	errBuf := &bytes.Buffer{}
	cmd.Stdout = outBuf
	cmd.Stderr = errBuf

	if err := cmd.Run(); err != nil {
		log.Printf("error getting settings\n")
		return nil, err
	}

	// turn it into a settings map
	oldSettings := make(map[string]string)
	for _, cl := range bytes.Split(outBuf.Bytes(), []byte{'\n'}) {
		if len(cl) == 0 {
			continue
		}

		parts := bytes.SplitN(cl, []byte{' '}, 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("more parts than expected in git config key/v line")
		}
		oldSettings[string(parts[0])] = strings.TrimSpace(string(parts[1]))
	}

	var oldSettingsKeys []string
	for k := range oldSettings {
		oldSettingsKeys = append(oldSettingsKeys, k)
	}
	sort.Strings(oldSettingsKeys)

	// get the list of settings that have changed, or are new
	var settingsToUpdate []string
	for _, k := range newSettingsKeys {
		oldVal, oldHasSetting := oldSettings[k]
		if (!oldHasSetting && newSettings[k] != "") || oldVal != newSettings[k] {
			settingsToUpdate = append(settingsToUpdate, k)
		}
	}

	return settingsToUpdate, nil
}

// Updates the zoekt.* git config options after a repo is cloned.
// Once a repo is cloned, we can no longer use the --config flag to update all
// of it's zoekt.* settings at once. `git config` is limited to one option at once.
// Settings are brute updated
func updateZoektGitConfig(repoDest string, settings map[string]string) (hasChange bool, err error) {
	var keys []string
	for k := range settings {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	settingsToUpdate, err := getZoektSettingsToUpdate(repoDest, settings, keys)
	if err != nil {
		return false, err
	}
	// if there is nothing to update, return
	if len(settingsToUpdate) == 0 {
		return false, nil
	}

	for _, k := range settingsToUpdate {
		if settings[k] != "" {
			if err := exec.Command("git", "-C", repoDest, "config", k, settings[k]).Run(); err != nil {
				return true, err
			}
		}
	}

	return true, nil
}

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
		// Repository exists, ensure settings are in sync
		hadUpdate, err := updateZoektGitConfig(repoDest, settings)
		if err != nil {
			return "", fmt.Errorf("failed to update repository settings: %w", err)
		}
		// For xvandish/zoekt only, we trigger a re-index here, since we have logic
		// to only brute-reindex on an interval. In that case, after a zoektConfig update,
		// if may take bruteReindexInterval hours for the repo-to be re-index and that config
		// to actually be reflected. Unfortunately we have no easy way of knowing whether
		if hadUpdate {
			return repoDest, nil
		}
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
