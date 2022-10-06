package sysinfo

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

const defaultSysMountPoint = "/sys"

// RegisterNewMountPointInfoMetric registers a Prometheus info metric named "mount_point_info" that
// contains the name of the block device that backs the file path associated with each of the provided mounts.
//
// The metric has a constant value of 1 and two labels:
//   - mount_name: caller-provided name for the given mount (example: "index_dir")
//   - device: name of the block device that backs the given mount (example: "sdb")
//
// The 'mounts; argument is a list of {"index_dir": "/home/.zoekt"}
//
// with a constant value 1 and two labels:
// - "mount_name"
func RegisterNewMountPointInfoMetric(logger sglog.Logger, sysMountPoint string, mounts map[string]string) {
	// This device discovery logic relies on the sysfs mountFilePath system, which only exists
	// on linux.
	//
	// See https://en.wikipedia.org/wiki/Sysfs for more information.
	if runtime.GOOS != "linux" {
		return
	}

	if sysMountPoint == "" {
		sysMountPoint = defaultSysMountPoint
	}

	metric := promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "mount_point_info",
		Help: "An info metric with a constant '1' value that contains mount_name, device mappings",
	}, []string{"mount_name", "device"})

	for mountName, mountFilePath := range mounts {
		// For each <mount_name>:<file_path> pairing, we need to find the name of the device
		// that stores <file_path>.
		//
		// E.x. For a mount named "indexDir" that points to "~/.zoekt", this logic will
		// discover that "sdb" is the disk that backs "~/.zoekt"
		//
		// In general, it's quite involved to handle every possible case:
		//https://unix.stackexchange.com/questions/11311/how-do-i-find-on-which-physical-device-a-folder-is-located
		//
		// This logic will focus on handling

		// 'stat' the mount's mountFilePath path to determine what the device's ID numbers is
		discoveryLogger := logger.Scoped("deviceNameDiscovery", "").
			With(sglog.String("mountName", mountName)).
			With(sglog.String("mountFilePath", mountFilePath))

		discoveryLogger.Debug(
			"'stat'-ing mount filePath",
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

		for !(devicePath == "" || devicePath == "/" || devicePath == ".") { // stop walking up the folder hierarchy once we have an empty path (or a terminal as defined by filepath.Dir)
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
