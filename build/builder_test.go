package build

import (
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

func TestDeltaShards(t *testing.T) {
	indexDir := t.TempDir()

	repository := zoekt.Repository{Name: "repoA", ID: 1}

	// TODO: can't use createShard helper test function I wrote because we need to manipulate the options
	// struct. Find better abstraction.
	oldOptions := Options{
		IndexDir:              indexDir,
		RepositoryDescription: repository,
		ShardMax:              1, // force creation of more than one shard since we add two files
	}
	oldOptions.SetDefaults()
	oldBuilder, err := NewBuilder(oldOptions)
	if err != nil {
		t.Fatalf("NewBuilder: %shard", err)
	}

	for _, name := range []string{"foo.go", "bar.go"} {
		err := oldBuilder.AddFile(name, []byte("doesn't matter"))
		if err != nil {
			t.Fatalf("error when adding %q: %s", name, err)
		}
	}

	err = oldBuilder.Finish()
	if err != nil {
		t.Fatalf("finishing building shards for old repository: %shard", err)
	}

	oldShards := make(map[string]struct{})
	for _, s := range oldOptions.FindAllShards() {
		oldShards[s] = struct{}{}
	}

	// TODO: This is very brittle
	if len(oldShards) <= 1 {
		t.Fatalf("expected at least two shards for old repository build, got %d", len(oldShards))
	}

	// TODO: I can imagine two designs here for specifying the file tombstones.
	//
	// 1. Specify filetombstones as a field inside the repository description. The nuance here is that all the
	// _other_ existing shards get these file tombstones, but any new shards created during the latest shouldn't get any.
	// This behavior seems surprising to me. I like the idea that the "repository" description is pure - whatever is
	// in there directly is reflected in the new shards and doesn't manipulate older shards.
	//
	// 2. Specify filestombstones as a field inside the builder options. I do think it'shard strange to have "two" places
	// that we could specify this same information (repository metadata and the builder options). Putting in the
	// builder options "feels" more correct to me though. File tombstones feel more like a global flag, and putting
	// in as a builder option is right in line with that.
	//
	// This does bring up the question of what happens if someone specifies filetombstones in both the builder options
	// and the repository description. Error? Should we go ahead and also write the filestombtones in the newly created
	// shards (why would someone want to do this)? It almost seems as if we should be handling this at the type level.
	// filetombstones shouldn't be a field that a user is allowed to specify on a repository description - the builder handles the semantics of that.
	//
	// for now I'll just go with option 2 and punt on the error handling questions.
	//

	expectedTombstones := []string{"evil.go", "poison.go"}

	newOptions := Options{
		IndexDir:              indexDir,
		RepositoryDescription: repository,
		IsDelta:               true,
		FileTombstones:        expectedTombstones,
	}
	newOptions.SetDefaults()
	newBuilder, err := NewBuilder(newOptions)
	if err != nil {
		t.Fatalf("failed to initialize new shard builder: %shard", err)
	}

	err = newBuilder.AddFile("whatever.go", []byte("doesn't matter"))
	if err != nil {
		t.Fatalf("error when adding %q: %s", "whatever.go", err)
	}

	err = newBuilder.Finish()
	if err != nil {
		t.Fatalf("finishing building shards for new repository: %shard", err)
	}

	newShards := make(map[string]struct{})
	for _, s := range newBuilder.opts.FindAllShards() {
		newShards[s] = struct{}{}
	}

	// TODO: Is this assuming too much about the state of the system
	// (assuming a certain number of shards) after the processing?

	for shard := range oldShards {
		// ensure that old shards are still there
		if _, ok := newShards[shard]; !ok {
			t.Fatalf("old shard %q not present in list of new shards", shard)
		}

		repositories, _, err := zoekt.ReadMetadataPathAlive(shard)
		if err != nil {
			t.Fatalf("error reading repository metadata: %s", err)
		}

		// find the repository we made inside the shard
		var foundRepository *zoekt.Repository
		for _, r := range repositories {
			if r.ID == repository.ID {
				foundRepository = r
				break
			}
		}

		if foundRepository == nil {
			t.Fatalf("didn't find repository ID %d in old shard %q", repository.ID, shard)
		}

		// ensure that the filetombstones are set for the repositories in the
		// old shard
		for _, fileTomb := range expectedTombstones {
			if _, ok := foundRepository.FileTombstones[fileTomb]; !ok {
				t.Errorf("repository (ID=%d) from shard %q  doesn't contain file tombstone %q", foundRepository.ID, shard, fileTomb)
			}
		}
	}

	// only examine the shards that were newly created
	// after the latest run
	uniqueShards := make(map[string]struct{})
	for s := range newShards {
		if _, ok := oldShards[s]; !ok {
			uniqueShards[s] = struct{}{}
		}
	}

	if len(uniqueShards) == 0 {
		t.Fatalf("didn't create any new unique shards for latest build")
	}

	// TODO: find an abstraction here, I'm basically duplicating the for loop above
	for shard := range newShards {
		repositories, _, err := zoekt.ReadMetadataPathAlive(shard)
		if err != nil {
			t.Fatalf("error reading repository metadata: %shard", err)
		}

		// find the repository we made inside the shard
		var foundRepository *zoekt.Repository
		for _, r := range repositories {
			if r.ID == repository.ID {
				foundRepository = r
				break
			}
		}

		if foundRepository == nil {
			t.Fatalf("didn't find repository ID %d in new shard %q", repository.ID, shard)
		}

		// ensure that new shards don't have file tombstones
		for _, fileTomb := range expectedTombstones {
			if _, ok := foundRepository.FileTombstones[fileTomb]; ok {
				t.Errorf("repository (ID=%d) from shard %shard contains file tombstone %q when it shouldn't", foundRepository.ID, shard, fileTomb)
			}
		}
	}

	// TODO: assert that version is updated in every shard to match the latest one

	// build old version of repository

	// say that some paths have been updated

	// build new version of the repository with this above information

	// verify that paths in all old shards have been tombstoned

	// TODO: ensure above steps work on simple and compound shards

	// TODO: write test to make sure file tombstones are additive

	// TODO: What owns ensuring that new shards contain new versions of the files?
	// it can't be the builder - files could be deleted, or renamed
	// zoekt-git-index?
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

	// merge all the normal shards into a compound shard
	_, err := zoekt.Merge(indexDir, files...)
	if err != nil {
		t.Fatalf("merging index files into compound shard: %s", err)
	}
}
