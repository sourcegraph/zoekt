package build

import (
	"errors"
	"flag"
	"io"
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
	type simpleShard struct {
		Repository zoekt.Repository
		// NumShards is the number of shards that should be created that
		// contain data for "Repository".
		NumShards int
	}

	tests := []struct {
		name               string
		simpleShards       []simpleShard
		compoundShards     [][]zoekt.Repository
		expectedShardCount int
		expectedRepository zoekt.Repository
	}{
		{
			name: "repository in normal shard",
			simpleShards: []simpleShard{
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
			simpleShards: []simpleShard{
				{Repository: zoekt.Repository{Name: "repoA", ID: 1}},
				{Repository: zoekt.Repository{Name: "repoB", ID: 2}, NumShards: 2},
				{Repository: zoekt.Repository{Name: "repoC", ID: 3}},
			},
			expectedShardCount: 2,
			expectedRepository: zoekt.Repository{Name: "repoB", ID: 2},
		},
		{
			name: "unknown repository",
			simpleShards: []simpleShard{
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
			name: "match on ID, not name (compound only)",
			compoundShards: [][]zoekt.Repository{
				{
					{Name: "repoA", ID: 1},
					{Name: "sameName", ID: 2},
					{Name: "sameName", ID: 3},
				},
				{
					{Name: "repoB", ID: 4},
					{Name: "sameName", ID: 5},
					{Name: "sameName", ID: 6},
				},
			},
			expectedShardCount: 1,
			expectedRepository: zoekt.Repository{Name: "sameName", ID: 5},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// prepare
			indexDir := t.TempDir()

			for _, s := range tt.simpleShards {
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

func TestBuilder_BranchNamesEqual(t *testing.T) {
	for i, test := range []struct {
		oldBranches []zoekt.RepositoryBranch
		newBranches []zoekt.RepositoryBranch
		expected    bool
	}{
		{
			oldBranches: []zoekt.RepositoryBranch{{Name: "main", Version: "v1"}, {Name: "release", Version: "v1"}},
			newBranches: []zoekt.RepositoryBranch{{Name: "main", Version: "v1"}, {Name: "release", Version: "v1"}},
			expected:    true,
		},
		{
			oldBranches: []zoekt.RepositoryBranch{{Name: "main", Version: "v1"}, {Name: "release", Version: "v3"}},
			newBranches: []zoekt.RepositoryBranch{{Name: "main", Version: "v2"}, {Name: "release", Version: "v4"}},
			expected:    true,
		},
		{
			oldBranches: []zoekt.RepositoryBranch{{Name: "main", Version: "v1"}},
			newBranches: []zoekt.RepositoryBranch{{Name: "main", Version: "v2"}, {Name: "release", Version: "v1"}},
			expected:    false,
		},
		{
			oldBranches: []zoekt.RepositoryBranch{{Name: "main", Version: "v1"}},
			newBranches: []zoekt.RepositoryBranch{{Name: "release", Version: "v1"}},
			expected:    false,
		},
		{
			oldBranches: []zoekt.RepositoryBranch{{Name: "main", Version: "v1"}},
			newBranches: []zoekt.RepositoryBranch{},
			expected:    false,
		},
		{
			oldBranches: []zoekt.RepositoryBranch{},
			newBranches: []zoekt.RepositoryBranch{{Name: "main", Version: "v1"}},
			expected:    false,
		},
	} {
		t.Run(strconv.Itoa(i), func(t *testing.T) {
			actual := BranchNamesEqual(test.oldBranches, test.newBranches)
			if test.expected != actual {
				t.Errorf("expected: %t, got: %t", test.expected, actual)
			}
		})
	}
}

func TestBuilder_DeltaShardsBuildsShouldErrorOnBranchSet(t *testing.T) {
	indexDir := t.TempDir()

	repository := zoekt.Repository{
		Name:     "repo",
		ID:       1,
		Branches: []zoekt.RepositoryBranch{{Name: "foo"}, {Name: "bar"}},
	}
	createTestShard(t, indexDir, repository, 2)

	repositoryNewBranches := zoekt.Repository{
		Name:     "repo",
		ID:       1,
		Branches: []zoekt.RepositoryBranch{{Name: "foo"}, {Name: "baz"}},
	}

	o := Options{
		IndexDir:              indexDir,
		RepositoryDescription: repositoryNewBranches,
		IsDelta:               true,
	}
	o.SetDefaults()

	b, err := NewBuilder(o)
	if err != nil {
		t.Fatalf("NewBuilder: %v", err)
	}

	err = b.Finish()
	if !errors.As(err, &deltaBranchSetError{}) {
		t.Fatalf("expected error complaning about different branch names, got: %s", err)
	}
}

func TestBuilder_DeltaShardsUpdateVersionsInOlderShards(t *testing.T) {
	indexDir := t.TempDir()

	repositoryV1 := zoekt.Repository{
		Name: "repo",
		ID:   1,
		Branches: []zoekt.RepositoryBranch{
			{Name: "main", Version: "v1"},
			{Name: "release", Version: "v1"},
		},
	}

	createTestShard(t, indexDir, repositoryV1, 2)

	repositoryV2 := zoekt.Repository{
		Name: "repo",
		ID:   1,
		Branches: []zoekt.RepositoryBranch{
			{Name: "main", Version: "v2"},
			{Name: "release", Version: "v2"},
		},
	}

	shards := createTestShard(t, indexDir, repositoryV2, 1, func(o *Options) {
		o.IsDelta = true
	})

	if len(shards) < 3 {
		t.Fatalf("expected at least 3 shards, got %d (%s)", len(shards), strings.Join(shards, ", "))
	}

	for _, s := range shards {
		repositories, _, err := zoekt.ReadMetadataPathAlive(s)
		if err != nil {
			t.Fatalf("reading repository metadata from shard %q", s)
		}

		var foundRepository *zoekt.Repository
		for _, r := range repositories {
			if r.ID == repositoryV2.ID {
				foundRepository = r
				break
			}
		}

		if foundRepository == nil {
			t.Fatalf("repository ID %d not in shard %q", repositoryV2.ID, s)
		}

		diffOptions := []cmp.Option{
			cmpopts.IgnoreUnexported(zoekt.Repository{}),
			cmpopts.IgnoreFields(zoekt.Repository{}, "IndexOptions", "HasSymbols"),
			cmpopts.EquateEmpty(),
		}

		if diff := cmp.Diff(&repositoryV2, foundRepository, diffOptions...); diff != "" {
			t.Errorf("shard %q: unexpected diff in repository metadata (-want +got):\n%s", s, diff)
		}
	}
}

func TestRepositoryMetadata(t *testing.T) {
	tests := []struct {
		name                      string
		normalShardRepositories   []zoekt.Repository
		compoundShardRepositories []zoekt.Repository
		input                     *zoekt.Repository
		expectedRepository        *zoekt.Repository
		expectedOk                bool
	}{
		{
			name: "repository in normal shards",
			normalShardRepositories: []zoekt.Repository{
				{Name: "repoA", ID: 1},
				{Name: "repoB", ID: 2},
				{Name: "repoC", ID: 3},
			},
			compoundShardRepositories: []zoekt.Repository{
				{Name: "repoD", ID: 4},
				{Name: "repoE", ID: 5},
				{Name: "repoF", ID: 6},
			},
			input:              &zoekt.Repository{Name: "repoB", ID: 2},
			expectedRepository: &zoekt.Repository{Name: "repoB", ID: 2},
			expectedOk:         true,
		},
		{
			name: "repository in compound shards",
			normalShardRepositories: []zoekt.Repository{
				{Name: "repoA", ID: 1},
				{Name: "repoB", ID: 2},
				{Name: "repoC", ID: 3},
			},
			compoundShardRepositories: []zoekt.Repository{
				{Name: "repoD", ID: 4},
				{Name: "repoE", ID: 5},
				{Name: "repoF", ID: 6},
			},
			input:              &zoekt.Repository{Name: "repoE", ID: 5},
			expectedRepository: &zoekt.Repository{Name: "repoE", ID: 5},
			expectedOk:         true,
		},
		{
			name: "repository not in any shard",
			normalShardRepositories: []zoekt.Repository{
				{Name: "repoA", ID: 1},
				{Name: "repoB", ID: 2},
				{Name: "repoC", ID: 3},
			},
			compoundShardRepositories: []zoekt.Repository{
				{Name: "repoD", ID: 4},
				{Name: "repoE", ID: 5},
				{Name: "repoF", ID: 6},
			},
			input:              &zoekt.Repository{Name: "notPresent", ID: 123},
			expectedRepository: nil,
			expectedOk:         false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// setup
			indexDir := t.TempDir()

			for _, r := range tt.normalShardRepositories {
				createTestShard(t, indexDir, r, 1)
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
			got, gotOk, err := o.RepositoryMetadata()
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

			if diff := cmp.Diff(tt.expectedRepository, got, compareOptions...); diff != "" {
				t.Errorf("unexpected difference in repositories (-want +got):\n%s", diff)
			}

			if tt.expectedOk != gotOk {
				t.Errorf("unexpected difference in 'ok' value: wanted %t, got %t", tt.expectedOk, gotOk)
			}
		})
	}
}

func createTestShard(t *testing.T, indexDir string, r zoekt.Repository, numShards int, optFns ...func(options *Options)) []string {
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

	for _, fn := range optFns {
		fn(&o)
	}

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

func createTestCompoundShard(t *testing.T, indexDir string, repositories []zoekt.Repository) {
	t.Helper()

	var shardNames []string

	for _, r := range repositories {
		// create an isolated scratch space to store normal shards for this repository
		scratchDir := t.TempDir()

		// create shards that'll be merged later
		createTestShard(t, scratchDir, r, 1)

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
}
