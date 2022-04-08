package main

import (
	"flag"
	"testing"
)

func TestLogDirFlag(t *testing.T) {

	logDirFlag := flag.Lookup("log_dir")
	if logDirFlag == nil {
		t.Fatal("log_dir flag not found, this breaks OSS users. Was a dependency modified?")
	}
}
