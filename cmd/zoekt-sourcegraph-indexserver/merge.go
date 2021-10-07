package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/zoekt"
)

// parseParams is helper function to parse a comma separated string of parameters
// of the form "string,int,bool". This should be used for debugging and testing
// only.
func parseParams(params string) (indexDir string, targetSizeBytes int64, simulate bool, err error) {
	ps := strings.Split(params, ",")
	indexDir = ps[0]

	targetSize, err := strconv.Atoi(ps[1])
	if err != nil {
		return
	}
	targetSizeBytes = int64(targetSize * 1024 * 1024)

	simulate, err = strconv.ParseBool(ps[2])
	if err != nil {
		return
	}
	return
}

// doMerge drives the merge process.
func doMerge(params string) error {
	dir, targetSizeBytes, simulate, err := parseParams(params)
	if err != nil {
		return err
	}

	if simulate {
		debug.Println("simulating")
	}

	shards, excluded := loadCandidates(dir)
	debug.Printf("merging: found %d candidate shards, %d repos were excluded\n", len(shards), excluded)
	if len(shards) == 0 {
		return nil
	}

	compounds := generateCompounds(shards, targetSizeBytes)
	debug.Printf("merging: generated %d compounds\n", len(compounds))
	if len(compounds) == 0 {
		return nil
	}

	var totalSizeBytes int64 = 0
	totalShards := 0
	for ix, comp := range compounds {
		debug.Printf("compound %d: merging %d shards with total size %.2f MiB\n", ix, len(comp.shards), float64(comp.size)/(1024*1024))
		if !simulate {
			err := callMerge(comp.shards)
			for _, s := range comp.shards {
				os.Remove(s.path)
			}
			if err != nil {
				return err
			}
		}
		totalShards += len(comp.shards)
		totalSizeBytes += comp.size
	}

	debug.Printf("total size: %.2f MiB, number of shards merged: %d\n", float64(totalSizeBytes)/(1024*1024), totalShards)
	return nil
}

type candidate struct {
	path string

	// The size as reported by os.Stat.
	sizeBytes int64
}

// loadCandidates returns all shards eligable for merging.
func loadCandidates(dir string) ([]candidate, int) {
	excluded := 0

	d, err := os.Open(dir)
	if err != nil {
		debug.Printf("failed to load candidates: %s", dir)
		return []candidate{}, excluded
	}
	defer d.Close()
	names, _ := d.Readdirnames(-1)

	candidates := make([]candidate, 0, len(names))
	for _, n := range names {
		path := filepath.Join(dir, n)

		fi, err := os.Stat(path)
		if err != nil {
			debug.Printf("stat failed for %s: %s", n, err)
			continue
		}

		if fi.IsDir() || filepath.Ext(path) != ".zoekt" {
			continue
		}

		if isExcluded(path) {
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

var reShard = regexp.MustCompile("\\.[0-9]{5}\\.zoekt$")

func hasMultipleShards(path string) bool {
	if !reShard.MatchString(path) {
		return false
	}
	secondShard := reShard.ReplaceAllString(path, ".00001.zoekt")
	_, err := os.Stat(secondShard)
	if os.IsNotExist(err) {
		return false
	}
	return true
}

func isExcluded(path string) bool {
	if hasMultipleShards(path) {
		return true
	}

	repos, _, err := zoekt.ReadMetadataPath(path)
	if err != nil {
		debug.Printf("failed to load metadata for %s\n", filepath.Base(path))
		return true
	}

	if len(repos) > 1 {
		return false
	}

	if repos[0].LatestCommitDate.After(time.Now().AddDate(0, 0, -7)) {
		return true
	}

	if priority, err := strconv.ParseFloat(repos[0].RawConfig["priority"], 64); err == nil && priority > 100 {
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

func generateCompounds(shards []candidate, targetSizeBytes int64) []compound {
	sort.Slice(shards, func(i, j int) bool {
		return shards[i].sizeBytes < shards[j].sizeBytes
	})

	compounds := make([]compound, 0)
	for len(shards) > 0 {
		cur := compound{}

		// Start with the largest shard and add smaller shards until we reach the target
		// size. This strategy automatically fills up compound shards below the target
		// size. We accept compounds with 1 repo because we will ignore them later in
		// callMerge.
		cur.add(shards[len(shards)-1])
		shards = shards[:len(shards)-1]
		for i := len(shards) - 1; i >= 0; i-- {
			if cur.size+shards[i].sizeBytes > targetSizeBytes {
				continue
			}
			cur.add(shards[i])
			shards = append(shards[:i], shards[i+1:]...)
		}
		compounds = append(compounds, cur)
	}
	return compounds
}

func callMerge(shards []candidate) error {
	if len(shards) <= 1 {
		return nil
	}

	cmd := exec.Command("zoekt-merge-index", "-")
	wc, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	defer wc.Close()

	err = cmd.Start()
	if err != nil {
		return err
	}

	for _, s := range shards {
		io.WriteString(wc, fmt.Sprintf("%s\n", s.path))
	}
	wc.Close()

	return cmd.Wait()
}
