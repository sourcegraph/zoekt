package build

import (
	"flag"
	"io"
	"log"
	"os"
	"path/filepath"
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

func TestRepositoryMetadata(t *testing.T) {
	tests := []struct {
		name                      string
		normalShardRepositories   []*zoekt.Repository
		compoundShardRepositories []*zoekt.Repository
		input                     *zoekt.Repository
		expected                  *zoekt.Repository
	}{
		{
			name: "repository in normal shards",
			normalShardRepositories: []*zoekt.Repository{
				{Name: "repoA", ID: 1},
				{Name: "repoB", ID: 2},
				{Name: "repoC", ID: 3},
			},
			compoundShardRepositories: []*zoekt.Repository{
				{Name: "repoD", ID: 4},
				{Name: "repoE", ID: 5},
				{Name: "repoF", ID: 6},
			},
			input:    &zoekt.Repository{Name: "repoB", ID: 2},
			expected: &zoekt.Repository{Name: "repoB", ID: 2},
		},
		{
			name: "repository in compound shards",
			normalShardRepositories: []*zoekt.Repository{
				{Name: "repoA", ID: 1},
				{Name: "repoB", ID: 2},
				{Name: "repoC", ID: 3},
			},
			compoundShardRepositories: []*zoekt.Repository{
				{Name: "repoD", ID: 4},
				{Name: "repoE", ID: 5},
				{Name: "repoF", ID: 6},
			},
			input:    &zoekt.Repository{Name: "repoE", ID: 5},
			expected: &zoekt.Repository{Name: "repoE", ID: 5},
		},
		{
			name: "repository not in any shard",
			normalShardRepositories: []*zoekt.Repository{
				{Name: "repoA", ID: 1},
				{Name: "repoB", ID: 2},
				{Name: "repoC", ID: 3},
			},
			compoundShardRepositories: []*zoekt.Repository{
				{Name: "repoD", ID: 4},
				{Name: "repoE", ID: 5},
				{Name: "repoF", ID: 6},
			},
			input:    &zoekt.Repository{Name: "notPresent", ID: 123},
			expected: nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// setup
			indexDir := t.TempDir()

			for _, r := range tt.normalShardRepositories {
				createTestShard(t, indexDir, r)
			}

			if len(tt.compoundShardRepositories) > 0 {
				createTestCompoundShard(t, indexDir, tt.compoundShardRepositories)
			}

			o := &Options{
				IndexDir:              indexDir,
				RepositoryDescription: *tt.input,
			}
			o.SetDefaults()

			// run test
			got, err := o.RepositoryMetadata()
			if err != nil {
				t.Errorf("received unexpected error: %v", err)
				return
			}

			// check outcome
			compareOptions := []cmp.Option{
				cmpopts.IgnoreUnexported(zoekt.Repository{}),
				cmpopts.IgnoreFields(zoekt.Repository{}, "IndexOptions"),
				cmpopts.EquateEmpty(),
			}

			if diff := cmp.Diff(tt.expected, got, compareOptions...); diff != "" {
				t.Errorf("unexpected difference (-want +got):\n%s", diff)
			}
		})
	}
}

func createTestShard(t *testing.T, indexDir string, r *zoekt.Repository) (shards []string) {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(indexDir), 0700); err != nil {
		t.Fatal(err)
	}

	o := Options{
		IndexDir:              indexDir,
		RepositoryDescription: *r,
	}
	o.SetDefaults()

	b, err := NewBuilder(o)
	if err != nil {
		t.Fatalf("NewBuilder: %v", err)
	}

	if err := b.Finish(); err != nil {
		t.Fatalf("Finish: %v", err)
	}

	return o.FindAllShards()
}

func createTestCompoundShard(t *testing.T, indexDir string, repositories []*zoekt.Repository) {
	t.Helper()

	var shardNames []string

	for _, r := range repositories {
		shardNames = append(shardNames, createTestShard(t, indexDir, r)...)
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

	_, err := zoekt.Merge(indexDir, files...)
	if err != nil {
		t.Fatalf("merging index files into compound shard: %s", err)
	}

}
