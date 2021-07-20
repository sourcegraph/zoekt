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

package zoekt // import "github.com/google/zoekt"

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"time"

	"github.com/google/zoekt/query"
)

// FileMatch contains all the matches within a file.
type FileMatch struct {
	// Ranking; the higher, the better.
	Score float64 // TODO - hide this field?

	// For debugging. Needs DebugScore set, but public so tests in
	// other packages can print some diagnostics.
	Debug string

	FileName string

	// Repository is the globally unique name of the repo of the
	// match
	Repository  string
	Branches    []string
	LineMatches []LineMatch

	// RepositoryID is a Sourcegraph extension. This is the ID of Repository in
	// Sourcegraph.
	RepositoryID uint32

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

// LineMatch holds the matches within a single line in a file.
type LineMatch struct {
	// The line in which a match was found.
	Line       []byte
	LineStart  int
	LineEnd    int
	LineNumber int

	// If set, this was a match on the filename.
	FileName bool

	// The higher the better. Only ranks the quality of the match
	// within the file, does not take rank of file into account
	Score         float64
	LineFragments []LineFragmentMatch
}

type Symbol struct {
	Sym        string
	Kind       string
	Parent     string
	ParentKind string
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

	// Shards that we did not process because a query was canceled.
	ShardsSkipped int

	// Number of non-overlapping matches
	MatchCount int

	// Number of candidate matches as a result of searching ngrams.
	NgramMatches int

	// Wall clock time for queued search.
	Wait time.Duration

	// Number of times regexp was called on files that we evaluated.
	RegexpsConsidered int
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
	s.ShardsSkipped += o.ShardsSkipped
	s.Wait += o.Wait
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
		s.ShardsSkipped > 0 ||
		s.Wait > 0)
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

// SearchResult contains search matches and extra data
type SearchResult struct {
	Stats
	Progress
	Files []FileMatch

	// RepoURLs holds a repo => template string map.
	RepoURLs map[string]string

	// FragmentNames holds a repo => template string map, for
	// the line number fragment.
	LineFragments map[string]string
}

// RepositoryBranch describes an indexed branch, which is a name
// combined with a version.
type RepositoryBranch struct {
	Name    string
	Version string
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
}

func (r *Repository) UnmarshalJSON(data []byte) error {
	// We define a new type so that we can use json.Unmarhsal
	// without recursing into this same method.
	type repository *Repository
	repo := repository(r)

	err := json.Unmarshal(data, repo)
	if err != nil {
		return err
	}

	id, _ := strconv.ParseUint(repo.RawConfig["repoid"], 10, 32)
	r.ID = uint32(id)

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
	IndexFormatVersion  int
	IndexFeatureVersion int
	IndexTime           time.Time
	PlainASCII          bool
	LanguageMap         map[string]byte
	ZoektVersion        string
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

// RepoList holds a set of Repository metadata.
type RepoList struct {
	// Full response to a List request. Returned when ListOptions.Minimal is false.
	Repos []*RepoListEntry

	Crashes int

	// Minimal response to a List request. Returned when ListOptions.Minimal is true.
	Minimal map[uint32]*MinimalRepoListEntry
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

type ListOptions struct {
	// Return only Minimal data per repo that Sourcegraph frontend needs.
	Minimal bool
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

	// Maximum number of important matches: skip processing
	// shard after we found this many important matches.
	ShardMaxImportantMatch int

	// Maximum number of important matches across shards.
	TotalMaxImportantMatch int

	// Abort the search after this much time has passed.
	MaxWallTime time.Duration

	// Trim the number of results after collating and sorting the
	// results
	MaxDocDisplayCount int

	// Trace turns on opentracing for this request if true and if the Jaeger address was provided as
	// a command-line flag
	Trace bool

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
