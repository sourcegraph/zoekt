package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"time"

	"github.com/google/zoekt"
	"gopkg.in/natefinch/lumberjack.v2"
)

var reCompound = regexp.MustCompile("compound-.*\\.zoekt")

// doMerge drives the merge process.
func doMerge(dir string, targetSizeBytes, maxSizeBytes int64, simulate bool) error {

	wc := &lumberjack.Logger{
		Filename:   filepath.Join(dir, "zoekt-merge-log.tsv"),
		MaxSize:    100, // Megabyte
		MaxBackups: 5,
	}

	if simulate {
		debug.Println("simulating")
	}

	shards, excluded := loadCandidates(dir, maxSizeBytes)
	debug.Printf("merging: found %d candidate shards, %d shards were excluded\n", len(shards), excluded)
	if len(shards) == 0 {
		return nil
	}

	compounds, _ := generateCompounds(shards, targetSizeBytes)
	debug.Printf("merging: generated %d compounds\n", len(compounds))
	if len(compounds) == 0 {
		return nil
	}

	var totalSizeBytes int64 = 0
	totalShards := 0
	for ix, comp := range compounds {
		debug.Printf("compound %d: merging %d shards with total size %.2f MiB\n", ix, len(comp.shards), float64(comp.size)/(1024*1024))
		if !simulate {
			stdOut, stdErr, err := callMerge(comp.shards)
			debug.Printf("callMerge: OUT: %s, ERR: %s\n", string(stdOut), string(stdErr))
			if err != nil {
				debug.Printf("error during merging compound %d, stdErr: %s, err: %s\n", ix, stdErr, err)
				continue
			}
			// for len(comp.shards)<=1, callMerge is a NOP. Hence there is no need to log
			// anything here.
			if len(comp.shards) > 1 {
				newCompoundName := reCompound.Find(stdErr)
				now := time.Now()
				for _, s := range comp.shards {
					_, _ = fmt.Fprintf(wc, "%d\t%s\t%s\t%s\n", now.UTC().Unix(), "merge", filepath.Base(s.path), string(newCompoundName))
				}
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
func loadCandidates(dir string, maxSize int64) ([]candidate, int) {
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

		if isExcluded(path, fi, maxSize) {
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

// isExcluded returns true if a shard should not be merged, false otherwise.
//
// We need path and FileInfo because FileInfo does not contain the full path, see
// discussion here https://github.com/golang/go/issues/32300.
func isExcluded(path string, fi os.FileInfo, maxSize int64) bool {

	// It takes around 2 minutes to create a compound shard of 2 GiB. This is true
	// even if we just add 1 repo to an existing compound shard. The reason is that
	// we don't support incremental merging, but instead create a compound shard from
	// scratch for each merge operation. Hence we want to avoid merging a compound
	// shard with other smaller candidate shards if the compound shard already has a
	// decent size.
	//
	// The concrete value of maxSize is not important as long as it is smaller than
	// the targetSize and large enough to see sufficient benefits from merging.
	if fi.Size() > maxSize {
		return true
	}

	if hasMultipleShards(path) {
		return true
	}

	repos, _, err := zoekt.ReadMetadataPath(path)
	if err != nil {
		debug.Printf("failed to load metadata for %s\n", fi.Name())
		return true
	}

	// Exclude compound shards from being merge targets. Why? We want repositories in a
	// compound shard to be ordered based on their priority. The easiest way to
	// enforce this is to delete the compound shard once it drops below a certain
	// size (handeled by cleanup), reindex the repositories and merge them with other
	// shards in the correct order.
	if len(repos) > 1 {
		return true
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

func generateCompounds(shards []candidate, targetSizeBytes int64) ([]compound, []candidate) {
	compounds := make([]compound, 0)
	cur := compound{}
	for _, s := range shards {
		cur.add(s)
		if cur.size > targetSizeBytes {
			compounds = append(compounds, cur)
			cur = compound{}
		}
	}
	return compounds, cur.shards
}

// callMerge calls zoekt-merge-index and captures its output. callMerge is a NOP
// if len(shards) <= 1.
func callMerge(shards []candidate) ([]byte, []byte, error) {
	if len(shards) <= 1 {
		return nil, nil, nil
	}

	cmd := exec.Command("zoekt-merge-index", "-")

	outBuf := &bytes.Buffer{}
	errBuf := &bytes.Buffer{}
	cmd.Stdout = outBuf
	cmd.Stderr = errBuf

	wc, err := cmd.StdinPipe()
	if err != nil {
		return nil, nil, err
	}

	go func() {
		for _, s := range shards {
			io.WriteString(wc, fmt.Sprintf("%s\n", s.path))
		}
		wc.Close()
	}()

	err = cmd.Run()
	// If err==nil we can safely delete the candidate shards. In case err!=nil we
	// don't know if a compound shard was created or not, so it is best to just
	// delete the candidate shards to avoid duplicate results in case a compound
	// shard was created after all.
	for _, s := range shards {
		os.Remove(s.path)
	}
	return outBuf.Bytes(), errBuf.Bytes(), err
}
