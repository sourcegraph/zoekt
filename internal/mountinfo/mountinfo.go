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
const defaultSysMountPoint = "/sys"

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
	logger = logger.Scoped("mountPointInfo", "registration logic for mount_point_info Prometheus metric")

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
		// discover the name of the block device that stores <mountFilePath>.
		discoveryLogger := logger.Scoped("deviceNameDiscovery", "").With(
			sglog.String("mountName", name),
			sglog.String("mountFilePath", filePath),
		)

		device, err := discoverDeviceName(discoveryLogger, discoverDeviceNameConfig{}, filePath)
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

type discoverDeviceNameConfig struct {
	// sysfsMountPoint is the location of the sysfs mount point.
	// If empty, defaultSysMountPoint will be used instead.
	sysfsMountPoint string

	// getDeviceNumber, if non-nil, is the function that will be used to find
	// the number of the block device that stores the specified file.
	// If getDeviceNumber is nil, mountinfo.getDeviceNumber will be used instead.
	getDeviceNumber func(filePath string) (major uint32, minor uint32, err error)
}

// discoverDeviceName returns the name of the block device that filePath is
// stored on.
func discoverDeviceName(logger sglog.Logger, config discoverDeviceNameConfig, filePath string) (string, error) {
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
	// - https://www.kernel.org/doc/ols/2005/ols2005v1-pages-321-334.pdf

	getDeviceNumber := getDeviceNumber
	if config.getDeviceNumber != nil {
		getDeviceNumber = config.getDeviceNumber
	}

	sysfsMountPoint := defaultSysMountPoint
	if config.sysfsMountPoint != "" {
		sysfsMountPoint = config.sysfsMountPoint
	}

	sysfsMountPoint = filepath.Clean(sysfsMountPoint)

	// the provided sysfs mountpoint could itself be a symlink, so we
	// resolve it immediately so that future file path
	// evaluations / massaging doesn't break
	sysfsMountPoint, err := filepath.EvalSymlinks(sysfsMountPoint)
	if err != nil {
		return "", fmt.Errorf("verifying sysfs mountpoint %q: failed to resolve symlink %w", sysfsMountPoint, err)
	}

	major, minor, err := getDeviceNumber(filePath)
	if err != nil {
		return "", fmt.Errorf("discovering device number: %w", err)
	}

	// Represent the number in <major>:<minor> format.
	deviceNumber := fmt.Sprintf("%d:%d", major, minor)

	logger.Debug(
		"discovered device number",
		sglog.String("deviceNumber", deviceNumber),
	)

	// /sys/dev/block/<device_number> symlinks to /sys/devices/.../block/.../<deviceName>
	symlink := filepath.Join(sysfsMountPoint, "dev", "block", deviceNumber)

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

	// massage the sysfs folder name to ensure that it always ends in a '/'
	// so that strings.HasPrefix does what we expect when checking to see if
	// we're still under the /sys sub-folder
	sysFolderPrefix := strings.TrimSuffix(sysfsMountPoint, string(os.PathSeparator))
	sysFolderPrefix = sysFolderPrefix + string(os.PathSeparator)

	for {
		if !strings.HasPrefix(devicePath, sysFolderPrefix) {
			// ensure that we're still under the /sys/ sub-folder
			return "", fmt.Errorf("validating device path: device path %q isn't a subpath of %q", devicePath, sysFolderPrefix)
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

func getDeviceNumber(filePath string) (major uint32, minor uint32, err error) {
	var stat unix.Stat_t
	err = unix.Stat(filePath, &stat)
	if err != nil {
		return 0, 0, fmt.Errorf("failed to stat %q: %w", filePath, err)
	}

	major, minor = unix.Major(uint64(stat.Dev)), unix.Minor(uint64(stat.Dev))
	return major, minor, nil
}
