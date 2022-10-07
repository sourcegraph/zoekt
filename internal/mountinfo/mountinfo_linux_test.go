package mountinfo

import (
	"os"
	"testing"

	"github.com/sourcegraph/log/logtest"
)

func Test_MountInfo_SmokeTest_Github_Actions(t *testing.T) {
	if os.Getenv("GITHUB_ACTIONS") == "" {
		t.Skip("this smoke test should only run in our Github Actions CI environment")
	}

	logger := logtest.Scoped(t)

	// A simple smoke test to verify that we can find the storage device
	// for the location of the zoekt checkout on the CI agent.
	filePath := os.Getenv("GITHUB_WORKSPACE")
	device, err := discoverDeviceName(logger, filePath)
	if err != nil {
		t.Fatal(err)
	}

	t.Logf("discovered device name %q for mount path %q", device, filePath)
}
