package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	sglog "github.com/sourcegraph/log"
	"github.com/sourcegraph/log/logtest"
	"github.com/sourcegraph/zoekt/build"

	"github.com/sourcegraph/zoekt"
)

func TestServer_defaultArgs(t *testing.T) {
	root, err := url.Parse("http://api.test")
	if err != nil {
		t.Fatal(err)
	}

	s := &Server{
		Sourcegraph: newSourcegraphClient(root, "", 0),
		IndexDir:    "/testdata/index",
		CPUCount:    6,
	}
	want := &indexArgs{
		IndexOptions: IndexOptions{
			Name: "testName",
		},
		IndexDir:    "/testdata/index",
		Parallelism: 6,
		Incremental: true,
		FileLimit:   1 << 20,
	}
	got := s.indexArgs(IndexOptions{Name: "testName"})
	if !cmp.Equal(got, want) {
		t.Errorf("mismatch (-want +got):\n%s", cmp.Diff(want, got))
	}
}

func TestListRepoIDs(t *testing.T) {
	var gotBody string
	var gotURL *url.URL
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotURL = r.URL

		b, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		gotBody = string(b)

		_, err = w.Write([]byte(`{"RepoIDs": [1, 2, 3]}`))
		if err != nil {
			t.Fatal(err)
		}
	}))
	defer ts.Close()

	u, err := url.Parse(ts.URL)
	if err != nil {
		t.Fatal(err)
	}

	s := newSourcegraphClient(u, "test-indexed-search-1", 0)

	gotRepos, err := s.List(context.Background(), []uint32{1, 3})
	if err != nil {
		t.Fatal(err)
	}

	if want := []uint32{1, 2, 3}; !cmp.Equal(gotRepos.IDs, want) {
		t.Errorf("repos mismatch (-want +got):\n%s", cmp.Diff(want, gotRepos.IDs))
	}
	if want := `{"Hostname":"test-indexed-search-1","IndexedIDs":[1,3]}`; gotBody != want {
		t.Errorf("body mismatch (-want +got):\n%s", cmp.Diff(want, gotBody))
	}
	if want := "/.internal/repos/index"; gotURL.Path != want {
		t.Errorf("request path mismatch (-want +got):\n%s", cmp.Diff(want, gotURL.Path))
	}
}

func TestDeleteShards(t *testing.T) {
	remainingRepoA := zoekt.Repository{ID: 1, Name: "A"}
	remainingRepoB := zoekt.Repository{ID: 2, Name: "B"}
	repositoryToDelete := zoekt.Repository{ID: 99, Name: "DELETE_ME"}

	t.Run("delete repository from set of normal shards", func(t *testing.T) {
		indexDir := t.TempDir()

		// map of repoID -> list of associated shard paths + metadata paths
		shardMap := make(map[uint32][]string)

		// setup: create shards for each repository, and populate the shard map
		for _, r := range []zoekt.Repository{
			remainingRepoA,
			remainingRepoB,
			repositoryToDelete,
		} {
			shards := createTestNormalShard(t, indexDir, r, 3)

			for _, shard := range shards {
				// create stub meta file
				metaFile := shard + ".meta"
				f, err := os.Create(metaFile)
				if err != nil {
					t.Fatalf("creating metadata file %q: %s", metaFile, err)
				}

				f.Close()

				shardMap[r.ID] = append(shardMap[r.ID], shard, metaFile)
			}
		}

		// run test: delete repository
		options := &build.Options{
			IndexDir:              indexDir,
			RepositoryDescription: repositoryToDelete,
		}
		options.SetDefaults()

		err := deleteShards(options)
		if err != nil {
			t.Errorf("unexpected error when deleting shards: %s", err)
		}

		// run assertions: gather all the shards + meta files that remain and
		// check to see that only the files associated with the "remaining" repositories
		// are present
		var actualShardFiles []string

		for _, pattern := range []string{"*.zoekt", "*.meta"} {
			files, err := filepath.Glob(filepath.Join(indexDir, pattern))
			if err != nil {
				t.Fatalf("globbing indexDir: %s", err)
			}

			actualShardFiles = append(actualShardFiles, files...)
		}

		var expectedShardFiles []string
		expectedShardFiles = append(expectedShardFiles, shardMap[remainingRepoA.ID]...)
		expectedShardFiles = append(expectedShardFiles, shardMap[remainingRepoB.ID]...)

		sort.Strings(actualShardFiles)
		sort.Strings(expectedShardFiles)

		if diff := cmp.Diff(expectedShardFiles, actualShardFiles); diff != "" {
			t.Errorf("unexpected diff in list of shard files (-want +got):\n%s", diff)
		}
	})

	t.Run("delete repository from compound shard", func(t *testing.T) {
		indexDir := t.TempDir()

		// setup: enable shard merging for compound shards
		t.Setenv("SRC_ENABLE_SHARD_MERGING", "1")

		// setup: create compound shard with all repositories
		repositories := []zoekt.Repository{remainingRepoA, remainingRepoB, repositoryToDelete}
		shard := createTestCompoundShard(t, indexDir, repositories)

		// run test: delete repository
		options := &build.Options{
			IndexDir:              indexDir,
			RepositoryDescription: repositoryToDelete,
		}
		options.SetDefaults()

		err := deleteShards(options)
		if err != nil {
			t.Errorf("unexpected error when deleting shards: %s", err)
		}

		// verify: read the compound shard, and ensure that only
		// the repositories that we expect are in the shard (and the deleted one has been tombstoned)
		actualRepositories, _, err := zoekt.ReadMetadataPathAlive(shard)
		if err != nil {
			t.Fatalf("reading repository metadata from shard: %s", err)
		}

		expectedRepositories := []*zoekt.Repository{&remainingRepoA, &remainingRepoB}

		sort.Slice(actualRepositories, func(i, j int) bool {
			return actualRepositories[i].ID < actualRepositories[j].ID
		})

		sort.Slice(expectedRepositories, func(i, j int) bool {
			return expectedRepositories[i].ID < expectedRepositories[j].ID
		})

		opts := []cmp.Option{
			cmpopts.IgnoreUnexported(zoekt.Repository{}),
			cmpopts.IgnoreFields(zoekt.Repository{}, "IndexOptions", "HasSymbols"),
			cmpopts.EquateEmpty(),
		}
		if diff := cmp.Diff(expectedRepositories, actualRepositories, opts...); diff != "" {
			t.Errorf("unexpected diff in list of repositories (-want +got):\n%s", diff)
		}
	})
}

func TestListRepoIDs_Error(t *testing.T) {
	msg := "deadbeaf deadbeaf"
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {

		// This is how Sourcegraph returns error messages to the caller.
		http.Error(w, msg, http.StatusInternalServerError)
	}))
	defer ts.Close()

	u, err := url.Parse(ts.URL)
	if err != nil {
		t.Fatal(err)
	}

	s := newSourcegraphClient(u, "test-indexed-search-1", 0)
	s.Client.RetryMax = 0

	_, err = s.List(context.Background(), []uint32{1, 3})

	if !strings.Contains(err.Error(), msg) {
		t.Fatalf("%s does not contain %s", err.Error(), msg)
	}
}

func TestMain(m *testing.M) {
	flag.Parse()
	level := sglog.LevelInfo
	if !testing.Verbose() {
		log.SetOutput(io.Discard)
		level = sglog.LevelNone
	}

	logtest.InitWithLevel(m, level)
	os.Exit(m.Run())
}

func createTestNormalShard(t *testing.T, indexDir string, r zoekt.Repository, numShards int, optFns ...func(options *build.Options)) []string {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(indexDir), 0700); err != nil {
		t.Fatal(err)
	}

	o := build.Options{
		IndexDir:              indexDir,
		RepositoryDescription: r,
		ShardMax:              75, // create a new shard every 75 bytes
	}
	o.SetDefaults()

	for _, fn := range optFns {
		fn(&o)
	}

	b, err := build.NewBuilder(o)
	if err != nil {
		t.Fatalf("NewBuilder: %v", err)
	}

	if numShards == 0 {
		// We have to make at least 1 shard.
		numShards = 1
	}

	for i := 0; i < numShards; i++ {
		// Create entries (file + contents) that are ~100 bytes each.
		// This (along with our shardMax setting of 75 bytes) means that each shard
		// will contain at most one of these.
		fileName := strconv.Itoa(i)
		document := zoekt.Document{Name: fileName, Content: []byte(strings.Repeat("A", 100))}
		for _, branch := range o.RepositoryDescription.Branches {
			document.Branches = append(document.Branches, branch.Name)
		}

		err := b.Add(document)
		if err != nil {
			t.Fatalf("failed to add file %q to builder: %s", fileName, err)
		}
	}

	if err := b.Finish(); err != nil {
		t.Fatalf("Finish: %v", err)
	}

	return o.FindAllShards()
}

func createTestCompoundShard(t *testing.T, indexDir string, repositories []zoekt.Repository, optFns ...func(options *build.Options)) string {
	t.Helper()

	var shardNames []string

	for _, r := range repositories {
		// create an isolated scratch space to store normal shards for this repository
		scratchDir := t.TempDir()

		// create shards that'll be merged later
		createTestNormalShard(t, scratchDir, r, 1, optFns...)

		// discover file names for all the normal shards we created
		// note: this only looks in the immediate 'scratchDir' folder and doesn't recurse
		shards, err := filepath.Glob(filepath.Join(scratchDir, "*.zoekt"))
		if err != nil {
			t.Fatalf("while globbing %q to find normal shards: %s", scratchDir, err)
		}

		shardNames = append(shardNames, shards...)
	}

	// load the normal shards that we created
	var files []zoekt.IndexFile
	for _, shard := range shardNames {
		f, err := os.Open(shard)
		if err != nil {
			t.Fatalf("opening shard file: %s", err)
		}
		defer f.Close()

		indexFile, err := zoekt.NewIndexFile(f)
		if err != nil {
			t.Fatalf("creating index file: %s", err)
		}
		defer indexFile.Close()

		files = append(files, indexFile)
	}

	// merge all the simple shards into a compound shard
	tmpName, dstName, err := zoekt.Merge(indexDir, files...)
	if err != nil {
		t.Fatalf("merging index files into compound shard: %s", err)
	}
	if err := os.Rename(tmpName, dstName); err != nil {
		t.Fatal(err)
	}

	return dstName
}

func TestCreateEmptyShard(t *testing.T) {
	dir := t.TempDir()

	args := &indexArgs{
		IndexOptions: IndexOptions{
			RepoID:   7,
			Name:     "empty-repo",
			CloneURL: "code/host",
		},
		Incremental: true,
		IndexDir:    dir,
		Parallelism: 1,
		FileLimit:   1,
	}

	if err := createEmptyShard(args); err != nil {
		t.Fatal(err)
	}

	bo := args.BuildOptions()
	bo.RepositoryDescription.Branches = []zoekt.RepositoryBranch{{Name: "HEAD", Version: "404aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}}

	if got := bo.IncrementalSkipIndexing(); !got {
		t.Fatalf("wanted %t, got %t", true, got)
	}
}

func TestFormatListUint32(t *testing.T) {
	cases := []struct {
		in   []uint32
		want string
	}{
		{
			in:   []uint32{42, 8, 3},
			want: "42, 8, ...",
		},
		{
			in:   []uint32{42, 8},
			want: "42, 8",
		},
		{
			in:   []uint32{42},
			want: "42",
		},
		{
			in:   []uint32{},
			want: "",
		},
	}

	for _, tt := range cases {
		t.Run(fmt.Sprintf("%v", tt.in), func(t *testing.T) {
			out := formatListUint32(tt.in, 2)
			if out != tt.want {
				t.Fatalf("want %s, got %s", tt.want, out)
			}
		})
	}
}
