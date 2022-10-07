package mountinfo

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	sglog "github.com/sourcegraph/log"
	"golang.org/x/sys/unix"
)

// defaultSysMountPoint is the common mount point for the sysfs pseudo-filesystem.
const defaultSysMountPoint = "/sys/"

// MustRegisterNewMountPointInfoMetric registers a Prometheus metric named "mount_point_info" that
// contains the names of the block storage devices that back each of the requested mounts.
//
// Mounts is a set of name -> file path mappings (example: {"indexDir": "/home/.zoekt"}).
//
// The metric "mount_point_info" has a constant value of 1 and two labels:
//   - mount_name: caller-provided name for the given mount (example: "indexDir")
//   - device: name of the block device that backs the given mount file path (example: "sdb")
//
// This metric only works on Linux-based operating systems that have access to the sysfs pseudo-filesystem.
// On all other operating systems, this metric will not emit any values.
func MustRegisterNewMountPointInfoMetric(logger sglog.Logger, mounts map[string]string) {
	metric := promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "mount_point_info",
		Help: "An info metric with a constant '1' value that contains mount_name, device mappings",
	}, []string{"mount_name", "device"})

	// This device discovery logic relies on the sysfs pseudo-filesystem, which only exists
	// on linux.
	//
	// See https://en.wikipedia.org/wiki/Sysfs for more information.
	if runtime.GOOS != "linux" {
		return
	}

	for name, filePath := range mounts {
		// for each <mountName>:<mountFilePath> pairing,
		// discover the name of the block device that stores <mountFilePath>.Ø
		discoveryLogger := logger.Scoped("deviceNameDiscovery", "").With(
			sglog.String("mountName", name),
			sglog.String("mountFilePath", filePath),
		)

		device, err := discoverDeviceName(discoveryLogger, filePath)
		if err != nil {
			discoveryLogger.Warn("skipping metric registration",
				sglog.String("reason", "failed to discover device name"),
				sglog.Error(err),
			)

			continue
		}

		discoveryLogger.Debug("discovered device name",
			sglog.String("deviceName", device),
		)

		metric.WithLabelValues(name, device).Set(1)
	}
}

// discoverDeviceName returns the name of the block device that filePath is
// stored on.
func discoverDeviceName(logger sglog.Logger, filePath string) (string, error) {
	// Note: It's quite involved to implement the device discovery logic for
	// every possible kind of storage device (e.x. logical volumes, NFS, etc.) See
	// https://unix.stackexchange.com/a/11312 for more information.
	//
	// As a result, this logic will only work correctly for filePaths that are either:
	// - stored directly on a block device
	// - stored on a block device's partition
	//
	// For all other device types, this logic will either:
	// - return an incorrect device name
	// - return an error
	//
	// This logic was implemented from information gathered from the following sources (amongst others):
	// - "The Linux Programming Interface" by Michael Kerrisk: Chapter 14
	// - "Linux Kernel Development" by Robert Love: Chapters 13, 17
	// - https://man7.org/linux/man-pages/man5/sysfs.5.html
	// - https://en.wikipedia.org/wiki/Sysfs
	// - https://unix.stackexchange.com/a/11312

	var stat unix.Stat_t
	err := unix.Stat(filePath, &stat)
	if err != nil {
		return "", fmt.Errorf("discovering device number: failed to stat %q: %w", filePath, err)
	}

	// extract the major and minor portions of the device ID, and
	// represent it in <major>:<minor> format
	major, minor := unix.Major(uint64(stat.Dev)), unix.Minor(uint64(stat.Dev))
	deviceNumber := fmt.Sprintf("%d:%d", major, minor)

	logger.Debug(
		"discovered device number",
		sglog.String("deviceNumber", deviceNumber),
	)

	// /sys/dev/block/<device_number> symlinks to /sys/devices/.../block/.../<discoverDeviceName>
	symlink := filepath.Join(defaultSysMountPoint, "dev", "block", deviceNumber)

	devicePath, err := filepath.EvalSymlinks(symlink)
	if err != nil {
		return "", fmt.Errorf("discovering device path: failed to evaluate sysfs symlink %q: %w", symlink, err)
	}

	devicePath, err = filepath.Abs(devicePath)
	if err != nil {
		return "", fmt.Errorf("discovering device path: failed to massage device path %q to absolute path: %w", devicePath, err)
	}

	logger.Debug("discovered device path",
		sglog.String("devicePath", devicePath),
	)

	// Check to see if devicePath points to a disk partition. If so, we need to find the parent
	// device.

	for {
		if !strings.HasPrefix(devicePath, defaultSysMountPoint) {
			// ensure that we're still under the /sys/ sub-folder
			return "", fmt.Errorf("validating device path: device path %q isn't a subpath of %q", devicePath, defaultSysMountPoint)
		}

		_, err := os.Stat(filepath.Join(devicePath, "partition"))
		if errors.Is(err, os.ErrNotExist) {
			break
		}

		parent := filepath.Dir(devicePath)

		logger.Debug("changing device path",
			sglog.String("reason", "oldDevicePath represents a disk partition"),

			sglog.String("oldDevicePath", devicePath),
			sglog.String("newDevicePath", parent),
		)

		devicePath = parent
	}

	// This devicePath should have an entry in the device tree
	// if it represents a block device (and not a partition).

	_, err = os.Stat(filepath.Join(devicePath, "device"))
	if err != nil {
		return "", fmt.Errorf("validating device path: ensuring that device (path %q) has an entry in the device tree: %q", devicePath, err)
	}

	// If this device is a block device, its device path should have a symlink
	// to the block subsystem.

	subsystemPath, err := filepath.EvalSymlinks(filepath.Join(devicePath, "subsystem"))
	if err != nil {
		return "", fmt.Errorf("validating device path: failed to discover subsystem that device (path %q) is part of: %w", devicePath, err)
	}

	if filepath.Base(subsystemPath) != "block" {
		return "", fmt.Errorf("validating device path: device (path %q) is not part of the block subsystem", devicePath)
	}

	device := filepath.Base(devicePath)
	return filepath.Base(device), nil
}
