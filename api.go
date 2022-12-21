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

package zoekt // import "github.com/sourcegraph/zoekt"

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"reflect"
	"strconv"
	"time"

	"github.com/sourcegraph/zoekt/query"
)

const mapHeaderBytes uint64 = 48
const sliceHeaderBytes uint64 = 24
const stringHeaderBytes uint64 = 16
const pointerSize uint64 = 8

// FileMatch contains all the matches within a file.
type FileMatch struct {
	// Ranking; the higher, the better.
	Score float64 // TODO - hide this field?

	// Experimental. Ranks is a vector containing floats in the interval [0, 1]. The
	// length of the vector depends on the output from the ranking function at index
	// time.
	//
	// This field is only set if the shard contains ranking information and
	// SearchOptions.UseDocumentRanks is true.
	Ranks []float64

	// For debugging. Needs DebugScore set, but public so tests in
	// other packages can print some diagnostics.
	Debug string

	FileName string

	// Repository is the globally unique name of the repo of the
	// match
	Repository string
	Branches   []string

	// One of LineMatches or ChunkMatches will be returned depending on whether
	// the SearchOptions.ChunkMatches is set.
	LineMatches  []LineMatch
	ChunkMatches []ChunkMatch

	// RepositoryID is a Sourcegraph extension. This is the ID of Repository in
	// Sourcegraph.
	RepositoryID uint32

	// RepositoryPriority is a Sourcegraph extension. It is used by Sourcegraph to
	// order results from different repositories relative to each other.
	RepositoryPriority float64

	// Only set if requested
	Content []byte

	// Checksum of the content.
	Checksum []byte

	// Detected language of the result.
	Language string

	// SubRepositoryName is the globally unique name of the repo,
	// if it came from a subrepository
	SubRepositoryName string

	// SubRepositoryPath holds the prefix where the subrepository
	// was mounted.
	SubRepositoryPath string

	// Commit SHA1 (hex) of the (sub)repo holding the file.
	Version string
}

func (m *FileMatch) sizeBytes() (sz uint64) {
	// Score
	sz += 8

	// ranks
	sz += 8 * uint64(len(m.Ranks))

	for _, s := range []string{
		m.Debug,
		m.FileName,
		m.Repository,
		m.Language,
		m.SubRepositoryName,
		m.SubRepositoryPath,
		m.Version,
	} {
		sz += stringHeaderBytes + uint64(len(s))
	}

	// Branches
	sz += sliceHeaderBytes
	for _, s := range m.Branches {
		sz += stringHeaderBytes + uint64(len(s))
	}

	// LineMatches
	sz += sliceHeaderBytes
	for _, lm := range m.LineMatches {
		sz += lm.sizeBytes()
	}

	// ChunkMatches
	sz += sliceHeaderBytes
	for _, cm := range m.ChunkMatches {
		sz += cm.sizeBytes()
	}

	// RepositoryID
	sz += 4

	// RepositoryPriority
	sz += 8

	// Content
	sz += sliceHeaderBytes + uint64(len(m.Content))

	// Checksum
	sz += sliceHeaderBytes + uint64(len(m.Checksum))

	return
}

// ChunkMatch is a set of non-overlapping matches within a contiguous range of
// lines in the file.
type ChunkMatch struct {
	// Content is a contiguous range of complete lines that fully contains Ranges.
	Content []byte
	// ContentStart is the location (inclusive) of the beginning of content
	// relative to the beginning of the file. It will always be at the
	// beginning of a line (Column will always be 1).
	ContentStart Location

	// FileName indicates whether this match is a match on the file name, in
	// which case Content will contain the file name.
	FileName bool

	// Ranges is a set of matching ranges within this chunk. Each range is relative
	// to the beginning of the file (not the beginning of Content).
	Ranges []Range

	// SymbolInfo is the symbol information associated with Ranges. If it is non-nil,
	// its length will equal that of Ranges. Any of its elements may be nil.
	SymbolInfo []*Symbol

	Score      float64
	DebugScore string
}

func (cm *ChunkMatch) sizeBytes() (sz uint64) {
	// Content
	sz += sliceHeaderBytes + uint64(len(cm.Content))

	// ContentStart
	sz += cm.ContentStart.sizeBytes()

	// FileName
	sz += 1

	// Ranges
	sz += sliceHeaderBytes
	if len(cm.Ranges) > 0 {
		sz += uint64(len(cm.Ranges)) * cm.Ranges[0].sizeBytes()
	}

	// SymbolInfo
	sz += sliceHeaderBytes
	for _, si := range cm.SymbolInfo {
		sz += pointerSize
		if si != nil {
			sz += si.sizeBytes()
		}
	}

	// Score
	sz += 8

	// DebugScore
	sz += stringHeaderBytes + uint64(len(cm.DebugScore))

	return
}

type Range struct {
	// The inclusive beginning of the range.
	Start Location
	// The exclusive end of the range.
	End Location
}

func (r *Range) sizeBytes() uint64 {
	return r.Start.sizeBytes() + r.End.sizeBytes()
}

type Location struct {
	// 0-based byte offset from the beginning of the file
	ByteOffset uint32
	// 1-based line number from the beginning of the file
	LineNumber uint32
	// 1-based column number (in runes) from the beginning of line
	Column uint32
}

func (l *Location) sizeBytes() uint64 {
	return 3 * 4
}

// LineMatch holds the matches within a single line in a file.
type LineMatch struct {
	// The line in which a match was found.
	Line       []byte
	LineStart  int
	LineEnd    int
	LineNumber int

	// Before and After are only set when SearchOptions.NumContextLines is > 0
	Before []byte
	After  []byte

	// If set, this was a match on the filename.
	FileName bool

	// The higher the better. Only ranks the quality of the match
	// within the file, does not take rank of file into account
	Score      float64
	DebugScore string

	LineFragments []LineFragmentMatch
}

func (lm *LineMatch) sizeBytes() (sz uint64) {
	// Line
	sz += sliceHeaderBytes + uint64(len(lm.Line))

	// LineStart, LineEnd, LineNumber
	sz += 3 * 8

	// Before
	sz += sliceHeaderBytes + uint64(len(lm.Before))

	// After
	sz += sliceHeaderBytes + uint64(len(lm.After))

	// FileName
	sz += 1

	// Score
	sz += 8

	// DebugScore
	sz += stringHeaderBytes + uint64(len(lm.DebugScore))

	// LineFragments
	sz += sliceHeaderBytes
	for _, lf := range lm.LineFragments {
		sz += lf.sizeBytes()
	}

	return
}

type Symbol struct {
	Sym        string
	Kind       string
	Parent     string
	ParentKind string
}

func (s *Symbol) sizeBytes() uint64 {
	return 4*stringHeaderBytes + uint64(len(s.Sym)+len(s.Kind)+len(s.Parent)+len(s.ParentKind))
}

// LineFragmentMatch a segment of matching text within a line.
type LineFragmentMatch struct {
	// Offset within the line, in bytes.
	LineOffset int

	// Offset from file start, in bytes.
	Offset uint32

	// Number bytes that match.
	MatchLength int

	SymbolInfo *Symbol
}

func (lfm *LineFragmentMatch) sizeBytes() (sz uint64) {
	// LineOffset
	sz += 8

	// Offset
	sz += 4

	// MatchLength
	sz += 8

	// SymbolInfo
	sz += pointerSize
	if lfm.SymbolInfo != nil {
		sz += lfm.SymbolInfo.sizeBytes()
	}

	return
}

type FlushReason uint8

const (
	FlushReasonTimerExpired FlushReason = 1 << iota
	FlushReasonFinalFlush
	FlushReasonMaxSize
)

var FlushReasonStrings = map[FlushReason]string{
	FlushReasonTimerExpired: "timer_expired",
	FlushReasonFinalFlush:   "final_flush",
	FlushReasonMaxSize:      "max_size_reached",
}

func (fr FlushReason) String() string {
	if v, ok := FlushReasonStrings[fr]; ok {
		return v
	}

	return "none"
}

// Stats contains interesting numbers on the search
type Stats struct {
	// Amount of I/O for reading contents.
	ContentBytesLoaded int64

	// Amount of I/O for reading from index.
	IndexBytesLoaded int64

	// Number of search shards that had a crash.
	Crashes int

	// Wall clock time for this search
	Duration time.Duration

	// Number of files containing a match.
	FileCount int

	// Number of files in shards that we considered.
	ShardFilesConsidered int

	// Files that we evaluated. Equivalent to files for which all
	// atom matches (including negations) evaluated to true.
	FilesConsidered int

	// Files for which we loaded file content to verify substring matches
	FilesLoaded int

	// Candidate files whose contents weren't examined because we
	// gathered enough matches.
	FilesSkipped int

	// Shards that we scanned to find matches.
	ShardsScanned int

	// Shards that we did not process because a query was canceled.
	ShardsSkipped int

	// Shards that we did not process because the query was rejected by the
	// ngram filter indicating it had no matches.
	ShardsSkippedFilter int

	// Number of non-overlapping matches
	MatchCount int

	// Number of candidate matches as a result of searching ngrams.
	NgramMatches int

	// Wall clock time for queued search.
	Wait time.Duration

	// Number of times regexp was called on files that we evaluated.
	RegexpsConsidered int

	// FlushReason explains why results were flushed.
	FlushReason FlushReason
}

func (s *Stats) sizeBytes() (sz uint64) {
	sz = 16 * 8 // This assumes we are running on a 64-bit architecture
	sz += 1     // FlushReason

	return
}

func (s *Stats) Add(o Stats) {
	s.ContentBytesLoaded += o.ContentBytesLoaded
	s.IndexBytesLoaded += o.IndexBytesLoaded
	s.Crashes += o.Crashes
	s.FileCount += o.FileCount
	s.FilesConsidered += o.FilesConsidered
	s.FilesLoaded += o.FilesLoaded
	s.FilesSkipped += o.FilesSkipped
	s.MatchCount += o.MatchCount
	s.NgramMatches += o.NgramMatches
	s.ShardFilesConsidered += o.ShardFilesConsidered
	s.ShardsScanned += o.ShardsScanned
	s.ShardsSkipped += o.ShardsSkipped
	s.ShardsSkippedFilter += o.ShardsSkippedFilter
	s.Wait += o.Wait
	s.RegexpsConsidered += o.RegexpsConsidered

	// We want the first non-zero FlushReason to be sticky. This is a useful
	// property when aggregating stats from several Zoekts.
	if s.FlushReason == 0 {
		s.FlushReason = o.FlushReason
	}
}

// Zero returns true if stats is empty.
func (s *Stats) Zero() bool {
	if s == nil {
		return true
	}

	return !(s.ContentBytesLoaded > 0 ||
		s.IndexBytesLoaded > 0 ||
		s.Crashes > 0 ||
		s.FileCount > 0 ||
		s.FilesConsidered > 0 ||
		s.FilesLoaded > 0 ||
		s.FilesSkipped > 0 ||
		s.MatchCount > 0 ||
		s.NgramMatches > 0 ||
		s.ShardFilesConsidered > 0 ||
		s.ShardsScanned > 0 ||
		s.ShardsSkipped > 0 ||
		s.ShardsSkippedFilter > 0 ||
		s.Wait > 0 ||
		s.RegexpsConsidered > 0)
}

// Progress contains information about the global progress of the running search query.
// This is used by the frontend to reorder results and emit them when stable.
// Sourcegraph specific: this is used when querying multiple zoekt-webserver instances.
type Progress struct {
	// Priority of the shard that was searched.
	Priority float64

	// MaxPendingPriority is the maximum priority of pending result that is being searched in parallel.
	// This is used to reorder results when the result set is known to be stable-- that is, when a result's
	// Priority is greater than the max(MaxPendingPriority) from the latest results of each backend, it can be returned to the user.
	//
	// MaxPendingPriority decreases monotonically in each SearchResult.
	MaxPendingPriority float64
}

func (p *Progress) sizeBytes() uint64 {
	return 2 * 8
}

// SearchResult contains search matches and extra data
type SearchResult struct {
	Stats

	// Do not encode this as we cannot encode -Inf in JSON
	Progress `json:"-"`

	Files []FileMatch

	// RepoURLs holds a repo => template string map.
	RepoURLs map[string]string

	// FragmentNames holds a repo => template string map, for
	// the line number fragment.
	LineFragments map[string]string
}

// SizeBytes is a best-effort estimate of the size of SearchResult in memory.
// The estimate does not take alignment into account. The result is a lower
// bound on the actual size in memory.
func (sr *SearchResult) SizeBytes() (sz uint64) {
	sz += sr.Stats.sizeBytes()
	sz += sr.Progress.sizeBytes()

	// Files
	sz += sliceHeaderBytes
	for _, f := range sr.Files {
		sz += f.sizeBytes()
	}

	// RepoURLs
	sz += mapHeaderBytes
	for k, v := range sr.RepoURLs {
		sz += stringHeaderBytes + uint64(len(k))
		sz += stringHeaderBytes + uint64(len(v))
	}

	// LineFragments
	sz += mapHeaderBytes
	for k, v := range sr.LineFragments {
		sz += stringHeaderBytes + uint64(len(k))
		sz += stringHeaderBytes + uint64(len(v))
	}

	return
}

// RepositoryBranch describes an indexed branch, which is a name
// combined with a version.
type RepositoryBranch struct {
	Name    string
	Version string
}

func (r RepositoryBranch) String() string {
	return fmt.Sprintf("%s@%s", r.Name, r.Version)
}

// Repository holds repository metadata.
type Repository struct {
	// Sourcergaph's repository ID
	ID uint32

	// The repository name
	Name string

	// The repository URL.
	URL string

	// The physical source where this repo came from, eg. full
	// path to the zip filename or git repository directory. This
	// will not be exposed in the UI, but can be used to detect
	// orphaned index shards.
	Source string

	// The branches indexed in this repo.
	Branches []RepositoryBranch

	// Nil if this is not the super project.
	SubRepoMap map[string]*Repository

	// URL template to link to the commit of a branch
	CommitURLTemplate string

	// The repository URL for getting to a file.  Has access to
	// {{Branch}}, {{Path}}
	FileURLTemplate string

	// The URL fragment to add to a file URL for line numbers. has
	// access to {{LineNumber}}. The fragment should include the
	// separator, generally '#' or ';'.
	LineFragmentTemplate string

	// Perf optimization: priority is set when we load the shard. It corresponds to
	// the value of "priority" stored in RawConfig.
	priority float64

	// All zoekt.* configuration settings.
	RawConfig map[string]string

	// Importance of the repository, bigger is more important
	Rank uint16

	// IndexOptions is a hash of the options used to create the index for the
	// repo.
	IndexOptions string

	// HasSymbols is true if this repository has indexed ctags
	// output. Sourcegraph specific: This field is more appropriate for
	// IndexMetadata. However, we store it here since the Sourcegraph frontend
	// can read this structure but not IndexMetadata.
	HasSymbols bool

	// Tombstone is true if we are not allowed to search this repo.
	Tombstone bool

	// LatestCommitDate is the date of the latest commit among all indexed Branches.
	// The date might be time.Time's 0-value if the repository was last indexed
	// before this field was added.
	LatestCommitDate time.Time

	// FileTombstones is a set of file paths that should be ignored across all branches
	// in this shard.
	FileTombstones map[string]struct{} `json:",omitempty"`
}

func (r *Repository) UnmarshalJSON(data []byte) error {
	// We define a new type so that we can use json.Unmarshal
	// without recursing into this same method.
	type repository *Repository
	repo := repository(r)

	err := json.Unmarshal(data, repo)
	if err != nil {
		return err
	}

	if v, ok := repo.RawConfig["repoid"]; ok {
		id, _ := strconv.ParseUint(v, 10, 32)
		r.ID = uint32(id)
	}

	if v, ok := repo.RawConfig["priority"]; ok {
		r.priority, err = strconv.ParseFloat(v, 64)
		if err != nil {
			r.priority = 0
		}

		// Sourcegraph indexserver doesn't set repo.Rank, so we set it here
		// based on priority. Setting it on read instead of during indexing
		// allows us to avoid a complete reindex.
		if r.Rank == 0 && r.priority > 0 {
			l := math.Log(float64(r.priority))
			repo.Rank = uint16((1.0 - 1.0/math.Pow(1+l, 0.6)) * 10000)
		}
	}
	return nil
}

// MergeMutable will merge x into r. mutated will be true if it made any
// changes. err is non-nil if we needed to mutate an immutable field.
//
// Note: SubRepoMap, IndexOptions and HasSymbol fields are ignored. They are
// computed while indexing so can't be synthesized from x.
//
// Note: We ignore RawConfig fields which are duplicated into Repository:
// name and id.
//
// Note: URL, *Template fields are ignored. They are not used by Sourcegraph.
func (r *Repository) MergeMutable(x *Repository) (mutated bool, err error) {
	if r.ID != x.ID {
		// Sourcegraph: strange behaviour may occur if ID changes but names don't.
		return mutated, errors.New("ID is immutable")
	}
	if r.Name != x.Name {
		// Name is encoded into the shard name on disk. We need to re-index if it
		// changes.
		return mutated, errors.New("Name is immutable")
	}
	if !reflect.DeepEqual(r.Branches, x.Branches) {
		// Need a reindex if content changing.
		return mutated, errors.New("Branches is immutable")
	}

	for k, v := range x.RawConfig {
		// We ignore name and id since they are encoded into the repository.
		if k == "name" || k == "id" {
			continue
		}
		if r.RawConfig == nil {
			mutated = true
			r.RawConfig = make(map[string]string)
		}
		if r.RawConfig[k] != v {
			mutated = true
			r.RawConfig[k] = v
		}
	}

	return mutated, nil
}

// IndexMetadata holds metadata stored in the index file. It contains
// data generated by the core indexing library.
type IndexMetadata struct {
	IndexFormatVersion    int
	IndexFeatureVersion   int
	IndexMinReaderVersion int
	IndexTime             time.Time
	PlainASCII            bool
	LanguageMap           map[string]uint16
	ZoektVersion          string
	ID                    string
}

// Statistics of a (collection of) repositories.
type RepoStats struct {
	// Repos is used for aggregrating the number of repositories.
	Repos int

	// Shards is the total number of search shards.
	Shards int

	// Documents holds the number of documents or files.
	Documents int

	// IndexBytes is the amount of RAM used for index overhead.
	IndexBytes int64

	// ContentBytes is the amount of RAM used for raw content.
	ContentBytes int64

	// Sourcegraph specific stats below. These are not as efficient to calculate
	// as the above statistics. We experimentally measured about a 10% slower
	// shard load time. However, we find these values very useful to track and
	// computing them outside of load time introduces a lot of complexity.

	// NewLinesCount is the number of newlines "\n" that appear in the zoekt
	// indexed documents. This is not exactly the same as line count, since it
	// will not include lines not terminated by "\n" (eg a file with no "\n", or
	// a final line without "\n"). Note: Zoekt deduplicates documents across
	// branches, so if a path has the same contents on multiple branches, there
	// is only one document for it. As such that document's newlines is only
	// counted once. See DefaultBranchNewLinesCount and AllBranchesNewLinesCount
	// for counts which do not deduplicate.
	NewLinesCount uint64

	// DefaultBranchNewLinesCount is the number of newlines "\n" in the default
	// branch.
	DefaultBranchNewLinesCount uint64

	// OtherBranchesNewLinesCount is the number of newlines "\n" in all branches
	// except the default branch.
	OtherBranchesNewLinesCount uint64
}

func (s *RepoStats) Add(o *RepoStats) {
	// can't update Repos, since one repo may have multiple
	// shards.
	s.Shards += o.Shards
	s.IndexBytes += o.IndexBytes
	s.Documents += o.Documents
	s.ContentBytes += o.ContentBytes

	// Sourcegraph specific
	s.NewLinesCount += o.NewLinesCount
	s.DefaultBranchNewLinesCount += o.DefaultBranchNewLinesCount
	s.OtherBranchesNewLinesCount += o.OtherBranchesNewLinesCount
}

type RepoListEntry struct {
	Repository    Repository
	IndexMetadata IndexMetadata
	Stats         RepoStats
}

type MinimalRepoListEntry struct {
	HasSymbols bool
	Branches   []RepositoryBranch
}

type ReposMap map[uint32]MinimalRepoListEntry

// MarshalBinary implements a specialized encoder for ReposMap.
func (q *ReposMap) MarshalBinary() ([]byte, error) {
	return reposMapEncode(*q)
}

// UnmarshalBinary implements a specialized decoder for ReposMap.
func (q *ReposMap) UnmarshalBinary(b []byte) error {
	var err error
	(*q), err = reposMapDecode(b)
	return err
}

// RepoList holds a set of Repository metadata.
type RepoList struct {
	// Returned when ListOptions.Field is RepoListFieldRepos.
	Repos []*RepoListEntry

	// Returned when ListOptions.Field is RepoListFieldMinimal.
	//
	// Deprecated: use ReposMap.
	Minimal map[uint32]*MinimalRepoListEntry

	// ReposMap is set when ListOptions.Field is RepoListFieldReposMap.
	ReposMap ReposMap

	Crashes int

	// Stats response to a List request.
	// This is the aggregate RepoStats of all repos matching the input query.
	Stats RepoStats
}

type Searcher interface {
	Search(ctx context.Context, q query.Q, opts *SearchOptions) (*SearchResult, error)

	// List lists repositories. The query `q` can only contain
	// query.Repo atoms.
	List(ctx context.Context, q query.Q, opts *ListOptions) (*RepoList, error)
	Close()

	// Describe the searcher for debug messages.
	String() string
}

type RepoListField int

const (
	RepoListFieldRepos    RepoListField = 0
	RepoListFieldMinimal                = 1
	RepoListFieldReposMap               = 2
)

type ListOptions struct {
	// Return only Minimal data per repo that Sourcegraph frontend needs.
	//
	// Deprecated: use Field
	Minimal bool

	// Field decides which field to populate in RepoList response.
	Field RepoListField
}

func (o *ListOptions) GetField() (RepoListField, error) {
	if o == nil {
		return RepoListFieldRepos, nil
	}
	if o.Field < 0 || o.Field > RepoListFieldReposMap {
		return 0, fmt.Errorf("unknown RepoListField %d", o.Field)
	}
	if o.Minimal == true {
		return RepoListFieldMinimal, nil
	}
	return o.Field, nil
}

func (o *ListOptions) String() string {
	return fmt.Sprintf("%#v", o)
}

type SearchOptions struct {
	// Return an upper-bound estimate of eligible documents in
	// stats.ShardFilesConsidered.
	EstimateDocCount bool

	// Return the whole file.
	Whole bool

	// Maximum number of matches: skip all processing an index
	// shard after we found this many non-overlapping matches.
	ShardMaxMatchCount int

	// Maximum number of matches: stop looking for more matches
	// once we have this many matches across shards.
	TotalMaxMatchCount int

	// Maximum number of matches: skip processing documents for a repository in
	// a shard once we have found ShardRepoMaxMatchCount.
	//
	// A compound shard may contain multiple repositories. This will most often
	// be set to 1 to find all repositories containing a result.
	ShardRepoMaxMatchCount int

	// Deprecated: this field is not read anymore.
	ShardMaxImportantMatch int

	// Deprecated: this field is not read anymore.
	TotalMaxImportantMatch int

	// Abort the search after this much time has passed.
	MaxWallTime time.Duration

	// FlushWallTime if non-zero will stop streaming behaviour at first and
	// instead will collate and sort results. At FlushWallTime the results will
	// be sent and then the behaviour will revert to the normal streaming.
	FlushWallTime time.Duration

	// Trim the number of results after collating and sorting the
	// results
	MaxDocDisplayCount int

	// If set to a number greater than zero then up to this many number
	// of context lines will be added before and after each matched line.
	// Note that the included context lines might contain matches and
	// it's up to the consumer of the result to remove those lines.
	NumContextLines int

	// If true, ChunkMatches will be returned in each FileMatch rather than LineMatches
	// EXPERIMENTAL: the behavior of this flag may be changed in future versions.
	ChunkMatches bool

	// EXPERIMENTAL. If true, document ranks are used as additional input for
	// sorting matches.
	UseDocumentRanks bool

	// RanksDampingFactor determines the contribution of documents ranks to the
	// final ranking based on RRF. A value in (0,1] reduces the contribution,
	// while a value in (-inf,0) increases it.
	RanksDampingFactor float64

	// Trace turns on opentracing for this request if true and if the Jaeger address was provided as
	// a command-line flag
	Trace bool

	// If set, the search results will contain debug information for scoring.
	DebugScore bool

	// SpanContext is the opentracing span context, if it exists, from the zoekt client
	SpanContext map[string]string
}

func (s *SearchOptions) String() string {
	return fmt.Sprintf("%#v", s)
}

// Sender is the interface that wraps the basic Send method.
type Sender interface {
	Send(*SearchResult)
}

// Streamer adds the method StreamSearch to the Searcher interface.
type Streamer interface {
	Searcher
	StreamSearch(ctx context.Context, q query.Q, opts *SearchOptions, sender Sender) (err error)
}
