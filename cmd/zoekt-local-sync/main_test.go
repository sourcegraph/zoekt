// Copyright 2026 Sourcegraph, Inc. All rights reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package main

import (
	"bytes"
	"errors"
	"flag"
	"strings"
	"testing"
)

func TestDirectoryLock(t *testing.T) {
	indexDir := t.TempDir()
	first, err := acquireDirectoryLock(indexDir)
	if err != nil {
		t.Fatal(err)
	}

	if second, err := acquireDirectoryLock(indexDir); err == nil {
		second.Close()
		t.Fatal("acquired an index directory lock twice")
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	third, err := acquireDirectoryLock(indexDir)
	if err != nil {
		t.Fatalf("acquire lock after release: %v", err)
	}
	if err := third.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestSyncDefaultsToPreview(t *testing.T) {
	var out bytes.Buffer
	err := execute([]string{"-index", t.TempDir(), t.TempDir()}, &out, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Pass -f to apply these changes.") {
		t.Fatalf("preview output did not explain how to apply changes:\n%s", out.String())
	}
}

func TestSyncRequiresRoot(t *testing.T) {
	err := execute(nil, &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "at least one root directory") {
		t.Fatalf("got error %v, want root requirement", err)
	}
}

func TestHelpIncludesDefaultOptionsAndSubcommands(t *testing.T) {
	for _, arg := range []string{"-h", "--help", "help"} {
		t.Run(arg, func(t *testing.T) {
			var out bytes.Buffer
			if err := execute([]string{arg}, &out, &bytes.Buffer{}); err != nil {
				t.Fatal(err)
			}
			for _, text := range []string{
				"zoekt-local-sync sync",
				"zoekt-local-sync list",
				"zoekt-local-sync remove",
				"Default sync options:",
				"-branches string",
				"zoekt-local-sync list -h",
				"zoekt-local-sync remove -h",
			} {
				if !strings.Contains(out.String(), text) {
					t.Fatalf("help output does not include %q:\n%s", text, out.String())
				}
			}
		})
	}
}

func TestSyncHelpUsesSyncCommandName(t *testing.T) {
	var errOut bytes.Buffer
	err := execute([]string{"sync", "-h"}, &bytes.Buffer{}, &errOut)
	if !errors.Is(err, flag.ErrHelp) {
		t.Fatalf("got error %v, want flag help", err)
	}
	if !strings.Contains(errOut.String(), "Usage: zoekt-local-sync sync") {
		t.Fatalf("sync help output has the wrong command name:\n%s", errOut.String())
	}
	if !strings.Contains(errOut.String(), "-branches string") {
		t.Fatalf("sync help output does not include sync options:\n%s", errOut.String())
	}
}
