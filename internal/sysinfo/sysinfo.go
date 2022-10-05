package sysinfo

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/prometheus/client_golang/prometheus"
	"golang.org/x/sys/unix"
)

const defaultSysMountPoint = "/sys"

// NewMountInfoCollector returns a prometheus.Collector that collects a single metric "mount_points"
// that contains
//
// with a constant value 1 and two labels:
// - "mount_name"
func NewMountInfoCollector(sysMountPoint string, mounts map[string]string) (prometheus.Collector, error) {
	if sysMountPoint == "" {
		sysMountPoint = defaultSysMountPoint
	}

	collector := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "mount_point_info",
		Help: "An info metric with a constant '1' value that contains mount_name, device mappings",
	}, []string{"mount_name", "device"})

	for mountName, file := range mounts {
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

		// 'stat' the mount's file path to determine what the device's ID numbers is
		var stat unix.Stat_t
		err := unix.Stat(file, &stat)
		if err != nil {
			return nil, fmt.Errorf("failed to discover device number for mount %q (path %q): %w", mountName, file, err)
		}

		// extract the major and minor portions of the device ID, and
		// represent it in <major>:<minor> format
		major, minor := unix.Major(uint64(stat.Dev)), unix.Minor(uint64(stat.Dev))
		deviceNumber := fmt.Sprintf("%d:%d", major, minor)

		// /sys/dev/block/<device_number> symlinks to /sys/devices/.../block/.../<deviceName>
		p := filepath.Join(sysMountPoint, "dev", "block", deviceNumber)
		devicePath, err := os.Readlink(p)
		if err != nil {
			return nil, fmt.Errorf("failed to evaluate sysfs symlink for device number %q: %w", deviceNumber, err)
		}

		for {
			// Is this device a partition? If so, walk up the folder hierarchy until we find
			// the parent block device.
			_, err := os.Stat(filepath.Join(devicePath, "partition"))
			if errors.Is(err, fs.ErrNotExist) {
				break
			}

			if err != nil {
				return nil, fmt.Errorf("")
			}

			devicePath = filepath.Dir(devicePath)
		}

		// This path should have a "device" folder if this
		// represents a physical block device. Fail if any error occurs while checking this.
		_, err = os.Stat(filepath.Join(devicePath, "device"))
		if err != nil {
			return nil, fmt.Errorf("if device")
		}

		deviceName := filepath.Base(devicePath)
		collector.WithLabelValues(mountName, deviceName).Set(1)
	}

	return collector, nil
}
