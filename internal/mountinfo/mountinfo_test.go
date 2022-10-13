package mountinfo

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/sourcegraph/log/logtest"
)

func Test_DeviceName_Partition(t *testing.T) {
	// This test use a sysfs snapshot from a dev docker-compose sourcegraph
	// deployment to verify that the device discovery logic returns the expected
	// device name.
	//
	// This particular test also happens target a device that is a partition (
	// ensure that our logic for determining the name of the underlying device backing
	// the partition is sound)
	//
	// ( lsblk output from the snapshotted machine for more context of the test setup)
	// ~ # lsblk
	// NAME   MAJ:MIN RM  SIZE RO TYPE MOUNTPOINTS
	// nbd0    43:0    0    0B  0 disk
	// nbd1    43:32   0    0B  0 disk
	// nbd2    43:64   0    0B  0 disk
	// nbd3    43:96   0    0B  0 disk
	// nbd4    43:128  0    0B  0 disk
	// nbd5    43:160  0    0B  0 disk
	// nbd6    43:192  0    0B  0 disk
	// nbd7    43:224  0    0B  0 disk
	// vda    254:0    0 59.6G  0 disk
	// └─vda1 254:1    0 59.6G  0 part /etc/hosts  # test targets this partition
	//                                 /etc/hostname
	//                                 /etc/resolv.conf
	//                                 /data/index
	// nbd8    43:256  0    0B  0 disk
	// nbd9    43:288  0    0B  0 disk
	// nbd10   43:320  0    0B  0 disk
	// nbd11   43:352  0    0B  0 disk
	// nbd12   43:384  0    0B  0 disk
	// nbd13   43:416  0    0B  0 disk
	// nbd14   43:448  0    0B  0 disk
	// nbd15   43:480  0    0B  0 disk

	// hard-code the device number for "vda1" from the sysfs snapshot
	mockGetDeviceNumber := func(devicePath string) (major uint32, minor uint32, err error) {
		return 254, 1, nil
	}

	// provide a custom sysfs location so that we can point the test
	// at our unpacked sysfs snapshot
	mockSysFSFolder := filepath.Join(t.TempDir(), "sys")

	// the filepath provided to the function doesn't matter since we're
	// hard-coding the device number
	fakeFilePath := "doesn't matter"

	// unpack the snapshot into our mock sysfs location
	wd, err := os.Getwd()
	if err != nil {
		log.Fatalf("getting current working directory: %s", err)
	}

	tarball := filepath.Join(wd, "testdata", "sysfs.tar.gz")
	err = decompressGzipTarball(tarball, mockSysFSFolder)
	if err != nil {
		log.Fatalf("decompressing sysfs tarball (%q): %s", tarball, err)
	}

	// execute the test with our injected mocks
	actualDevice, err := discoverDeviceName(
		logtest.Scoped(t),
		discoverDeviceNameConfig{
			sysfsMountPoint: mockSysFSFolder,
			getDeviceNumber: mockGetDeviceNumber,
		},
		fakeFilePath,
	)

	if err != nil {
		t.Fatalf("discovering device name for file path %q: %s", fakeFilePath, err)
	}

	// verify that the discovered device name is the one that we expect from the snapshot
	expectedDevice := "vda"
	if diff := cmp.Diff(expectedDevice, actualDevice); diff != "" {
		t.Fatalf("recieved unexpected device name (-want +got):\n%s", diff)
	}
}

func decompressGzipTarball(tarball, outputFolder string) error {
	file, err := os.Open(tarball)
	if err != nil {
		return fmt.Errorf("opening tarball %q: %w", tarball, err)
	}

	defer file.Close()

	gz, err := gzip.NewReader(file)
	if err != nil {
		return fmt.Errorf("initializing gzip reader: %s", err)
	}

	reader := tar.NewReader(gz)

	for {
		header, err := reader.Next()
		if err == io.EOF {
			break
		}

		if err != nil {
			return fmt.Errorf("initializing tar reader: %w", err)
		}

		outputFile := filepath.Join(outputFolder, header.Name)

		switch header.Typeflag {
		case tar.TypeDir:
			err := os.MkdirAll(outputFile, os.FileMode(header.Mode))
			if err != nil {
				return fmt.Errorf("creating directory %q: %w", outputFile, err)
			}

		case tar.TypeSymlink:
			err := os.Symlink(header.Linkname, outputFile)
			if err != nil {
				return fmt.Errorf("creating symlink (%q -> %q): %w", outputFile, header.Linkname, err)
			}

		case tar.TypeReg:
			f, err := os.OpenFile(outputFile, os.O_CREATE|os.O_RDWR, os.FileMode(header.Mode))
			if err != nil {
				return fmt.Errorf("creating file %q: %w", outputFile, err)
			}

			_, err = io.Copy(f, reader)
			if err != nil {
				return fmt.Errorf("writing file %q: %s", outputFile, err)
			}

			f.Close()

		default:
			return fmt.Errorf("encounted unknown file header type (%d) for file %q", header.Typeflag, header.Name)
		}
	}

	return nil
}
