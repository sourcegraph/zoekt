package mountinfo

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	sglog "github.com/sourcegraph/log"
	"golang.org/x/sys/unix"
)

// DefaultSysMountPoint is the common mount point for the sysfs pseudo-filesystem.
const DefaultSysMountPoint = "/sys"

// MustRegisterNewMountPointInfoMetric registers a Prometheus metric named "mount_point_info" that
// contains the names of the block storage devices that back each of the requested mounts.
//
// Mounts is a set of name -> file path mappings (example: {"indexDir": "/home/.zoekt"}).
//
// sysMountPoint is the custom mount point for the sysfs pseudo-filesystem to use. If empty,
// DefaultSysMountPoint will be used instead.
//
// The metric "mount_point_info" has a constant value of 1 and two labels:
//   - mount_name: caller-provided name for the given mount (example: "indexDir")
//   - device: name of the block device that backs the given mount file path (example: "sdb")
//
// This metric only works on Linux-based operating systems that have access to the sysfs pseudo-filesystem.
// On all other operating systems, this metric will not emit any values.
func MustRegisterNewMountPointInfoMetric(logger sglog.Logger, sysMountPoint string, mounts map[string]string) {
	if sysMountPoint == "" {
		sysMountPoint = DefaultSysMountPoint
	}

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

	for mountName, mountFilePath := range mounts {
		// For each <mountName>:<mountFilePath> pairing, we need to
		// discover the name of the block device that stores <mountFilePath>.
		//
		// Example: For a mount named "indexDir" with a filePath of "~/.zoekt", this logic will
		// discover that "sdb" is the disk that backs "~/.zoekt".
		//
		// Note: It's quite involved to implement the device discovery logic for
		// every possible kind of storage device (e.x. logical volumes, NFS, etc.) See
		// https://unix.stackexchange.com/a/11312 for more information.
		//
		//  As a result, this logic will only work correctly for mountFilePaths that are either:
		// 	- stored directly on a block device
		//	- stored on a block device's partition
		//
		// For all other device types, this logic will either:
		// - find an incorrect device name
		// - emit an error in the logs
		//
		// This logic was implemented from information gathered from the following sources (amongst others):
		// - "The Linux Programming Interface" by Michael Kerrisk: Chapter 14
		// - "Linux Kernel Development" by Robert Love: Chapters 13, 17
		// - https://man7.org/linux/man-pages/man5/sysfs.5.html
		// - https://en.wikipedia.org/wiki/Sysfs
		// - https://unix.stackexchange.com/a/11312

		// 'stat' the mount's mountFilePath path to determine what the device's ID numbers is
		discoveryLogger := logger.Scoped("deviceNameDiscovery", "").
			With(sglog.String("mountName", mountName)).
			With(sglog.String("mountFilePath", mountFilePath))

		discoveryLogger.Debug(
			"'stat'-ing mountFilePath",
			sglog.String("operation", "discovering device number"),
		)

		var stat unix.Stat_t
		err := unix.Stat(mountFilePath, &stat)
		if err != nil {
			discoveryLogger.Debug("failed to stat",
				sglog.String("operation", "discovering device number"),
				sglog.Error(err),
			)

			continue
		}

		// extract the major and minor portions of the device ID, and
		// represent it in <major>:<minor> format
		major, minor := unix.Major(uint64(stat.Dev)), unix.Minor(uint64(stat.Dev))
		deviceNumber := fmt.Sprintf("%d:%d", major, minor)

		discoveryLogger.Debug("discovered device number",
			sglog.String("operation", "discovering device number"),
			sglog.String("deviceNumber", deviceNumber),
		)

		// /sys/dev/block/<device_number> symlinks to /sys/devices/.../block/.../<deviceName>
		symlink := filepath.Join(sysMountPoint, "dev", "block", deviceNumber)

		discoveryLogger.Debug("evaluating sysfs symlink",
			sglog.String("operation", "discovering device path"),
			sglog.String("symlink", symlink),
		)

		devicePath, err := filepath.EvalSymlinks(symlink)
		if err != nil {
			discoveryLogger.Debug("failed to evaluate sysfs symlink",
				sglog.String("operation", "discovering device path"),
				sglog.Error(err),
			)

			continue
		}

		discoveryLogger.Debug("discovered device path",
			sglog.String("operation", "discovering device path"),
			sglog.String("devicePath", devicePath),
		)

		// Check to see if devicePath points to a disk partition. If so, we need to find the parent
		// device.

		for {
			if devicePath == "" || devicePath == "/" || devicePath == "." {
				// stop walking up the folder hierarchy once we have an empty path (or a terminal as defined by filepath.Dir)
				break
			}

			_, err := os.Stat(filepath.Join(devicePath, "partition"))
			if errors.Is(err, os.ErrNotExist) {
				break
			}

			parent := filepath.Dir(devicePath)

			discoveryLogger.Debug("changing device path since oldDevicePath represents a disk partition",
				sglog.String("operation", "validating device path"),

				sglog.String("oldDevicePath", devicePath),
				sglog.String("newDevicePath", parent),
			)

			devicePath = parent
		}

		// This devicePath should have an entry in the device tree
		// if it represents a block device (and not a partition).

		_, err = os.Stat(filepath.Join(devicePath, "device"))
		if err != nil {
			discoveryLogger.Debug("failed to ensure that the device has an entry in the device tree",
				sglog.String("operation", "validating device path"),
				sglog.String("devicePath", devicePath),

				sglog.Error(err),
			)

			continue
		}

		// If this device is a block device, its device path should have a symlink
		// to the block subsystem.

		subsystemPath, err := filepath.EvalSymlinks(filepath.Join(devicePath, "subsystem"))
		if err != nil {
			discoveryLogger.Debug("failed to discover subsystem that the device is part of",
				sglog.String("operation", "validating device path"),
				sglog.String("devicePath", devicePath),

				sglog.Error(err),
			)

			continue
		}

		if filepath.Base(subsystemPath) != "block" {
			discoveryLogger.Debug("device is not part of the 'block' subsystem",
				sglog.String("operation", "validating device path"),
				sglog.String("devicePath", devicePath),

				sglog.String("subsystemPath", subsystemPath),
			)

			continue
		}

		deviceName := filepath.Base(devicePath)

		discoveryLogger.Debug("discovered device name",
			sglog.String("deviceName", deviceName),
		)

		metric.WithLabelValues(mountName, deviceName).Set(1)
	}
}
