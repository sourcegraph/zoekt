package index

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/sourcegraph/zoekt"
	"github.com/sourcegraph/zoekt/query"
)

func TestSetTombstone(t *testing.T) {
	mockRepos = mkRepos("r1", "r2", "r3")

	readMeta := func(shard string) []byte {
		blob, err := os.ReadFile(shard + ".meta")
		if err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
		return blob
	}

	dir := t.TempDir()
	ghostShard := filepath.Join(dir, "test.zoekt")

	isAlive := func(alive []bool) {
		t.Helper()
		blob := readMeta(ghostShard)
		ghostRepos := []*zoekt.Repository{}
		if err := json.Unmarshal(blob, &ghostRepos); err != nil {
			t.Fatal(err)
		}
		for i, repo := range ghostRepos {
			if repo.Tombstone == alive[i] {
				t.Fatalf("r%d: want %t, got %t\n", i+1, alive[i], repo.Tombstone)
			}
		}
	}

	if err := SetTombstone(ghostShard, 2); err != nil {
		t.Fatal(err)
	}
	isAlive([]bool{true, false, true})

	if err := SetTombstone(ghostShard, 1); err != nil {
		t.Fatal(err)
	}
	isAlive([]bool{false, false, true})

	if err := UnsetTombstone(ghostShard, 2); err != nil {
		t.Fatal(err)
	}
	isAlive([]bool{false, true, true})
}

func mkRepos(repoNames ...string) []*zoekt.Repository {
	ret := make([]*zoekt.Repository, 0, len(repoNames))
	for i, n := range repoNames {
		ret = append(ret, &zoekt.Repository{ID: uint32(i + 1), Name: n})
	}
	return ret
}

func TestInitTombstonedDocsReleasesMapAndFiltersSearch(t *testing.T) {
	dir := t.TempDir()
	repo := zoekt.Repository{
		Name:     "repo",
		ID:       1,
		Branches: []zoekt.RepositoryBranch{{Name: "main", Version: "v1"}},
	}

	opts := Options{
		IndexDir:              dir,
		RepositoryDescription: repo,
		DisableCTags:          true,
	}
	opts.SetDefaults()

	b, err := NewBuilder(opts)
	if err != nil {
		t.Fatalf("NewBuilder: %v", err)
	}

	const totalDocs = 70
	for i := range totalDocs {
		name := fmt.Sprintf("file_%03d.go", i)
		if err := b.Add(Document{
			Name:     name,
			Content:  []byte(fmt.Sprintf("package p\nconst token_%03d = \"needle\"\n", i)),
			Branches: []string{"main"},
		}); err != nil {
			t.Fatalf("Add(%s): %v", name, err)
		}
	}
	if err := b.Finish(); err != nil {
		t.Fatalf("Finish: %v", err)
	}

	shards := opts.FindAllShards()
	if len(shards) != 1 {
		t.Fatalf("expected 1 shard, got %d", len(shards))
	}
	shardPath := shards[0]

	metaRepo := repo
	metaRepo.FileTombstones = map[string]struct{}{
		"file_000.go": {},
		"file_065.go": {},
	}
	for i := range 2000 {
		metaRepo.FileTombstones[fmt.Sprintf("unrelated_shard_file_%04d.go", i)] = struct{}{}
	}
	tmp, final, err := JsonMarshalRepoMetaTemp(shardPath, &metaRepo)
	if err != nil {
		t.Fatalf("JsonMarshalRepoMetaTemp: %v", err)
	}
	if err := os.Rename(tmp, final); err != nil {
		t.Fatalf("Rename meta: %v", err)
	}

	f, err := os.Open(shardPath)
	if err != nil {
		t.Fatalf("Open shard: %v", err)
	}
	defer func() { _ = f.Close() }()

	inf, err := NewIndexFile(f)
	if err != nil {
		t.Fatalf("NewIndexFile: %v", err)
	}

	s, err := NewSearcher(inf)
	if err != nil {
		inf.Close()
		t.Fatalf("NewSearcher: %v", err)
	}
	defer s.Close()

	d := s.(*indexData)
	if d.repoMetaData[0].FileTombstones != nil {
		t.Fatalf("expected d.repoMetaData[0].FileTombstones to be nil after initTombstonedDocs, got len=%d", len(d.repoMetaData[0].FileTombstones))
	}
	if d.repoListEntry[0].Repository.FileTombstones != nil {
		t.Fatalf("expected d.repoListEntry[0].Repository.FileTombstones to be nil, got len=%d", len(d.repoListEntry[0].Repository.FileTombstones))
	}
	if len(d.tombstonedDocs) != 2 {
		t.Fatalf("expected tombstonedDocs bitset length 2 for %d docs, got %d", totalDocs, len(d.tombstonedDocs))
	}
	if d.isDocTombstoned(999) {
		t.Fatalf("out-of-range doc should not be tombstoned")
	}

	sr, err := s.Search(context.Background(), &query.Substring{Pattern: "needle"}, &zoekt.SearchOptions{})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(sr.Files) != totalDocs-2 {
		t.Fatalf("expected %d matching files, got %d", totalDocs-2, len(sr.Files))
	}
	for _, fm := range sr.Files {
		if fm.FileName == "file_000.go" || fm.FileName == "file_065.go" {
			t.Fatalf("tombstoned file %q returned in search results", fm.FileName)
		}
	}
}

func TestBuilderDeltaPrunesUnrelatedFileTombstones(t *testing.T) {
	dir := t.TempDir()
	repo := zoekt.Repository{
		Name:     "repo",
		ID:       1,
		Branches: []zoekt.RepositoryBranch{{Name: "main", Version: "v1"}},
	}

	opts := Options{
		IndexDir:              dir,
		RepositoryDescription: repo,
		DisableCTags:          true,
		ShardMax:              100,
	}
	opts.SetDefaults()

	b, err := NewBuilder(opts)
	if err != nil {
		t.Fatalf("NewBuilder: %v", err)
	}
	docs := []Document{
		{Name: "shard0_keep.go", Content: []byte(strings.Repeat("a", 60)), Branches: []string{"main"}},
		{Name: "shard0_drop.go", Content: []byte(strings.Repeat("b", 60)), Branches: []string{"main"}},
		{Name: "shard1_drop.go", Content: []byte(strings.Repeat("c", 60)), Branches: []string{"main"}},
	}
	for _, doc := range docs {
		if err := b.Add(doc); err != nil {
			t.Fatalf("Add(%s): %v", doc.Name, err)
		}
	}
	if err := b.Finish(); err != nil {
		t.Fatalf("Finish initial: %v", err)
	}

	oldShards := opts.FindAllShards()
	if len(oldShards) < 2 {
		t.Fatalf("expected at least 2 shards, got %d", len(oldShards))
	}

	// Pre-populate shard 0 metadata with stale unrelated tombstones to verify compaction on update.
	repos0, _, err := ReadMetadataPathAlive(oldShards[0])
	if err != nil {
		t.Fatalf("ReadMetadataPathAlive: %v", err)
	}
	repos0[0].FileTombstones = map[string]struct{}{
		"stale_foreign_file.go": {},
	}
	tmp, final, err := JsonMarshalRepoMetaTemp(oldShards[0], repos0[0])
	if err != nil {
		t.Fatalf("JsonMarshalRepoMetaTemp: %v", err)
	}
	if err := os.Rename(tmp, final); err != nil {
		t.Fatalf("Rename meta: %v", err)
	}

	deltaOpts := opts
	deltaOpts.IsDelta = true
	deltaOpts.RepositoryDescription.Branches = []zoekt.RepositoryBranch{{Name: "main", Version: "v2"}}

	db, err := NewBuilder(deltaOpts)
	if err != nil {
		t.Fatalf("NewBuilder delta: %v", err)
	}
	db.MarkFileAsChangedOrRemoved("shard0_drop.go")
	db.MarkFileAsChangedOrRemoved("shard1_drop.go")
	db.MarkFileAsChangedOrRemoved("deleted_never_indexed.go")
	if err := db.Add(Document{
		Name:     "shard0_drop.go",
		Content:  []byte("updated content"),
		Branches: []string{"main"},
	}); err != nil {
		t.Fatalf("Add delta doc: %v", err)
	}
	if err := db.Finish(); err != nil {
		t.Fatalf("Finish delta: %v", err)
	}

	wantPerShard := []map[string]struct{}{
		{"shard0_drop.go": {}},
		{"shard1_drop.go": {}},
	}
	if len(oldShards) != len(wantPerShard) {
		t.Fatalf("expected %d old shards, got %d", len(wantPerShard), len(oldShards))
	}
	for i, shard := range oldShards {
		repos, _, err := ReadMetadataPathAlive(shard)
		if err != nil {
			t.Fatalf("ReadMetadataPathAlive(%s): %v", shard, err)
		}
		got := repos[0].FileTombstones
		if diff := cmp.Diff(wantPerShard[i], got); diff != "" {
			t.Fatalf("shard %s FileTombstones mismatch (-want +got):\n%s", shard, diff)
		}
	}
}

func TestMergeAndExplodePreserveFileTombstones(t *testing.T) {
	buildRepoShard := func(id uint32, name string, files []string, tombstones map[string]struct{}) IndexFile {
		t.Helper()
		dir := t.TempDir()
		repo := zoekt.Repository{
			Name:     name,
			ID:       id,
			Branches: []zoekt.RepositoryBranch{{Name: "main", Version: "v1"}},
		}
		opts := Options{
			IndexDir:              dir,
			RepositoryDescription: repo,
			DisableCTags:          true,
		}
		opts.SetDefaults()

		b, err := NewBuilder(opts)
		if err != nil {
			t.Fatalf("NewBuilder(%s): %v", name, err)
		}
		for _, fn := range files {
			if err := b.Add(Document{
				Name:     fn,
				Content:  []byte("package p\nconst sharedNeedle = 1\n"),
				Branches: []string{"main"},
			}); err != nil {
				t.Fatalf("Add(%s/%s): %v", name, fn, err)
			}
		}
		if err := b.Finish(); err != nil {
			t.Fatalf("Finish(%s): %v", name, err)
		}

		shards := opts.FindAllShards()
		if len(shards) != 1 {
			t.Fatalf("expected 1 shard for %s, got %d", name, len(shards))
		}
		shardPath := shards[0]

		if len(tombstones) > 0 {
			repos, _, err := ReadMetadataPathAlive(shardPath)
			if err != nil {
				t.Fatalf("ReadMetadataPathAlive(%s): %v", name, err)
			}
			repos[0].FileTombstones = tombstones
			tmp, final, err := JsonMarshalRepoMetaTemp(shardPath, repos[0])
			if err != nil {
				t.Fatalf("JsonMarshalRepoMetaTemp(%s): %v", name, err)
			}
			if err := os.Rename(tmp, final); err != nil {
				t.Fatalf("Rename meta(%s): %v", name, err)
			}
		}

		f, err := os.Open(shardPath)
		if err != nil {
			t.Fatalf("Open(%s): %v", name, err)
		}
		inf, err := NewIndexFile(f)
		if err != nil {
			_ = f.Close()
			t.Fatalf("NewIndexFile(%s): %v", name, err)
		}
		t.Cleanup(func() { inf.Close() })
		return inf
	}

	// repo-a has "alive.go" and "shared.go" (tombstoned).
	// repo-b has "shared.go" (alive).
	infA := buildRepoShard(1, "repo-a", []string{"alive.go", "shared.go"}, map[string]struct{}{"shared.go": {}})
	infB := buildRepoShard(2, "repo-b", []string{"shared.go"}, nil)

	mergeDir := t.TempDir()
	tmpCompound, dstCompound, err := Merge(mergeDir, infA, infB)
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	if err := os.Rename(tmpCompound, dstCompound); err != nil {
		t.Fatalf("Rename compound: %v", err)
	}

	// 1. Verify search directly on the compound shard before Explode.
	cf, err := os.Open(dstCompound)
	if err != nil {
		t.Fatalf("Open compound: %v", err)
	}
	cinf, err := NewIndexFile(cf)
	if err != nil {
		_ = cf.Close()
		t.Fatalf("NewIndexFile compound: %v", err)
	}
	cs, err := NewSearcher(cinf)
	if err != nil {
		cinf.Close()
		t.Fatalf("NewSearcher compound: %v", err)
	}
	csr, err := cs.Search(context.Background(), &query.Substring{Pattern: "sharedNeedle"}, &zoekt.SearchOptions{})
	cs.Close()
	if err != nil {
		t.Fatalf("Search compound: %v", err)
	}
	gotCompound := map[string][]string{}
	for _, fm := range csr.Files {
		gotCompound[fm.Repository] = append(gotCompound[fm.Repository], fm.FileName)
	}
	wantCompound := map[string][]string{
		"repo-a": {"alive.go"},
		"repo-b": {"shared.go"},
	}
	if diff := cmp.Diff(wantCompound, gotCompound); diff != "" {
		t.Fatalf("compound search mismatch (-want +got):\n%s", diff)
	}

	// 2. Explode the compound shard and verify per-repo metadata and search results.
	explodeDir := t.TempDir()
	if err := Explode(explodeDir, dstCompound); err != nil {
		t.Fatalf("Explode: %v", err)
	}

	exploded, err := filepath.Glob(filepath.Join(explodeDir, "*.zoekt"))
	if err != nil {
		t.Fatalf("Glob exploded: %v", err)
	}
	if len(exploded) != 2 {
		t.Fatalf("expected 2 exploded shards, got %v", exploded)
	}

	gotExploded := map[string][]string{}
	gotTombstones := map[string]map[string]struct{}{}
	for _, path := range exploded {
		repos, _, err := ReadMetadataPathAlive(path)
		if err != nil {
			t.Fatalf("ReadMetadataPathAlive(%s): %v", path, err)
		}
		gotTombstones[repos[0].Name] = repos[0].FileTombstones

		ef, err := os.Open(path)
		if err != nil {
			t.Fatalf("Open exploded(%s): %v", path, err)
		}
		einf, err := NewIndexFile(ef)
		if err != nil {
			_ = ef.Close()
			t.Fatalf("NewIndexFile exploded(%s): %v", path, err)
		}
		es, err := NewSearcher(einf)
		if err != nil {
			einf.Close()
			t.Fatalf("NewSearcher exploded(%s): %v", path, err)
		}
		sr, err := es.Search(context.Background(), &query.Substring{Pattern: "sharedNeedle"}, &zoekt.SearchOptions{})
		es.Close()
		if err != nil {
			t.Fatalf("Search exploded(%s): %v", path, err)
		}
		for _, fm := range sr.Files {
			gotExploded[fm.Repository] = append(gotExploded[fm.Repository], fm.FileName)
		}
	}

	if diff := cmp.Diff(wantCompound, gotExploded); diff != "" {
		t.Fatalf("exploded search mismatch (-want +got):\n%s", diff)
	}
	wantTombstones := map[string]map[string]struct{}{
		"repo-a": {"shared.go": {}},
		"repo-b": nil,
	}
	if diff := cmp.Diff(wantTombstones, gotTombstones); diff != "" {
		t.Fatalf("exploded FileTombstones mismatch (-want +got):\n%s", diff)
	}
}
