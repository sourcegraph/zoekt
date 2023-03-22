package main

import (
	"bufio"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/xvandish/zoekt"
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

	tmpName, dstName, err := zoekt.Merge(dstDir, files...)
	if err != nil {
		return err
	}

	// Delete input shards.
	for _, name := range names {
		paths, err := zoekt.IndexFilePaths(name)
		if err != nil {
			return fmt.Errorf("zoekt-merge-index: %w", err)
		}
		for _, p := range paths {
			if err := os.Remove(p); err != nil {
				return fmt.Errorf("zoekt-merge-index: failed to remove simple shard: %w", err)
			}
		}
	}

	// We only rename the compound shard if all simple shards could be deleted in the
	// previous step. This guarantees we won't have duplicate indexes.
	if err := os.Rename(tmpName, dstName); err != nil {
		return fmt.Errorf("zoekt-merge-index: failed to rename compound shard: %w", err)
	}
	return nil
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

	return merge(filepath.Dir(paths[0]), paths)
}

// explode splits the input shard into individual shards and places them in dstDir.
// Temporary files created in the process are removed on a best effort basis.
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
		// best effort removal of tmp files. If os.Remove fails, indexserver will delete
		// the leftover tmp files during the next cleanup.
		for tmpFn := range exploded {
			os.Remove(tmpFn)
		}
	}()
	if err != nil {
		return fmt.Errorf("zoekt.Explode: %w", err)
	}

	// remove the input shard first to avoid duplicate indexes. In the worst case,
	// the process is interrupted just after we delete the compound shard, in which
	// case we have to reindex the lost repos.
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

	// best effort rename shards.
	for tmpFn, dstFn := range exploded {
		if err := os.Rename(tmpFn, dstFn); err != nil {
			log.Printf("explode: rename failed: %s", err)
		}
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
