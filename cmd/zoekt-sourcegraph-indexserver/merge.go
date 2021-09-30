package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/zoekt"
)

// doMerge drives the merge process, I imagine this function goes away once we
// automate merging.
func doMerge(params string) error {
	dir, maxSize, targetSize, days, simulate, err := parseParams(params)
	if err != nil {
		return err
	}

	if simulate {
		debug.Println("simulating")
	}

	shards := loadShards(dir)
	if len(shards) == 0 {
		return fmt.Errorf("no shards found")
	}
	debug.Printf("found %d shards\n", len(shards))

	backupDir := filepath.Join(dir, ".scratch/bak")
	err = os.MkdirAll(backupDir, 0o755)
	if err != nil {
		debug.Printf("error creating backup dir %s: %s", backupDir, err)
		return err
	}

	opts := compoundOpts{
		targetSizeBytes: int64(targetSize) * 1024 * 1024,
		maxSizeBytes:    int64(maxSize) * 1024 * 1024,
		cutoffDate:      time.Now().AddDate(0, 0, -days),
	}
	compounds, excluded := generateCompounds(shards, opts)

	debug.Printf("generated %d compounds and %d excluded repositories\n", len(compounds), len(excluded))
	if len(compounds) == 0 {
		return nil
	}

	var totalSizeBytes int64 = 0
	totalShards := 0
	for ix, comp := range compounds {
		debug.Printf("compound %d: merging %d shards with total size %.2f MiB\n", ix, len(comp.shards), float64(comp.size)/(1024*1024))
		if !simulate {
			err := callMerge(comp.shards)
			if err != nil {
				return err
			}
			moveAll(backupDir, comp.shards)
		}
		totalShards += len(comp.shards)
		totalSizeBytes += comp.size
	}

	debug.Printf("total size: %.2f MiB, number of shards merged: %d\n", float64(totalSizeBytes)/(1024*1024), totalShards)
	return nil
}

// loadShards returns all simple shards in dir without .meta files.
func loadShards(dir string) []shard {
	d, err := os.Open(dir)
	if err != nil {
		debug.Printf("failed to loadShards: %s", dir)
		return []shard{}
	}
	defer d.Close()
	names, _ := d.Readdirnames(-1)

	shards := make([]shard, 0, len(names))
	for _, n := range names {
		path := filepath.Join(dir, n)

		fi, err := os.Stat(path)
		if err != nil {
			debug.Printf("stat failed for %s: %s", n, err)
			continue
		}

		if fi.IsDir() || filepath.Ext(path) != ".zoekt" || strings.HasPrefix(filepath.Base(path), "compound-") {
			continue
		}

		repos, _, err := zoekt.ReadMetadataPath(path)
		if err != nil {
			debug.Printf("failed to load metadata for %s\n", filepath.Base(path))
			continue
		}
		if len(repos) != 1 {
			debug.Printf("expected %s to be a simple shard, but encountered %d repos", n, len(repos))
			continue
		}

		rank, err := strconv.ParseFloat(repos[0].RawConfig["rank"], 64)
		if err != nil {
			debug.Printf("error parsing rank %s for shard: %s: %s, setting rank to 0", repos[0].RawConfig["rank"], n, err)
			rank = 0
		}

		shards = append(shards, shard{
			Repo:      n,
			Path:      path,
			ModTime:   fi.ModTime(),
			SizeBytes: fi.Size(),
			Rank:      rank,
		})
	}
	return shards
}

type compound struct {
	shards []shard
	size   int64
}

type compoundOpts struct {
	targetSizeBytes int64
	maxSizeBytes    int64
	cutoffDate      time.Time
}

// generateCompounds encodes the merge policy:
//   - merge shards that are older than opt.cutoffDate
//   - merge shards that are smaller than opt.maxSizeBytes
//   - merge shards with similar rank
//   - compound shards should be larger than opt.targetSizeBytes
func generateCompounds(shards []shard, opt compoundOpts) ([]compound, []shard) {
	cur := 0
	for ix, s := range shards {
		if s.ModTime.After(opt.cutoffDate) || s.SizeBytes > opt.maxSizeBytes {
			shards[cur], shards[ix] = shards[ix], shards[cur]
			cur++
			continue
		}
	}
	if cur == len(shards) {
		return []compound{}, shards
	}

	excluded := shards[:cur]
	shards = shards[cur:]

	sort.Slice(shards, func(i, j int) bool {
		return shards[i].Rank < shards[j].Rank
	})

	// We prioritze merging shards with similar priority. This approach does not
	// minimize the distance to the target compound size, but it gets close enough.
	compounds := make([]compound, 0)
	currentCompound := compound{}
	for _, s := range shards {
		if currentCompound.size > opt.targetSizeBytes {
			compounds = append(compounds, currentCompound)
			currentCompound = compound{}
		}
		currentCompound.shards = append(currentCompound.shards, s)
		currentCompound.size += s.SizeBytes
	}
	if currentCompound.size > opt.targetSizeBytes {
		compounds = append(compounds, currentCompound)
	} else {
		// The shards in currentCompound did not reach the desired target size. We don't
		// want to create tiny compound shards, hence we append to excluded. The next
		// time we run merge we might have enough shards to cross the threshold.
		excluded = append(excluded, currentCompound.shards...)
	}

	return compounds, excluded
}

func callMerge(shards []shard) error {
	sb := strings.Builder{}
	for _, s := range shards {
		sb.WriteString(fmt.Sprintf("%s\n", s.Path))
	}

	cmd := exec.Command("zoekt-merge-index", "-")
	wc, err := cmd.StdinPipe()
	if err != nil {
		return err
	}

	err = cmd.Start()
	if err != nil {
		return err
	}

	_, err = io.WriteString(wc, sb.String())
	if err != nil {
		return err
	}
	wc.Close()

	return cmd.Wait()
}

// parseParams is helper function to parse a comma separated string of parameters
// of the form "path,1,2000,1,true". This should be used just for debugging and
// testing.
func parseParams(params string) (indexDir string, maxSize int, targetSize int, days int, simulate bool, err error) {
	ps := strings.Split(params, ",")
	indexDir = ps[0]

	maxSize, err = strconv.Atoi(ps[1])
	if err != nil {
		return
	}

	targetSize, err = strconv.Atoi(ps[2])
	if err != nil {
		return
	}

	days, err = strconv.Atoi(ps[3])
	if err != nil {
		return
	}

	simulate, err = strconv.ParseBool(ps[4])
	if err != nil {
		return
	}
	return
}
