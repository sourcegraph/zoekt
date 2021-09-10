package main

import (
	"bufio"
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

	return zoekt.Merge(dstDir, files...)
}

func main() {
	paths := os.Args[1:]
	if paths[0] == "-" {
		paths = []string{}
		scanner := bufio.NewScanner(os.Stdin)
		for scanner.Scan() {
			paths = append(paths, strings.TrimSpace(scanner.Text()))
		}
		if err := scanner.Err(); err != nil {
			log.Fatal(err)
		}
		log.Printf("merging %d paths from stdin", len(paths))
	}
	err := merge(filepath.Dir(paths[0]), paths)
	if err != nil {
		log.Fatal(err)
	}
}
