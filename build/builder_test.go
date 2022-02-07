package build

import (
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log"
	"os"
	"path/filepath"
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

	type fields struct {
		IndexDir              string
		SizeMax               int
		Parallelism           int
		ShardMax              int
		TrigramMax            int
		RepositoryDescription zoekt.Repository
		SubRepositories       map[string]*zoekt.Repository
		DisableCTags          bool
		CTags                 string
		CTagsMustSucceed      bool
		MemProfile            string
		LargeFiles            []string
	}
	tests := []struct {
		name                    string
		fields                  fields
		normalShardRepositories []zoekt.Repository
		compoundShards          [][]zoekt.Repository
		expectedShardCount      int
		expectedRepository      zoekt.Repository
	}{
		{
			name: "single normal shard",
			normalShardRepositories: []zoekt.Repository{
				{Name: "repoA", ID: 1},
			},
			expectedShardCount: 1,
			expectedRepository: zoekt.Repository{Name: "repoA", ID: 1},
		},
		{
			name: "single compound shard",
			compoundShards: [][]zoekt.Repository{
				{
					{Name: "repoA", ID: 1},
					{Name: "repoB", ID: 2},
					{Name: "repoC", ID: 3},
				},
			},
			expectedShardCount: 1,
			expectedRepository: zoekt.Repository{Name: "repoB", ID: 2},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			indexDir := t.TempDir()

			for _, r := range tt.normalShardRepositories {
				createTestShard(t, indexDir, r)
			}

			for _, repositoryGroup := range tt.compoundShards {
				createTestCompoundShard(t, indexDir, repositoryGroup)
			}

			o := &Options{
				IndexDir:              indexDir,
				RepositoryDescription: tt.expectedRepository,
			}
			o.SetDefaults()

			shards := o.FindAllShards()
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

func createTestShard(t *testing.T, indexDir string, r zoekt.Repository) {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(indexDir), 0700); err != nil {
		t.Fatal(err)
	}

	o := Options{
		IndexDir:              indexDir,
		RepositoryDescription: r,
	}
	o.SetDefaults()

	b, err := NewBuilder(o)
	if err != nil {
		t.Fatalf("NewBuilder: %v", err)
	}

	if err := b.Finish(); err != nil {
		t.Fatalf("Finish: %v", err)
	}
}

func createTestCompoundShard(t *testing.T, indexDir string, repositories []zoekt.Repository) {
	t.Helper()

	// create a stash space to create normal shards in
	normalShardIndexDir := t.TempDir()

	for _, r := range repositories {
		createTestShard(t, normalShardIndexDir, r)
	}

	var shardNames []string

	// walk through stash space to discover
	// file names for all the normal shards we created
	err := fs.WalkDir(os.DirFS(normalShardIndexDir), ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return fmt.Errorf("processing %q: %s", path, err)
		}

		if d.IsDir() || !strings.HasSuffix(d.Name(), ".zoekt") {
			return nil
		}

		shardNames = append(shardNames, filepath.Join(normalShardIndexDir, path))

		return nil
	})
	if err != nil {
		t.Fatalf("while walking %q to find normal shards: %s", normalShardIndexDir, err)
	}

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

	compoundShard, err := zoekt.Merge(normalShardIndexDir, files...)
	if err != nil {
		t.Fatalf("merging index files into compound shard: %s", err)
	}

	// move compound shard from scratch directory to final index
	// directory
	err = os.Rename(compoundShard, filepath.Join(indexDir, filepath.Base(compoundShard)))
	if err != nil {
		t.Fatalf("failed to move compound shard %q to index directory %q: %s", compoundShard, indexDir, err)
	}

}
