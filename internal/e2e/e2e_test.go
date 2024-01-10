package e2e

import (
	"flag"
	"io"
	"log"
	"os"
	"testing"
)

func TestMain(m *testing.M) {
	flag.Parse()
	if !testing.Verbose() {
		log.SetOutput(io.Discard)
	}
	os.Exit(m.Run())
}
