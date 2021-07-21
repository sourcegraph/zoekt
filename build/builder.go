// Copyright 2016 Google Inc. All rights reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//    http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// package build implements a more convenient interface for building
// zoekt indices.
package build

import (
	"crypto/sha1"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"math"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"runtime/pprof"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/bmatcuk/doublestar"
	"github.com/google/zoekt"
	"github.com/google/zoekt/ctags"
	"gopkg.in/natefinch/lumberjack.v2"
)

var DefaultDir = filepath.Join(os.Getenv("HOME"), ".zoekt")

// Branch describes a single branch version.
type Branch struct {
	Name    string
	Version string
}

// Options sets options for the index building.
type Options struct {
	// IndexDir is a directory that holds *.zoekt index files.
	IndexDir string

	// SizeMax is the maximum file size
	SizeMax int

	// Parallelism is the maximum number of shards to index in parallel
	Parallelism int

	// ShardMax sets the maximum corpus size for a single shard
	ShardMax int

	// TrigramMax sets the maximum number of distinct trigrams per document.
	TrigramMax int

	// RepositoryDescription holds names and URLs for the repository.
	RepositoryDescription zoekt.Repository

	// SubRepositories is a path => sub repository map.
	SubRepositories map[string]*zoekt.Repository

	// DisableCTags disables the generation of ctags metadata.
	DisableCTags bool

	// Path to exuberant ctags binary to run
	CTags string

	// If set, ctags must succeed.
	CTagsMustSucceed bool

	// Write memory profiles to this file.
	MemProfile string

	// LargeFiles is a slice of glob patterns, including ** for any number
	// of directories, where matching file paths should be indexed
	// regardless of their size. The full pattern syntax is here:
	// https://github.com/bmatcuk/doublestar/tree/v1#patterns.
	LargeFiles []string
}

// HashOptions creates a hash of the options that affect an index.
func (o *Options) HashOptions() string {
	hasher := sha1.New()

	hasher.Write([]byte(o.CTags))
	hasher.Write([]byte(fmt.Sprintf("%t", o.CTagsMustSucceed)))
	hasher.Write([]byte(fmt.Sprintf("%d", o.SizeMax)))
	hasher.Write([]byte(fmt.Sprintf("%q", o.LargeFiles)))
	hasher.Write([]byte(fmt.Sprintf("%t", o.DisableCTags)))

	return fmt.Sprintf("%x", hasher.Sum(nil))
}

type largeFilesFlag struct{ *Options }

func (f largeFilesFlag) String() string {
	// From flag.Value documentation:
	//
	// The flag package may call the String method with a zero-valued receiver,
	// such as a nil pointer.
	if f.Options == nil {
		return ""
	}
	s := append([]string{""}, f.LargeFiles...)
	return strings.Join(s, "-large_file ")
}

func (f largeFilesFlag) Set(value string) error {
	f.LargeFiles = append(f.LargeFiles, value)
	return nil
}

// Flags adds flags for build options to fs. It is the "inverse" of Args.
func (o *Options) Flags(fs *flag.FlagSet) {
	x := *o
	x.SetDefaults()
	fs.IntVar(&o.SizeMax, "file_limit", x.SizeMax, "maximum file size")
	fs.IntVar(&o.TrigramMax, "max_trigram_count", x.TrigramMax, "maximum number of trigrams per document")
	fs.IntVar(&o.ShardMax, "shard_limit", x.ShardMax, "maximum corpus size for a shard")
	fs.IntVar(&o.Parallelism, "parallelism", x.Parallelism, "maximum number of parallel indexing processes.")
	fs.StringVar(&o.IndexDir, "index", x.IndexDir, "directory for search indices")
	fs.BoolVar(&o.CTagsMustSucceed, "require_ctags", x.CTagsMustSucceed, "If set, ctags calls must succeed.")
	fs.Var(largeFilesFlag{o}, "large_file", "A glob pattern where matching files are to be index regardless of their size. You can add multiple patterns by setting this more than once.")

	// Sourcegraph specific
	fs.BoolVar(&o.DisableCTags, "disable_ctags", x.DisableCTags, "If set, ctags will not be called.")
}

// Args generates command line arguments for o. It is the "inverse" of Flags.
func (o *Options) Args() []string {
	var args []string

	if o.SizeMax != 0 {
		args = append(args, "-file_limit", strconv.Itoa(o.SizeMax))
	}

	if o.TrigramMax != 0 {
		args = append(args, "-max_trigram_count", strconv.Itoa(o.TrigramMax))
	}

	if o.ShardMax != 0 {
		args = append(args, "-shard_limit", strconv.Itoa(o.ShardMax))
	}

	if o.Parallelism != 0 {
		args = append(args, "-parallelism", strconv.Itoa(o.Parallelism))
	}

	if o.IndexDir != "" {
		args = append(args, "-index", o.IndexDir)
	}

	if o.CTagsMustSucceed {
		args = append(args, "-require_ctags")
	}

	for _, a := range o.LargeFiles {
		args = append(args, "-large_file", a)
	}

	// Sourcegraph specific
	if o.DisableCTags {
		args = append(args, "-disable_ctags")
	}

	return args
}

// Builder manages (parallel) creation of uniformly sized shards. The
// builder buffers up documents until it collects enough documents and
// then builds a shard and writes.
type Builder struct {
	opts     Options
	throttle chan int

	nextShardNum int
	todo         []*zoekt.Document
	size         int

	parser ctags.Parser

	building sync.WaitGroup

	errMu      sync.Mutex
	buildError error

	// temp name => final name for finished shards. We only rename
	// them once all shards succeed to avoid Frankstein corpuses.
	finishedShards map[string]string

	shardLogger io.WriteCloser
}

type finishedShard struct {
	temp, final string
}

// SetDefaults sets reasonable default options.
func (o *Options) SetDefaults() {
	if o.CTags == "" {
		if ctags := os.Getenv("CTAGS_COMMAND"); ctags != "" {
			o.CTags = ctags
		}
	}

	if o.CTags == "" {
		ctags, err := exec.LookPath("universal-ctags")
		if err == nil {
			o.CTags = ctags
		}
	}

	if o.CTags == "" {
		ctags, err := exec.LookPath("ctags-exuberant")
		if err == nil {
			o.CTags = ctags
		}
	}

	if o.Parallelism == 0 {
		o.Parallelism = 4
	}
	if o.SizeMax == 0 {
		o.SizeMax = 2 << 20
	}
	if o.ShardMax == 0 {
		o.ShardMax = 100 << 20
	}
	if o.TrigramMax == 0 {
		o.TrigramMax = 20000
	}

	if o.RepositoryDescription.Name == "" && o.RepositoryDescription.URL != "" {
		parsed, _ := url.Parse(o.RepositoryDescription.URL)
		if parsed != nil {
			o.RepositoryDescription.Name = filepath.Join(parsed.Host, parsed.Path)
		}
	}
}

func hashString(s string) string {
	h := sha1.New()
	io.WriteString(h, s)
	return fmt.Sprintf("%x", h.Sum(nil))
}

// ShardName returns the name the given index shard.
func (o *Options) ShardName(n int) string {
	abs := url.QueryEscape(o.RepositoryDescription.Name)
	if len(abs) > 200 {
		abs = abs[:200] + hashString(abs)[:8]
	}
	return filepath.Join(o.IndexDir,
		fmt.Sprintf("%s_v%d.%05d.zoekt", abs, zoekt.IndexFormatVersion, n))
}

type IndexState string

const (
	IndexStateMissing IndexState = "missing"
	IndexStateCorrupt            = "corrupt"
	IndexStateVersion            = "version-mismatch"
	IndexStateOption             = "option-mismatch"
	IndexStateMeta               = "meta-mismatch"
	IndexStateContent            = "content-mismatch"
	IndexStateEqual              = "equal"
)

// IncrementalSkipIndexing returns true if the index present on disk matches
// the build options.
func (o *Options) IncrementalSkipIndexing() bool {
	return o.IndexState() == IndexStateEqual
}

// IndexState checks how the index present on disk compares to the build
// options.
func (o *Options) IndexState() IndexState {
	fn := o.ShardName(0)

	repo, index, err := zoekt.ReadMetadataPath(fn)
	if os.IsNotExist(err) {
		return IndexStateMissing
	} else if err != nil {
		return IndexStateCorrupt
	}

	if index.IndexFeatureVersion != zoekt.FeatureVersion {
		return IndexStateVersion
	}

	if repo.IndexOptions != o.HashOptions() {
		return IndexStateOption
	}

	if !reflect.DeepEqual(repo.Branches, o.RepositoryDescription.Branches) {
		return IndexStateContent
	}

	// We can mutate repo since it lives in the scope of this function call.
	if updated, err := repo.MergeMutable(&o.RepositoryDescription); err != nil {
		// non-nil err means we are trying to update an immutable field =>
		// reindex content.
		log.Printf("warn: immutable field changed, requires re-index: %s", err)
		return IndexStateContent
	} else if updated {
		return IndexStateMeta
	}

	return IndexStateEqual
}

// IgnoreSizeMax determines whether the max size should be ignored.
func (o *Options) IgnoreSizeMax(name string) bool {
	for _, pattern := range o.LargeFiles {
		pattern = strings.TrimSpace(pattern)
		m, _ := doublestar.PathMatch(pattern, name)
		if m {
			return true
		}
	}

	return false
}

// NewBuilder creates a new Builder instance.
func NewBuilder(opts Options) (*Builder, error) {
	opts.SetDefaults()
	if opts.RepositoryDescription.Name == "" {
		return nil, fmt.Errorf("builder: must set Name")
	}

	b := &Builder{
		opts:           opts,
		throttle:       make(chan int, opts.Parallelism),
		finishedShards: map[string]string{},
	}

	if b.opts.DisableCTags {
		b.opts.CTags = ""
	}

	if b.opts.CTags == "" && b.opts.CTagsMustSucceed {
		return nil, fmt.Errorf("ctags binary not found, but CTagsMustSucceed set")
	}

	if strings.Contains(opts.CTags, "universal-ctags") {
		parser, err := ctags.NewParser(opts.CTags)
		if err != nil && opts.CTagsMustSucceed {
			return nil, fmt.Errorf("ctags.NewParser: %v", err)
		}

		b.parser = parser
	}

	b.shardLogger = &lumberjack.Logger{
		Filename:   filepath.Join(opts.IndexDir, "zoekt-builder-shard-log.tsv"),
		MaxSize:    100, // Megabyte
		MaxBackups: 5,
	}

	if _, err := b.newShardBuilder(); err != nil {
		return nil, err
	}

	return b, nil
}

// AddFile is a convenience wrapper for the Add method
func (b *Builder) AddFile(name string, content []byte) error {
	return b.Add(zoekt.Document{Name: name, Content: content})
}

func (b *Builder) Add(doc zoekt.Document) error {
	allowLargeFile := b.opts.IgnoreSizeMax(doc.Name)

	// Adjust trigramMax for allowed large files so we don't exclude them.
	trigramMax := b.opts.TrigramMax
	if allowLargeFile {
		trigramMax = math.MaxInt64
	}

	if len(doc.Content) > b.opts.SizeMax && !allowLargeFile {
		// We could pass the document on to the shardbuilder, but if
		// we pass through a part of the source tree with binary/large
		// files, the corresponding shard would be mostly empty, so
		// insert a reason here too.
		doc.SkipReason = fmt.Sprintf("document size %d larger than limit %d", len(doc.Content), b.opts.SizeMax)
	} else if err := zoekt.CheckText(doc.Content, trigramMax); err != nil {
		doc.SkipReason = err.Error()
		doc.Language = "binary"
	}

	b.todo = append(b.todo, &doc)
	b.size += len(doc.Name) + len(doc.Content)
	if b.size > b.opts.ShardMax {
		return b.flush()
	}

	return nil
}

// Finish creates a last shard from the buffered documents, and clears
// stale shards from previous runs. This should always be called, also
// in failure cases, to ensure cleanup.
func (b *Builder) Finish() error {
	defer b.shardLogger.Close()

	b.flush()
	b.building.Wait()

	if b.buildError != nil {
		for tmp := range b.finishedShards {
			log.Printf("Builder.Finish %s", tmp)
			os.Remove(tmp)
		}
		b.finishedShards = map[string]string{}
		return b.buildError
	}

	for tmp, final := range b.finishedShards {
		if err := os.Rename(tmp, final); err != nil {
			b.buildError = err
			continue
		}

		b.shardLog("upsert", final)

		// Remove extra files unrelated to the new shard. We don't want the old
		// meta file sticking around.
		paths, err := zoekt.IndexFilePaths(final)
		if err != nil {
			b.buildError = err
		}
		for _, p := range paths {
			if p == final {
				continue
			}
			log.Printf("removing old shard file: %s", p)
			if err := os.Remove(p); err != nil {
				b.buildError = err
			}
		}
	}
	b.finishedShards = map[string]string{}

	if b.nextShardNum > 0 {
		if err := b.deleteRemainingShards(); err != nil {
			log.Printf("failed to delete some old shards: %v", err)
		}
	}
	return b.buildError
}

func (b *Builder) deleteRemainingShards() error {
	for {
		shard := b.nextShardNum
		b.nextShardNum++
		name := b.opts.ShardName(shard)
		paths, err := zoekt.IndexFilePaths(name)
		if err != nil {
			return err
		}
		if len(paths) == 0 {
			return nil
		}
		for _, p := range paths {
			err := os.Remove(p)
			if err != nil {
				return err
			}
		}
		b.shardLog("remove", name)
	}
}

func (b *Builder) flush() error {
	todo := b.todo
	b.todo = nil
	b.size = 0
	b.errMu.Lock()
	defer b.errMu.Unlock()
	if b.buildError != nil {
		return b.buildError
	}

	hasShard := b.nextShardNum > 0
	if len(todo) == 0 && hasShard {
		return nil
	}

	shard := b.nextShardNum
	b.nextShardNum++

	if b.opts.Parallelism > 1 {
		b.building.Add(1)
		go func() {
			b.throttle <- 1
			done, err := b.buildShard(todo, shard)
			<-b.throttle

			b.errMu.Lock()
			defer b.errMu.Unlock()
			if err != nil && b.buildError == nil {
				b.buildError = err
			}
			if err == nil {
				b.finishedShards[done.temp] = done.final
			}
			b.building.Done()
		}()
	} else {
		// No goroutines when we're not parallel. This
		// simplifies memory profiling.
		done, err := b.buildShard(todo, shard)
		b.buildError = err
		if err == nil {
			b.finishedShards[done.temp] = done.final
		}
		if b.opts.MemProfile != "" {
			// drop memory, and profile.
			todo = nil
			b.writeMemProfile(b.opts.MemProfile)
		}

		return b.buildError
	}

	return nil
}

func (b *Builder) shardLog(action, shard string) {
	shard = filepath.Base(shard)
	var shardSize int64
	if fi, err := os.Stat(filepath.Join(b.opts.IndexDir, shard)); err == nil {
		shardSize = fi.Size()
	}
	_, _ = fmt.Fprintf(b.shardLogger, "%d\t%s\t%s\t%d\n", time.Now().UTC().Unix(), action, shard, shardSize)
}

var profileNumber int

func (b *Builder) writeMemProfile(name string) {
	nm := fmt.Sprintf("%s.%d", name, profileNumber)
	profileNumber++
	f, err := os.Create(nm)
	if err != nil {
		log.Fatal("could not create memory profile: ", err)
	}
	runtime.GC() // get up-to-date statistics
	if err := pprof.WriteHeapProfile(f); err != nil {
		log.Fatal("could not write memory profile: ", err)
	}
	f.Close()
	log.Printf("wrote mem profile %q", nm)
}

// map [0,inf) to [0,1) monotonically
func squashRange(j int) float64 {
	x := float64(j)
	return x / (1 + x)
}

var testRe = regexp.MustCompile("test")

type rankedDoc struct {
	*zoekt.Document
	rank []float64
}

func rank(d *zoekt.Document, origIdx int) []float64 {
	test := 0.0
	if testRe.MatchString(d.Name) {
		test = 1.0
	}

	// Smaller is earlier (=better).
	return []float64{
		// Prefer docs that are not tests
		test,

		// With many symbols
		1.0 - squashRange(len(d.Symbols)),

		// With short content
		squashRange(len(d.Content)),

		// With short names
		squashRange(len(d.Name)),

		// That is present is as many branches as possible
		1.0 - squashRange(len(d.Branches)),

		// Preserve original ordering.
		squashRange(origIdx),
	}
}

func sortDocuments(todo []*zoekt.Document) {
	rs := make([]rankedDoc, 0, len(todo))
	for i, t := range todo {
		rd := rankedDoc{t, rank(t, i)}
		rs = append(rs, rd)
	}
	sort.Slice(rs, func(i, j int) bool {
		r1 := rs[i].rank
		r2 := rs[j].rank
		for i := range r1 {
			if r1[i] < r2[i] {
				return true
			}
			if r1[i] > r2[i] {
				return false
			}
		}

		return false
	})
	for i := range todo {
		todo[i] = rs[i].Document
	}
}

func (b *Builder) buildShard(todo []*zoekt.Document, nextShardNum int) (*finishedShard, error) {
	if b.opts.CTags != "" {
		err := ctagsAddSymbols(todo, b.parser, b.opts.CTags)
		if b.opts.CTagsMustSucceed && err != nil {
			return nil, err
		}
		if err != nil {
			log.Printf("ignoring %s error: %v", b.opts.CTags, err)
		}
	}

	name := b.opts.ShardName(nextShardNum)

	shardBuilder, err := b.newShardBuilder()
	if err != nil {
		return nil, err
	}
	sortDocuments(todo)
	for _, t := range todo {
		if err := shardBuilder.Add(*t); err != nil {
			return nil, err
		}
	}

	return b.writeShard(name, shardBuilder)
}

func (b *Builder) newShardBuilder() (*zoekt.IndexBuilder, error) {
	desc := b.opts.RepositoryDescription
	desc.HasSymbols = b.opts.CTags != ""
	desc.SubRepoMap = b.opts.SubRepositories
	desc.IndexOptions = b.opts.HashOptions()

	shardBuilder, err := zoekt.NewIndexBuilder(&desc)
	if err != nil {
		return nil, err
	}
	return shardBuilder, nil
}

func (b *Builder) writeShard(fn string, ib *zoekt.IndexBuilder) (*finishedShard, error) {
	dir := filepath.Dir(fn)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}

	f, err := ioutil.TempFile(dir, filepath.Base(fn)+".*.tmp")
	if err != nil {
		return nil, err
	}
	if runtime.GOOS != "windows" {
		if err := f.Chmod(0o666 &^ umask); err != nil {
			return nil, err
		}
	}

	defer f.Close()
	if err := ib.Write(f); err != nil {
		return nil, err
	}
	fi, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if err := f.Close(); err != nil {
		return nil, err
	}

	log.Printf("finished %s: %d index bytes (overhead %3.1f)", fn, fi.Size(),
		float64(fi.Size())/float64(ib.ContentSize()+1))

	return &finishedShard{f.Name(), fn}, nil
}

// umask holds the Umask of the current process
var umask os.FileMode
