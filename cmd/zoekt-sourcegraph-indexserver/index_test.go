package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/google/zoekt"
)

func TestGetIndexOptions(t *testing.T) {
	var response []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if got, want := r.URL.String(), "/.internal/search/configuration"; got != want {
			http.Error(w, fmt.Sprintf("got URL %v want %v", got, want), http.StatusBadRequest)
			return
		}
		if got, want := r.Form, (url.Values{"repo": []string{"test/repo"}}); !reflect.DeepEqual(got, want) {
			http.Error(w, fmt.Sprintf("got URL %v want %v", got, want), http.StatusBadRequest)
			return
		}
		w.Write(response)
	}))
	defer server.Close()

	u, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}

	cases := map[string]*IndexOptions{
		`{"Symbols": true, "LargeFiles": ["foo","bar"]}`: {
			Symbols:    true,
			LargeFiles: []string{"foo", "bar"},
		},

		`{"Symbols": false, "LargeFiles": ["foo","bar"]}`: {
			LargeFiles: []string{"foo", "bar"},
		},

		`{}`: {},

		`{"Symbols": true}`: {
			Symbols: true,
		},

		`{"RepoID": 123}`: {
			RepoID: 123,
		},

		`{"Error": "boom"}`: nil,
	}

	for r, want := range cases {
		response = []byte(r)

		got, err := getIndexOptions(u, "test/repo")
		if err != nil && want != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if want == nil {
			continue
		}

		if d := cmp.Diff(*want, got[0].IndexOptions); d != "" {
			t.Log("response", r)
			t.Errorf("mismatch (-want +got):\n%s", d)
		}
	}
}

func TestIndex(t *testing.T) {
	root, err := url.Parse("http://api.test")
	if err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name        string
		args        indexArgs
		wantArchive []string
		wantGit     []string
	}{{
		name: "minimal",
		args: indexArgs{
			Root: root,
			Name: "test/repo",
			IndexOptions: IndexOptions{
				Branches: []zoekt.RepositoryBranch{{Name: "HEAD", Version: "deadbeef"}},
			},
		},
		wantArchive: []string{
			"zoekt-archive-index -name test/repo -commit deadbeef -branch HEAD -disable_ctags http://api.test/.internal/git/test/repo/tar/deadbeef",
		},
		wantGit: []string{
			"git -c init.defaultBranch=nonExistentBranchBB0FOFCH32 init --bare $TMPDIR/test%2Frepo.git",
			"git -C $TMPDIR/test%2Frepo.git -c protocol.version=2 fetch --depth=1 http://api.test/.internal/git/test/repo deadbeef",
			"git -C $TMPDIR/test%2Frepo.git update-ref HEAD deadbeef",
			"git -C $TMPDIR/test%2Frepo.git config zoekt.archived 0",
			"git -C $TMPDIR/test%2Frepo.git config zoekt.fork 0",
			"git -C $TMPDIR/test%2Frepo.git config zoekt.name test/repo",
			"git -C $TMPDIR/test%2Frepo.git config zoekt.priority 0",
			"git -C $TMPDIR/test%2Frepo.git config zoekt.public 0",
			"git -C $TMPDIR/test%2Frepo.git config zoekt.repoid 0",
			"zoekt-git-index -submodules=false -branches HEAD -disable_ctags $TMPDIR/test%2Frepo.git",
		},
	}, {
		name: "minimal-id",
		args: indexArgs{
			Root: root,
			Name: "test/repo",
			IndexOptions: IndexOptions{
				Branches: []zoekt.RepositoryBranch{{Name: "HEAD", Version: "deadbeef"}},
				RepoID:   123,
			},
		},
		wantArchive: []string{
			"zoekt-archive-index -name test/repo -commit deadbeef -branch HEAD -disable_ctags http://api.test/.internal/git/test/repo/tar/deadbeef",
		},
		wantGit: []string{
			"git -c init.defaultBranch=nonExistentBranchBB0FOFCH32 init --bare $TMPDIR/test%2Frepo.git",
			"git -C $TMPDIR/test%2Frepo.git -c protocol.version=2 fetch --depth=1 http://api.test/.internal/git/test/repo deadbeef",
			"git -C $TMPDIR/test%2Frepo.git update-ref HEAD deadbeef",
			"git -C $TMPDIR/test%2Frepo.git config zoekt.archived 0",
			"git -C $TMPDIR/test%2Frepo.git config zoekt.fork 0",
			"git -C $TMPDIR/test%2Frepo.git config zoekt.name test/repo",
			"git -C $TMPDIR/test%2Frepo.git config zoekt.priority 0",
			"git -C $TMPDIR/test%2Frepo.git config zoekt.public 0",
			"git -C $TMPDIR/test%2Frepo.git config zoekt.repoid 123",
			"zoekt-git-index -submodules=false -branches HEAD -disable_ctags $TMPDIR/test%2Frepo.git",
		},
	}, {
		name: "all",
		args: indexArgs{
			Root:              root,
			Name:              "test/repo",
			Incremental:       true,
			IndexDir:          "/data/index",
			Parallelism:       4,
			FileLimit:         123,
			DownloadLimitMBPS: "1000",
			IndexOptions: IndexOptions{
				LargeFiles: []string{"foo", "bar"},
				Symbols:    true,
				Branches: []zoekt.RepositoryBranch{
					{Name: "HEAD", Version: "deadbeef"},
					{Name: "dev", Version: "feebdaed"}, // ignored for archive
				},
			},
		},
		wantArchive: []string{strings.Join([]string{
			"zoekt-archive-index",
			"-name", "test/repo",
			"-commit", "deadbeef",
			"-branch", "HEAD",
			"-incremental",
			"-download-limit-mbps", "1000",
			"-file_limit", "123",
			"-parallelism", "4",
			"-index", "/data/index",
			"-require_ctags",
			"-large_file", "foo",
			"-large_file", "bar",
			"http://api.test/.internal/git/test/repo/tar/deadbeef",
		}, " ")},
		wantGit: []string{
			"git -c init.defaultBranch=nonExistentBranchBB0FOFCH32 init --bare $TMPDIR/test%2Frepo.git",
			"git -C $TMPDIR/test%2Frepo.git -c protocol.version=2 fetch --depth=1 http://api.test/.internal/git/test/repo deadbeef feebdaed",
			"git -C $TMPDIR/test%2Frepo.git update-ref HEAD deadbeef",
			"git -C $TMPDIR/test%2Frepo.git update-ref refs/heads/dev feebdaed",
			"git -C $TMPDIR/test%2Frepo.git config zoekt.archived 0",
			"git -C $TMPDIR/test%2Frepo.git config zoekt.fork 0",
			"git -C $TMPDIR/test%2Frepo.git config zoekt.name test/repo",
			"git -C $TMPDIR/test%2Frepo.git config zoekt.priority 0",
			"git -C $TMPDIR/test%2Frepo.git config zoekt.public 0",
			"git -C $TMPDIR/test%2Frepo.git config zoekt.repoid 0",
			"zoekt-git-index -submodules=false -incremental -branches HEAD,dev " +
				"-file_limit 123 -parallelism 4 -index /data/index -require_ctags -large_file foo -large_file bar " +
				"$TMPDIR/test%2Frepo.git",
		},
	}}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var got []string
			runCmd := func(c *exec.Cmd) error {
				cmd := strings.Join(c.Args, " ")
				cmd = strings.ReplaceAll(cmd, filepath.Clean(os.TempDir()), "$TMPDIR")
				got = append(got, cmd)
				return nil
			}

			branches := tc.args.Branches
			tc.args.Branches = branches[:1]
			if err := archiveIndex(&tc.args, runCmd); err != nil {
				t.Fatal(err)
			}
			if !cmp.Equal(got, tc.wantArchive) {
				t.Errorf("archive mismatch (-want +got):\n%s", cmp.Diff(tc.wantArchive, got, splitargs))
			}

			got = nil
			tc.args.Branches = branches
			if err := gitIndex(&tc.args, runCmd); err != nil {
				t.Fatal(err)
			}
			if !cmp.Equal(got, tc.wantGit) {
				t.Errorf("git mismatch (-want +got):\n%s", cmp.Diff(tc.wantGit, got, splitargs))
			}
		})
	}
}

var splitargs = cmpopts.AcyclicTransformer("splitargs", func(cmd string) []string {
	return strings.Split(cmd, " ")
})
