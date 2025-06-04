package index

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/require"

	"github.com/sourcegraph/zoekt"
)

var update = flag.Bool("update", false, "update golden file")

// ensure we don't regress on how we build v16
func TestBuildv16(t *testing.T) {
	dir := t.TempDir()

	opts := Options{
		IndexDir: dir,
		RepositoryDescription: zoekt.Repository{
			Name:     "repo",
			Source:   "./testdata/repo/",
			Metadata: map[string]string{"foo": "bar"},
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
	_, wantMetadata, err := ReadMetadataPath(wantP)
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
		err = os.WriteFile(wantP, data, 0o644)
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
	}, {
		// multiple large file pattern with negated pattern
		args: []string{"-large_file", "*.md", "-large_file", "!*.yaml"},
		want: Options{
			LargeFiles: []string{"*.md", "!*.yaml"},
		},
	}, {
		// multiple large file pattern with escaped character
		args: []string{"-large_file", "*.md", "-large_file", "\\!*.yaml"},
		want: Options{
			LargeFiles: []string{"*.md", "\\!*.yaml"},
		},
	}}

	ignored := []cmp.Option{
		// depends on $PATH setting.
		cmpopts.IgnoreFields(Options{}, "CTagsPath"),
		cmpopts.IgnoreFields(Options{}, "ScipCTagsPath"),
		cmpopts.IgnoreFields(Options{}, "changedOrRemovedFiles"),
		cmpopts.IgnoreFields(zoekt.Repository{}, "priority"),
	}

	for _, c := range cases {
		c.want.SetDefaults()
		// depends on $PATH setting.
		c.want.CTagsPath = ""

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
	err = b.Add(Document{
		Name:    "f1",
		Content: binary,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(b.todo) != 1 || b.todo[0].SkipReason == SkipReasonNone {
		t.Fatalf("document should have been skipped")
	}
	if b.todo[0].Content != nil {
		t.Fatalf("document content should be empty")
	}
	if b.size >= 100 {
		t.Fatalf("content of skipped documents should not count towards shard size thresold")
	}
}

func TestPartialSuccess(t *testing.T) {
	dir := t.TempDir()

	opts := Options{
		IndexDir:    dir,
		ShardMax:    1024,
		SizeMax:     1 << 20,
		Parallelism: 1,
	}
	opts.RepositoryDescription.Name = "repo"
	opts.SetDefaults()

	b, err := NewBuilder(opts)
	if err != nil {
		t.Fatalf("NewBuilder: %v", err)
	}

	for i := range 4 {
		nm := fmt.Sprintf("F%d", i)
		_ = b.AddFile(nm, []byte(strings.Repeat("01234567\n", 128)))
	}
	b.buildError = fmt.Errorf("any error")

	// No error checking.
	_ = b.Finish()

	// Finish cleans up temporary files.
	if fs, err := filepath.Glob(dir + "/*"); err != nil {
		t.Errorf("glob(%s): %v", dir, err)
	} else if len(fs) != 0 {
		t.Errorf("got shards %v, want []", fs)
	}
}

// Tests that we skip looping over repos in compound shards when we know that
// the repository we are looking for is not in the shard.
func TestSkipCompoundShards(t *testing.T) {
	metricCompoundShardLookups.Reset()

	compoundShards := [][]zoekt.Repository{
		{
			{Name: "repoA", ID: 1},
			{Name: "repoB", ID: 2},
			{Name: "repoC", ID: 3},
		},
		{
			{Name: "repoD", ID: 4},
			{Name: "repoE", ID: 5},
			{Name: "repoF", ID: 6},
			{Name: "repoF", ID: 7},
			{Name: "repoF", ID: 8},
		},
	}
	var lookForRepoID uint32 = 99
	wantSkippedCount := 2

	indexDir := t.TempDir()
	for _, repositoryGroup := range compoundShards {
		createTestCompoundShard(t, indexDir, repositoryGroup)
	}
	o := &Options{
		IndexDir:              indexDir,
		RepositoryDescription: zoekt.Repository{ID: lookForRepoID},
	}

	shard := o.findCompoundShard()
	require.Empty(t, shard)

	// Check if the "skipped" counter was incremented
	skippedCount := int(testutil.ToFloat64(metricCompoundShardLookups.WithLabelValues("skipped")))
	require.Equal(t, wantSkippedCount, skippedCount)
}

// With optimization
// BenchmarkFindCompoundShard-16    	   33505	     36016 ns/op
//
// Without optimization
// BenchmarkFindCompoundShard-16    	      76	  15568589 ns/op
func BenchmarkFindCompoundShard(b *testing.B) {
	// Generate a large compound shard
	const numRepos = 5000
	repositories := make([]zoekt.Repository, numRepos)
	for i := range numRepos {
		repositories[i] = zoekt.Repository{
			Name: fmt.Sprintf("repo%d", i+1),
			ID:   uint32(i + 1),
		}
	}
	indexDir := b.TempDir()
	createTestCompoundShard(b, indexDir, repositories)

	// pick id that is not in the shard
	var searchRepoID uint32 = numRepos + 1

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		o := &Options{
			IndexDir:              indexDir,
			RepositoryDescription: zoekt.Repository{ID: searchRepoID},
		}

		shard := o.findCompoundShard()
		if shard != "" {
			b.Fatal("expected empty result")
		}
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
			expectedRepository: zoekt.Repository{Name: "something-else", ID: 5},
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
					repos, _, err := ReadMetadataPathAlive(s)
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

func TestBuilder_DeltaShardsBuildsShouldErrorOnIndexOptionsMismatch(t *testing.T) {
	repository := zoekt.Repository{
		Name:     "repo",
		ID:       1,
		Branches: []zoekt.RepositoryBranch{{Name: "foo"}},
	}

	for _, test := range []struct {
		name    string
		options func(options *Options)
	}{
		{
			name:    "update option CTagsPath to non default",
			options: func(options *Options) { options.CTagsPath = "ctags_updated_test/universal-ctags" },
		},
		{
			name:    "update option DisableCTags to non default",
			options: func(options *Options) { options.DisableCTags = true },
		},
		{
			name:    "update option SizeMax to non default",
			options: func(options *Options) { options.SizeMax -= 10 },
		},
		{
			name:    "update option LargeFiles to non default",
			options: func(options *Options) { options.LargeFiles = []string{"-large_file", "*.md", "-large_file", "*.yaml"} },
		},
	} {
		test := test

		t.Run(test.name, func(t *testing.T) {
			indexDir := t.TempDir()

			// initially use default options
			createTestShard(t, indexDir, repository, 2)

			o := Options{
				IndexDir:              indexDir,
				RepositoryDescription: repository,
				IsDelta:               true,
			}
			test.options(&o)

			b, err := NewBuilder(o)
			if err != nil {
				t.Fatalf("NewBuilder: %v", err)
			}

			err = b.Finish()
			if err == nil {
				t.Fatalf("no error regarding index options mismatch")
			}

			var optionsMismatchError *deltaIndexOptionsMismatchError
			if !errors.As(err, &optionsMismatchError) {
				t.Fatalf("expected error complaining about index options mismatch, got: %s", err)
			}
		})
	}
}

func TestBuilder_DeltaShardsMetadataInOlderShards(t *testing.T) {
	olderTime := time.Unix(0, 0)
	newerTime := time.Unix(10000, 0)

	for _, test := range []struct {
		name               string
		originalRepository zoekt.Repository
		updatedRepository  zoekt.Repository
	}{
		{
			name: "update commit information",
			originalRepository: zoekt.Repository{
				Name: "repo",
				ID:   1,
				Branches: []zoekt.RepositoryBranch{
					{Name: "main", Version: "v1"},
					{Name: "release", Version: "v1"},
				},
			},
			updatedRepository: zoekt.Repository{
				Name: "repo",
				ID:   1,
				Branches: []zoekt.RepositoryBranch{
					{Name: "main", Version: "v2"},
					{Name: "release", Version: "v2"},
				},
			},
		},
		{
			name: "update latest commit date (older -> newer)",
			originalRepository: zoekt.Repository{
				Name: "repo",
				ID:   1,
				Branches: []zoekt.RepositoryBranch{
					{Name: "main", Version: "v1"},
				},
				LatestCommitDate: olderTime,
			},
			updatedRepository: zoekt.Repository{
				Name: "repo",
				ID:   1,
				Branches: []zoekt.RepositoryBranch{
					{Name: "main", Version: "v2"},
				},
				LatestCommitDate: newerTime,
			},
		},
		{
			name: "update latest commit date (even if latest commit date is older - the most recent commits are the source of truth)",
			originalRepository: zoekt.Repository{
				Name: "repo",
				ID:   1,
				Branches: []zoekt.RepositoryBranch{
					{Name: "main", Version: "v1"},
				},
				LatestCommitDate: newerTime,
			},
			updatedRepository: zoekt.Repository{
				Name: "repo",
				ID:   1,
				Branches: []zoekt.RepositoryBranch{
					{Name: "main", Version: "v2"},
				},
				LatestCommitDate: olderTime,
			},
		},
	} {
		test := test

		t.Run(test.name, func(t *testing.T) {
			indexDir := t.TempDir()

			createTestShard(t, indexDir, test.originalRepository, 2, func(o *Options) {
				o.DisableCTags = true
			})

			shards := createTestShard(t, indexDir, test.updatedRepository, 1, func(o *Options) {
				o.IsDelta = true
				o.DisableCTags = true
			})

			if len(shards) < 3 {
				t.Fatalf("expected at least 3 shards, got %d (%s)", len(shards), strings.Join(shards, ", "))
			}

			for _, s := range shards {
				repositories, _, err := ReadMetadataPathAlive(s)
				if err != nil {
					t.Fatalf("reading repository metadata from shard %q", s)
				}

				var foundRepository *zoekt.Repository
				for _, r := range repositories {
					if r.ID == test.updatedRepository.ID {
						foundRepository = r
						break
					}
				}

				if foundRepository == nil {
					t.Fatalf("repository ID %d not in shard %q", test.updatedRepository.ID, s)
				}

				diffOptions := []cmp.Option{
					cmpopts.IgnoreUnexported(zoekt.Repository{}),
					cmpopts.IgnoreFields(zoekt.Repository{}, "IndexOptions"),
					cmpopts.EquateEmpty(),
				}

				if diff := cmp.Diff(&test.updatedRepository, foundRepository, diffOptions...); diff != "" {
					t.Errorf("shard %q: unexpected diff in repository metadata (-want +got):\n%s", s, diff)
				}
			}
		})
	}
}

func TestFindRepositoryMetadata(t *testing.T) {
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

			optFns := []func(o *Options){
				// ctags aren't important for this test, and the equality checks
				// for diffing repositories can break due to local configuration
				func(o *Options) {
					o.DisableCTags = true
				},
			}

			for _, r := range tt.normalShardRepositories {
				createTestShard(t, indexDir, r, 1, optFns...)
			}

			if len(tt.compoundShardRepositories) > 0 {
				createTestCompoundShard(t, indexDir, tt.compoundShardRepositories, optFns...)
			}

			o := &Options{
				IndexDir:              indexDir,
				RepositoryDescription: *tt.input,
			}
			o.SetDefaults()

			// run test
			got, _, gotOk, err := o.FindRepositoryMetadata()
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

func createTestShard(t testing.TB, indexDir string, r zoekt.Repository, numShards int, optFns ...func(options *Options)) []string {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(indexDir), 0o700); err != nil {
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

	for i := range numShards {
		// Create entries (file + contents) that are ~100 bytes each.
		// This (along with our shardMax setting of 75 bytes) means that each shard
		// will contain at most one of these.
		fileName := strconv.Itoa(i)
		document := Document{Name: fileName, Content: []byte(strings.Repeat("A", 100))}
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

func createTestCompoundShard(t testing.TB, indexDir string, repositories []zoekt.Repository, optFns ...func(options *Options)) {
	t.Helper()

	var shardNames []string

	for _, r := range repositories {
		// create an isolated scratch space to store normal shards for this repository
		scratchDir := t.TempDir()

		// create shards that'll be merged later
		createTestShard(t, scratchDir, r, 1, optFns...)

		// discover file names for all the normal shards we created
		// note: this only looks in the immediate 'scratchDir' folder and doesn't recurse
		shards, err := filepath.Glob(filepath.Join(scratchDir, "*.zoekt"))
		if err != nil {
			t.Fatalf("while globbing %q to find normal shards: %s", scratchDir, err)
		}

		shardNames = append(shardNames, shards...)
	}

	// load the normal shards that we created
	var files []IndexFile
	for _, shard := range shardNames {
		f, err := os.Open(shard)
		if err != nil {
			t.Fatalf("opening shard file: %s", err)
		}
		defer f.Close()

		indexFile, err := NewIndexFile(f)
		if err != nil {
			t.Fatalf("creating index file: %s", err)
		}
		defer indexFile.Close()

		files = append(files, indexFile)
	}

	// merge all the simple shards into a compound shard
	tmpName, dstName, err := Merge(indexDir, files...)
	if err != nil {
		t.Fatalf("merging index files into compound shard: %s", err)
	}
	if err := os.Rename(tmpName, dstName); err != nil {
		t.Fatal(err)
	}
}

func TestIgnoreSizeMax(t *testing.T) {
	for _, test := range []struct {
		name       string
		largeFiles []string
		filePaths  []string
		expected   bool
	}{
		{
			name:       "empty pattern does nothing",
			largeFiles: []string{""},
			filePaths:  []string{"F0"},
			expected:   false,
		},
		{
			name:       "positive match allows",
			largeFiles: []string{"F0"},
			filePaths:  []string{"F0"},
			expected:   true,
		},
		{
			name:       "positive and negative patterns allows",
			largeFiles: []string{"F?", "!F0"},
			filePaths:  []string{"F1"},
			expected:   true,
		},
		{
			name:       "positive and negative patterns disallows",
			largeFiles: []string{"F?", "!F0"},
			filePaths:  []string{"F0"},
			expected:   false,
		},
		{
			name:       "positive escaped pattern allows",
			largeFiles: []string{"\\!F?"},
			filePaths:  []string{"!F0", "!F1"},
			expected:   true,
		},
		{
			name:       "postive escaped pattern does not disallow",
			largeFiles: []string{"F0", "\\!F?"},
			filePaths:  []string{"F0", "!F0"},
			expected:   true,
		},
		{
			name:       "combined meta and literal interpretation disallows",
			largeFiles: []string{"*F*", "!!F*"},
			filePaths:  []string{"!F0"},
			expected:   false,
		},
		{
			name:       "combined meta and literal interpretation allows",
			largeFiles: []string{"*F*", "!!F*"},
			filePaths:  []string{"F0"},
			expected:   true,
		},
		{
			name:       "largeFiles order: positive match overrides previous negative match and allows",
			largeFiles: []string{"F?", "!F0", "!F1", "F0"},
			filePaths:  []string{"F0"},
			expected:   true,
		},
		{
			name:       "largeFiles order: positive match overrides previous negative match and disallows",
			largeFiles: []string{"F?", "!F0", "!F1", "F0"},
			filePaths:  []string{"F1"},
			expected:   false,
		},
		{
			name:       "largeFiles order: negative match overrides previous positive match and allows",
			largeFiles: []string{"F?", "!?0", "F0", "!F0"},
			filePaths:  []string{"F1"},
			expected:   true,
		},
		{
			name:       "largeFiles order: negative match overrides previous positive match and disallows",
			largeFiles: []string{"F?", "!?0", "F0", "!F0"},
			filePaths:  []string{"F0"},
			expected:   false,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			o := Options{
				LargeFiles: test.largeFiles,
			}

			for _, filePath := range test.filePaths {
				ignore := o.IgnoreSizeMax(filePath)
				if ignore != test.expected {
					t.Errorf("IgnoreSizeMax() for filepath %v returned unexpected result %v", filePath, ignore)
				}
			}
		})
	}
}

type filerankCase struct {
	name string
	docs []*Document
	want []int
}

func testFileRankAspect(t *testing.T, c filerankCase) {
	var want []*Document
	for _, j := range c.want {
		want = append(want, c.docs[j])
	}

	got := make([]*Document, len(c.docs))
	copy(got, c.docs)
	sortDocuments(got)

	print := func(ds []*Document) string {
		r := ""
		for _, d := range ds {
			r += fmt.Sprintf("%v, ", d)
		}
		return r
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got docs [%v], want [%v]", print(got), print(want))
	}
}

func TestFileRank(t *testing.T) {
	for _, c := range []filerankCase{{
		name: "filename",
		docs: []*Document{
			{
				Name:    "longlonglong",
				Content: []byte("bla"),
			},
			{
				Name:    "short",
				Content: []byte("bla"),
			},
		},
		want: []int{1, 0},
	}, {
		name: "test",
		docs: []*Document{
			{
				Name:    "foo_test.go",
				Content: []byte("bla"),
			},
			{
				Name:    "longlonglong",
				Content: []byte("bla"),
			},
		},
		want: []int{1, 0},
	}, {
		name: "content",
		docs: []*Document{
			{
				Content: []byte("bla"),
			},
			{
				Content: []byte("blablablabla"),
			},
			{
				Content: []byte("blabla"),
			},
		},
		want: []int{0, 2, 1},
	}, {
		name: "skipped docs",
		docs: []*Document{
			{
				Name:       "binary_file",
				SkipReason: SkipReasonBinary,
			},
			{
				Name:    "some_test.go",
				Content: []byte("bla"),
			},
			{
				Name:       "large_file.go",
				SkipReason: SkipReasonTooLarge,
			},
			{
				Name:    "file.go",
				Content: []byte("blabla"),
			},
		},
		want: []int{3, 1, 0, 2},
	}} {
		t.Run(c.name, func(t *testing.T) {
			testFileRankAspect(t, c)
		})
	}
}

func TestOptions_shardName(t *testing.T) {
	opts := Options{
		IndexDir: "/data",
		RepositoryDescription: zoekt.Repository{
			Name:     "a/b",
			TenantID: 123,
			ID:       456,
		},
	}

	t.Setenv("WORKSPACES_API_URL", "")
	if got, want := opts.shardNameVersion(16, 0), "/data/a%2Fb_v16.00000.zoekt"; got != want {
		t.Fatalf("expected shard name to be repo name based:\ngot:  %q\nwant: %q", got, want)
	}

	t.Setenv("WORKSPACES_API_URL", "http://example.com")
	if got, want := opts.shardNameVersion(16, 0), "/data/000000123_000000456_v16.00000.zoekt"; got != want {
		t.Fatalf("expected shard name to be ID based:\ngot:  %q\nwant: %q", got, want)
	}

	// If something goes wrong and TenantID and RepoID is unset, we create a
	// name which won't be visible by any tenant.
	opts = Options{
		IndexDir: "/data",
		RepositoryDescription: zoekt.Repository{
			Name: "a/b",
		},
	}
	if got, want := opts.shardNameVersion(16, 0), "/data/000000000_000000000_v16.00000.zoekt"; got != want {
		t.Fatalf("expected shard name to be with no tenant:\ngot:  %q\nwant: %q", got, want)
	}
}
