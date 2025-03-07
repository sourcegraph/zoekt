package main

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"time"

	"github.com/grafana/regexp"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/sourcegraph/zoekt/index"
	"github.com/sourcegraph/zoekt/internal/tenant"
	"go.uber.org/atomic"
)

var metricShardMergingRunning = promauto.NewGauge(prometheus.GaugeOpts{
	Name: "index_shard_merging_running",
	Help: "Set to 1 if indexserver's merge job is running.",
})

var metricShardMergingDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
	Name:    "index_shard_merging_duration_seconds",
	Help:    "The duration of 1 shard merge operation.",
	Buckets: prometheus.LinearBuckets(30, 30, 10),
}, []string{"error"})

func pickCandidates(shards []candidate, targetSizeBytes int64) compound {
	c := compound{}
	for _, shard := range shards {
		c.add(shard)
		if c.size >= targetSizeBytes {
			return c
		}
	}
	return compound{}
}

var mergeRunning atomic.Bool

func defaultMergeCmd(args ...string) *exec.Cmd {
	cmd := exec.Command("zoekt-merge-index", "merge")
	cmd.Args = append(cmd.Args, args...)
	return cmd
}

func defaultExplodeCmd(args ...string) *exec.Cmd {
	cmd := exec.Command("zoekt-merge-index", "explode")
	cmd.Args = append(cmd.Args, args...)
	return cmd
}

// doMerge drives the merge process. It holds the lock on s.indexDir for the
// duration of 1 merge, which might be several minutes, depending on the target
// size of the compound shard.
func (s *Server) doMerge() {
	s.merge(defaultMergeCmd)
}

// same as doMerge but with a configurable merge command.
func (s *Server) merge(mergeCmd func(args ...string) *exec.Cmd) {
	// Guard against the user triggering competing merge jobs with the debug
	// command.
	if !mergeRunning.CompareAndSwap(false, true) {
		infoLog.Printf("merge already running")
		return
	}
	defer mergeRunning.Store(false)

	metricShardMergingRunning.Set(1)
	defer metricShardMergingRunning.Set(0)

	// We keep creating compound shards until we run out of shards to merge or until
	// we encounter an error during merging.
	next := true
	for next {
		next = false
		s.muIndexDir.Global(func() {
			candidates, excluded := loadCandidates(s.IndexDir, s.mergeOpts)
			infoLog.Printf("loadCandidates: candidates=%d excluded=%d", len(candidates), excluded)

			c := pickCandidates(candidates, s.mergeOpts.targetSizeBytes)
			if len(c.shards) <= 1 {
				infoLog.Printf("could not find enough shards to build a compound shard")
				return
			}
			infoLog.Printf("start merging: shards=%d total_size=%.2fMiB", len(c.shards), float64(c.size)/(1024*1024))

			var paths []string
			for _, p := range c.shards {
				paths = append(paths, p.path)
			}

			start := time.Now()

			cmd := mergeCmd(paths...)

			// zoekt-merge-index writes the full path of the new compound shard to stdout.
			stdoutBuf := &bytes.Buffer{}
			stderrBuf := &bytes.Buffer{}
			cmd.Stdout = stdoutBuf
			cmd.Stderr = stderrBuf

			err := cmd.Run()

			durationSeconds := time.Since(start).Seconds()
			metricShardMergingDuration.WithLabelValues(strconv.FormatBool(err != nil)).Observe(durationSeconds)
			if err != nil {
				errorLog.Printf("error merging shards: stdout=%s, stderr=%s, durationSeconds=%.2f err=%s", stdoutBuf.String(), stderrBuf.String(), durationSeconds, err)
				return
			}

			infoLog.Printf("finished merging: shard=%s durationSeconds=%.2f", stdoutBuf.String(), durationSeconds)

			next = true
		})
	}
}

type candidate struct {
	path string

	// The size as reported by os.Stat.
	sizeBytes int64
}

// loadCandidates returns all shards eligible for merging.
func loadCandidates(dir string, opts mergeOpts) ([]candidate, int) {
	excluded := 0

	d, err := os.Open(dir)
	if err != nil {
		debugLog.Printf("failed to load candidates: %s", dir)
		return []candidate{}, excluded
	}
	defer d.Close()
	names, _ := d.Readdirnames(-1)

	candidates := make([]candidate, 0, len(names))
	for _, n := range names {
		path := filepath.Join(dir, n)

		fi, err := os.Stat(path)
		if err != nil {
			debugLog.Printf("stat failed for %s: %s", n, err)
			continue
		}

		if fi.IsDir() || filepath.Ext(path) != ".zoekt" {
			continue
		}

		if isExcluded(path, fi, opts) {
			excluded++
			continue
		}

		candidates = append(candidates, candidate{
			path:      path,
			sizeBytes: fi.Size(),
		})
	}
	return candidates, excluded
}

var reShard = regexp.MustCompile(`\.[0-9]{5}\.zoekt$`)

func hasMultipleShards(path string) bool {
	if !reShard.MatchString(path) {
		return false
	}
	secondShard := reShard.ReplaceAllString(path, ".00001.zoekt")
	_, err := os.Stat(secondShard)
	return !os.IsNotExist(err)
}

type mergeOpts struct {
	// targetSizeBytes is the target size in bytes for compound shards. The higher
	// the value the more repositories a compound shard will contain and the bigger
	// the potential for saving MEM. The savings in MEM come at the cost of a
	// degraded search performance.
	targetSizeBytes int64

	// compound shards smaller than minSizeBytes will be deleted by vacuum.
	minSizeBytes int64

	// vacuumInterval is how often indexserver scans compound shards to remove
	// tombstones.
	vacuumInterval time.Duration

	// mergeInterval defines how often indexserver runs the merge operation in
	// the index directory.
	mergeInterval time.Duration

	// number of days since the last commit until we consider the shard for
	// merging. For example, a value of 7 means that only repos that have been
	// inactive for 7 days will be considered for merging.
	minAgeDays int
}

// isExcluded returns true if a shard should not be merged, false otherwise.
//
// We need path and FileInfo because FileInfo does not contain the full path, see
// discussion here https://github.com/golang/go/issues/32300.
func isExcluded(path string, fi os.FileInfo, opts mergeOpts) bool {
	if hasMultipleShards(path) {
		return true
	}

	repos, _, err := index.ReadMetadataPath(path)
	if err != nil {
		debugLog.Printf("failed to load metadata for %s\n", fi.Name())
		return true
	}

	// Exclude compound shards from being merge targets. Why? We want repositories in a
	// compound shard to be ordered based on their priority. The easiest way to
	// enforce this is to delete the compound shard once it drops below a certain
	// size (handled by cleanup), reindex the repositories and merge them with other
	// shards in the correct order.
	if len(repos) > 1 {
		return true
	}

	if repos[0].LatestCommitDate.After(time.Now().AddDate(0, 0, -opts.minAgeDays)) {
		return true
	}

	return false
}

type compound struct {
	shards []candidate
	size   int64
}

func (c *compound) add(cand candidate) {
	c.shards = append(c.shards, cand)
	c.size += cand.sizeBytes
}

// explodeTenantCompoundShards explodes all compound shards that have repos from
// the tenant in question. The caller must hold the global lock.
func (s *Server) explodeTenantCompoundShards(ctx context.Context, explodeFunc func(path string) error) error {
	tnt, err := tenant.FromContext(ctx)
	if err != nil {
		return err
	}

	paths, err := filepath.Glob(filepath.Join(s.IndexDir, "compound-*"))
	if err != nil {
		return err
	}
	if len(paths) == 0 {
		return nil
	}

nextCompoundShard:
	for _, path := range paths {
		// We don't use ReadMetadataPathAlive because we want to detect
		// tombstoned repos, too.
		repos, _, err := index.ReadMetadataPath(path)
		if err != nil {
			return err
		}
		for _, repo := range repos {
			if repo.TenantID == tnt.ID() {
				err := explodeFunc(path)
				if err != nil {
					return err
				}

				continue nextCompoundShard
			}
		}
	}
	return nil
}
