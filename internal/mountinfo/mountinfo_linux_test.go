//go:build linux

package mountinfo

import (
	"log"
	"os"
	"testing"

	"github.com/sourcegraph/log/logtest"
)

func Test_DeviceName_SmokeTest(t *testing.T) {
	// A simple smoke test to verify that we can find the storage device
	// for the current working directory.
	logger := logtest.Scoped(t)

	filePath, err := os.Getwd()
	if err != nil {
		log.Fatalf("getting current working directory: %s", err)
	}

	device, err := discoverDeviceName(logger, discoverDeviceNameOpts{}, filePath)
	if err != nil {
		t.Fatalf("discovering device name for file path %q: %s", filePath, err)
	}

	t.Logf("discovered device name %q for file path %q", device, filePath)
}
