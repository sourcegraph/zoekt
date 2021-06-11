package main

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

const publicSetFileName = "public.txt"

var (
	metricNumPublicSet = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "index_num_public_set",
		Help: "Number of indexed repos that are in the public set.",
	})

	metricPublicSetUpdate = promauto.NewCounter(prometheus.CounterOpts{
		Name: "index_public_set_update_total",
		Help: "Total number of times the public set has been updated.",
	})
)

// writePublicSet will write public to public.txt. It will first read the
// existing public.txt file and include those repos in failed. This is to
// prevent intermittent failures polling for options removing a repository
// from the public corpus. The assumption is we will converge to a working get
// option.
//
// public.txt is a sorted list of repository names, one per line. This format
// was chosen for easy introspection with command line tools.
func writePublicSet(dir string, public []string, failed map[string]struct{}) error {
	// We want to keep values that used to be marked public, but we failed to
	// find out the current visibility state. We treat getIndexOpts as best
	// effort, so when it fails we use the old visibility.
	var old []string
	err := readPublicSetFunc(dir, func(repo string) {
		old = append(old, repo)
		if _, ok := failed[repo]; ok {
			public = append(public, repo)
		}
	})
	if err != nil {
		return fmt.Errorf("failed parsing public.txt: %w", err)
	}

	metricNumPublicSet.Set(float64(len(public)))

	sort.Strings(old)
	sort.Strings(public)

	// Check if we need to update.
	if len(public) == len(old) {
		same := true
		for i := range public {
			if public[i] != old[i] {
				same = false
				break
			}
		}
		if same {
			return nil
		}
	}

	// write to temp file first so we can do atomic update.
	dst := filepath.Join(dir, publicSetFileName)
	out, err := os.OpenFile(dst+".tmp", os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
	if err != nil {
		return fmt.Errorf("failed to write public.txt.tmp: %w", err)
	}
	defer os.Remove(dst + ".tmp")
	defer out.Close()

	w := bufio.NewWriter(out)
	for _, repo := range public {
		// can ignore error since it will be returned by flush
		_, _ = w.WriteString(repo)
		_ = w.WriteByte('\n')
	}
	if err := w.Flush(); err != nil {
		return fmt.Errorf("failed to write public.txt.tmp: %w", err)
	}
	if err := out.Close(); err != nil {
		return fmt.Errorf("failed to write public.txt.tmp: %w", err)
	}

	if err := os.Rename(dst+".tmp", dst); err != nil {
		return fmt.Errorf("failed to write public.txt: %w", err)
	}

	metricPublicSetUpdate.Inc()

	return nil
}

func readPublicSetFunc(dir string, f func(string)) error {
	file, err := os.Open(filepath.Join(dir, publicSetFileName))
	if os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		repo := strings.TrimSpace(scanner.Text())
		if repo != "" {
			f(repo)
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}

	return nil
}
