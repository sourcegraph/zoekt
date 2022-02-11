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
func explode(dstDir string, inputShard string) error {
	f, err := os.Open(inputShard)
	if err != nil {
		return err
	}
	defer f.Close()

	indexFile, err := zoekt.NewIndexFile(f)
	if err != nil {
		return err
	}
	defer indexFile.Close()

	exploded, err := zoekt.Explode(dstDir, indexFile)
	defer func() {
		// best effort removal of tmp files. If os.Remove failes, indexserver will delete
		// the leftover tmp files during the next cleanup.
		for tmpFn := range exploded {
			os.Remove(tmpFn)
		}
	}()
	if err != nil {
		return fmt.Errorf("zoekt.Explode: %w", err)
	}
	var fns []string
	for tmpFn, dstFn := range exploded {
		err = os.Rename(tmpFn, dstFn)
		if err != nil {
			// clean up the shards we already renamed to avoid duplicate results.
			for _, fn := range fns {
				os.Remove(fn)
			}
			return fmt.Errorf("explode: rename failed: %w", err)
		}
		fns = append(fns, dstFn)
	}

	// Don't remove the input shard if its name matches one of the destination
	// shards. This can happen, for example, if the input shard is a simple shard.
	for _, dstFn := range exploded {
		if dstFn == inputShard {
			return nil
		}
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
		return fmt.Errorf("explode: error removing input shard %s: %w", inputShard, err)
	}
	return nil
}

func explodeCmd(path string) error {
	return explode(filepath.Dir(path), path)
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
