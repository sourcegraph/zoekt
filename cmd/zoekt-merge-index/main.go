package main

import (
	"log"
	"os"
	"path/filepath"

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
	err := merge(filepath.Dir(os.Args[1]), os.Args[1:])
	if err != nil {
		log.Fatal(err)
	}
}
