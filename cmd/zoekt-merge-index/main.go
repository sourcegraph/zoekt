package main

import (
	"bufio"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/zoekt"
)

func merge(dstDir string, names []string) error {
	var files []zoekt.IndexFile
	for _, fn := range names {
		f, err := os.Open(fn)
		if err != nil {
			return err
		}
		defer f.Close()

		indexFile, err := zoekt.NewIndexFile(f)
		if err != nil {
			return err
		}
		defer indexFile.Close()

		files = append(files, indexFile)
	}

	_, err := zoekt.Merge(dstDir, files...)
	return err
}

func mergeCmd(paths []string) error {
	if paths[0] == "-" {
		paths = []string{}
		scanner := bufio.NewScanner(os.Stdin)
		for scanner.Scan() {
			paths = append(paths, strings.TrimSpace(scanner.Text()))
		}
		if err := scanner.Err(); err != nil {
			return err
		}
		log.Printf("merging %d paths from stdin", len(paths))
	}
	err := merge(filepath.Dir(paths[0]), paths)
	if err != nil {
		return err
	}
	return nil
}

// explode splits a shard into indiviual shards and places them in dstDir.
// If it returns without error, the input shard was deleted and the first
// result contains the list of all new shards.
//
// explode cleans up tmp files created in the process on a best effort basis.
func explode(dstDir string, inputShard string) ([]string, error) {
	var tmpFns []string
	var err error

	defer func() {
		// best effort removal of tmp files. If os.Remove failes, indexserver will delete
		// the leftover tmp files during the next cleanup.
		for _, fn := range tmpFns {
			os.Remove(fn)
		}
	}()

	f, err := os.Open(inputShard)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	indexFile, err := zoekt.NewIndexFile(f)
	if err != nil {
		return nil, err
	}
	defer indexFile.Close()

	tmpFns, err = zoekt.Explode(dstDir, indexFile)
	if err != nil {
		return nil, fmt.Errorf("exloded failed: %w", err)
	}
	var fns []string
	for _, tmpFn := range tmpFns {
		fn := strings.TrimSuffix(tmpFn, ".tmp")
		err = os.Rename(tmpFn, fn)
		if err != nil {
			// clean up the shards we already renamed to avoid duplicate results.
			for _, fn := range fns {
				os.Remove(fn)
			}
			return nil, fmt.Errorf("explode: rename failed: %w", err)
		}
		fns = append(fns, fn)
	}

	// Special case: If the input shard was a simple shard, then we have already
	// overwritten the original shard in the previous step with os.Rename. Deleting
	// the input shard in the next step would leave us with no shard. This behavior
	// is due to the fact that simple shards are named based on the name of the repo
	// they contain.
	if len(tmpFns) == 1 && strings.TrimSuffix(tmpFns[0], ".tmp") == inputShard {
		return fns, nil
	}

	removeInputShard := func() (err error) {
		defer func() {
			if err != nil {
				// delete the new shards to avoid duplicate results.
				for _, fn := range fns {
					os.Remove(fn)
				}
			}
		}()

		paths, err := zoekt.IndexFilePaths(inputShard)
		if err != nil {
			return err
		}
		for _, path := range paths {
			err = os.Remove(path)
			if err != nil {
				return err
			}
		}
		return nil
	}

	if err = removeInputShard(); err != nil {
		return nil, fmt.Errorf("explode: error removing shard %s: %w", inputShard, err)
	}
	return fns, nil
}

func explodeCmd(path string) error {
	_, err := explode(filepath.Dir(path), path)
	return err
}

func main() {
	switch subCommand := os.Args[1]; subCommand {
	case "merge":
		if err := mergeCmd(os.Args[2:]); err != nil {
			log.Fatal(err)
		}
	case "explode":
		if err := explodeCmd(os.Args[2]); err != nil {
			log.Fatal(err)
		}
	default:
		log.Fatalf("unknown subcommand %s", subCommand)
	}

}
