package mountinfo

import (
	"log"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/sourcegraph/log/logtest"
)

func Test_DeviceName_Fixtures(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		log.Fatalf("getting current working directory: %s", err)
	}

	// This test uses the

	mockSysFSFolder := filepath.Join(wd, "fixtures", "sys")
	mockGetDeviceNumber := func(devicePath string) (major uint32, minor uint32, err error) {
		return 254, 1, nil
	}

	filePath := "fakeTestFolder" // doesn't matter since we're hard-coding the device number above

	logger := logtest.Scoped(t)
	actual, err := discoverDeviceName(logger, discoverDeviceNameConfig{
		sysfsMountPoint: mockSysFSFolder,
		getDeviceNumber: mockGetDeviceNumber,
	}, filePath)

	if err != nil {
		t.Fatalf("discovering device name for file path %q: %s", filePath, err)
	}

	expectedDeviceName := "vda"

	if diff := cmp.Diff(expectedDeviceName, actual); diff != "" {
		t.Fatalf("recieved unexpected device name (-want +got):\n%s", diff)
	}
}
