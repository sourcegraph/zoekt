//go:build linux

package mountinfo

import (
	"log"
	"os"
	"testing"

	"github.com/sourcegraph/log/logtest"
)

func Test_MountInfo_SmokeTest(t *testing.T) {
	logger := logtest.Scoped(t)

	// A simple smoke test to verify that we can find the storage device
	// for the current working directory
	filePath, err := os.Getwd()
	if err != nil {
		log.Fatalf("getting currrent working directory: %s", err)
	}

	device, err := discoverDeviceName(logger, filePath)
	if err != nil {
		t.Fatal(err)
	}

	t.Logf("discovered device name %q for mount path %q", device, filePath)
}
