package build

import (
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/google/zoekt"
)

var update = flag.Bool("update", false, "update golden file")

// ensure we don't regress on how we build v16
func TestBuildv16(t *testing.T) {
	dir := t.TempDir()

	opts := Options{
		IndexDir: dir,
		RepositoryDescription: zoekt.Repository{
			Name:   "repo",
			Source: "./testdata/repo/",
		},
		DisableCTags: true,
	}
	opts.SetDefaults()

	b, err := NewBuilder(opts)
	if err != nil {
		t.Fatal(err)
	}

	for _, p := range []string{"main.go"} {
		blob, err := os.ReadFile(filepath.Join("../testdata/repo", p))
		if err != nil {
			t.Fatal(err)
		}
		if err := b.AddFile(p, blob); err != nil {
			t.Fatal(err)
		}
	}

	wantP := filepath.Join("../testdata/shards", "repo_v16.00000.zoekt")

	// fields indexTime and id depend on time. For this test, we copy the fields from
	// the old shard.
	_, wantMetadata, err := zoekt.ReadMetadataPath(wantP)
	if err != nil {
		t.Fatal(err)
	}
	b.indexTime = wantMetadata.IndexTime
	b.id = wantMetadata.ID

	if err := b.Finish(); err != nil {
		t.Fatal(err)
	}

	gotP := filepath.Join(dir, "repo_v16.00000.zoekt")

	if *update {
		data, err := os.ReadFile(gotP)
		if err != nil {
			t.Fatal(err)
		}
		err = os.WriteFile(wantP, data, 0644)
		if err != nil {
			t.Fatal(err)
		}
		return
	}

	got, err := os.ReadFile(gotP)
	if err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile(wantP)
	if err != nil {
		t.Fatal(err)
	}

	if d := cmp.Diff(want, got); d != "" {
		t.Errorf("mismatch (-want +got):\n%s", d)
	}
}

func TestFlags(t *testing.T) {
	cases := []struct {
		args []string
		want Options
	}{{
		// Defaults
		args: []string{},
		want: Options{},
	}, {
		args: []string{"-index", "/tmp"},
		want: Options{
			IndexDir: "/tmp",
		},
	}, {
		// single large file pattern
		args: []string{"-large_file", "*.md"},
		want: Options{
			LargeFiles: []string{"*.md"},
		},
	}, {
		// multiple large file pattern
		args: []string{"-large_file", "*.md", "-large_file", "*.yaml"},
		want: Options{
			LargeFiles: []string{"*.md", "*.yaml"},
		},
	}}

	ignored := []cmp.Option{
		// depends on $PATH setting.
		cmpopts.IgnoreFields(Options{}, "CTags"),
		cmpopts.IgnoreFields(zoekt.Repository{}, "priority"),
	}

	for _, c := range cases {
		c.want.SetDefaults()
		// depends on $PATH setting.
		c.want.CTags = ""

		got := Options{}
		fs := flag.NewFlagSet("", flag.ContinueOnError)
		got.Flags(fs)
		if err := fs.Parse(c.args); err != nil {
			t.Errorf("failed to parse args %v: %v", c.args, err)
		} else if d := cmp.Diff(c.want, got, ignored...); d != "" {
			t.Errorf("mismatch for %v (-want +got):\n%s", c.args, d)
		}
	}
}

func TestIncrementalSkipIndexing(t *testing.T) {
	cases := []struct {
		name string
		want bool
		opts Options
	}{{
		name: "v17-noop",
		want: true,
		opts: Options{
			RepositoryDescription: zoekt.Repository{
				Name: "repo17",
			},
			SizeMax:      2097152,
			DisableCTags: true,
		},
	}, {
		name: "v16-noop",
		want: true,
		opts: Options{
			RepositoryDescription: zoekt.Repository{
				Name: "repo",
			},
			SizeMax:      2097152,
			DisableCTags: true,
		},
	}, {
		name: "v17-id",
		want: false,
		opts: Options{
			RepositoryDescription: zoekt.Repository{
				Name: "repo17",
				RawConfig: map[string]string{
					"repoid": "123",
				},
			},
			SizeMax:      2097152,
			DisableCTags: true,
		},
	}, {
		name: "doesnotexist",
		want: false,
		opts: Options{
			RepositoryDescription: zoekt.Repository{
				Name: "doesnotexist",
			},
			SizeMax:      2097152,
			DisableCTags: true,
		},
	}}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tc.opts.IndexDir = "../testdata/shards"
			t.Log(tc.opts.IndexState())
			got := tc.opts.IncrementalSkipIndexing()
			if got != tc.want {
				t.Fatalf("want %v got %v", tc.want, got)
			}
		})
	}
}

func TestMain(m *testing.M) {
	flag.Parse()
	if !testing.Verbose() {
		log.SetOutput(io.Discard)
	}
	os.Exit(m.Run())
}

func TestDontCountContentOfSkippedFiles(t *testing.T) {
	b, err := NewBuilder(Options{RepositoryDescription: zoekt.Repository{
		Name: "foo",
	}})
	if err != nil {
		t.Fatal(err)
	}

	// content with at least 100 bytes
	binary := append([]byte("abc def \x00"), make([]byte, 100)...)
	err = b.Add(zoekt.Document{
		Name:    "f1",
		Content: binary,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(b.todo) != 1 || b.todo[0].SkipReason == "" {
		t.Fatalf("document should have been skipped")
	}
	if b.size >= 100 {
		t.Fatalf("content of skipped documents should not count towards shard size thresold")
	}
}

func TestOptions_FindAllShards(t *testing.T) {
	type normalShard struct {
		Repository zoekt.Repository
		// NumShards is the number of shards that should be created that
		// contain data for "Repository".
		NumShards int
	}

	tests := []struct {
		name               string
		normalShards       []normalShard
		compoundShards     [][]zoekt.Repository
		expectedShardCount int
		expectedRepository zoekt.Repository
	}{
		{
			name: "repository in normal shard",
			normalShards: []normalShard{
				{Repository: zoekt.Repository{Name: "repoA", ID: 1}},
				{Repository: zoekt.Repository{Name: "repoB", ID: 2}},
				{Repository: zoekt.Repository{Name: "repoC", ID: 3}},
			},
			expectedShardCount: 1,
			expectedRepository: zoekt.Repository{Name: "repoB", ID: 2},
		},
		{
			name: "repository in compound shard",
			compoundShards: [][]zoekt.Repository{
				{
					{Name: "repoA", ID: 1},
					{Name: "repoB", ID: 2},
					{Name: "repoC", ID: 3},
				},
				{
					{Name: "repoD", ID: 4},
					{Name: "repoE", ID: 5},
					{Name: "repoF", ID: 6},
				},
			},
			expectedShardCount: 1,
			expectedRepository: zoekt.Repository{Name: "repoB", ID: 2},
		},
		{
			name: "repository split across multiple shards",
			normalShards: []normalShard{
				{Repository: zoekt.Repository{Name: "repoA", ID: 1}},
				{Repository: zoekt.Repository{Name: "repoB", ID: 2}, NumShards: 2},
				{Repository: zoekt.Repository{Name: "repoC", ID: 3}},
			},
			expectedShardCount: 2,
			expectedRepository: zoekt.Repository{Name: "repoB", ID: 2},
		},
		{
			name: "unknown repository",
			normalShards: []normalShard{
				{Repository: zoekt.Repository{Name: "repoA", ID: 1}},
				{Repository: zoekt.Repository{Name: "repoB", ID: 2}},
				{Repository: zoekt.Repository{Name: "repoC", ID: 3}},
			},
			compoundShards: [][]zoekt.Repository{
				{
					{Name: "repoD", ID: 4},
					{Name: "repoE", ID: 5},
					{Name: "repoF", ID: 6},
				},
			},
			expectedShardCount: 0,
		},
		{
			name: "match on ID, not name",
			normalShards: []normalShard{
				{Repository: zoekt.Repository{Name: "sameName", ID: 1}},
				{Repository: zoekt.Repository{Name: "sameName", ID: 2}},
				{Repository: zoekt.Repository{Name: "sameName", ID: 3}},
			},
			compoundShards: [][]zoekt.Repository{
				{
					{Name: "sameName", ID: 4},
					{Name: "sameName", ID: 5},
					{Name: "sameName", ID: 6},
				},
				{
					{Name: "sameName", ID: 7},
					{Name: "sameName", ID: 8},
					{Name: "sameName", ID: 9},
				},
			},
			expectedShardCount: 1,
			expectedRepository: zoekt.Repository{Name: "sameName", ID: 5},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// prepare
			indexDir := t.TempDir()

			for _, s := range tt.normalShards {
				createTestShard(t, indexDir, s.Repository, s.NumShards)
			}

			for _, repositoryGroup := range tt.compoundShards {
				createTestCompoundShard(t, indexDir, repositoryGroup)
			}

			o := &Options{
				IndexDir:              indexDir,
				RepositoryDescription: tt.expectedRepository,
			}
			o.SetDefaults()

			// run test
			shards := o.FindAllShards()

			// verify results
			if len(shards) != tt.expectedShardCount {
				t.Fatalf("expected %d shard(s), received %d shard(s)", tt.expectedShardCount, len(shards))
			}

			if tt.expectedShardCount > 0 {
				for _, s := range shards {
					// all shards should contain the metadata for the desired repository
					repos, _, err := zoekt.ReadMetadataPathAlive(s)
					if err != nil {
						t.Fatalf("reading metadata from shard %q: %s", s, err)
					}

					foundRepository := false
					for _, r := range repos {
						if r.ID == tt.expectedRepository.ID {
							foundRepository = true
							break
						}
					}

					if !foundRepository {
						t.Errorf("shard %q doesn't contain metadata for repository %d", s, tt.expectedRepository.ID)
					}
				}
			}
		})
	}
}

func createTestShard(t *testing.T, indexDir string, r zoekt.Repository, numShards int) {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(indexDir), 0700); err != nil {
		t.Fatal(err)
	}

	o := Options{
		IndexDir:              indexDir,
		RepositoryDescription: r,
		ShardMax:              75, // create a new shard every 75 bytes
	}
	o.SetDefaults()

	b, err := NewBuilder(o)
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
		contents := []byte(strings.Repeat("A", 100))

		err := b.AddFile(fileName, contents)
		if err != nil {
			t.Fatalf("failed to add file %q to builder: %s", fileName, err)
		}
	}

	if err := b.Finish(); err != nil {
		t.Fatalf("Finish: %v", err)
	}
}

func createTestCompoundShard(t *testing.T, indexDir string, repositories []zoekt.Repository) {
	t.Helper()

	// create a scratch space to store normal shards
	scratchDir := t.TempDir()

	for _, r := range repositories {
		// create normal shards that'll be merged later
		createTestShard(t, scratchDir, r, 1)
	}

	var shardNames []string

	// walk through scratch space to discover
	// file names for all the normal shards we created
	err := fs.WalkDir(os.DirFS(scratchDir), ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return fmt.Errorf("processing %q: %s", path, err)
		}

		if d.IsDir() || !strings.HasSuffix(d.Name(), ".zoekt") {
			return nil
		}

		s := filepath.Join(scratchDir, path)
		shardNames = append(shardNames, s)
		return nil
	})
	if err != nil {
		t.Fatalf("while walking %q to find normal shards: %s", scratchDir, err)
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

	// merge all the normal shards into a compound shard
	scratchCompoundShard, err := zoekt.Merge(scratchDir, files...)
	if err != nil {
		t.Fatalf("merging index files into compound shard: %s", err)
	}

	// move compound shard from scratch directory to final index directory
	finalShard := filepath.Join(indexDir, filepath.Base(scratchCompoundShard))
	err = os.Rename(scratchCompoundShard, finalShard)
	if err != nil {
		t.Fatalf("failed to move compound shard %q to index directory %q: %s", scratchCompoundShard, indexDir, err)
	}
}
