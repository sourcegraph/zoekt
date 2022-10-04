package sysinfo

import (
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/procfs"
	"golang.org/x/sys/unix"
)

func NewMountInfoCollector(fs procfs.FS, mounts map[string]string) (prometheus.Collector, error) {

	labels := make(prometheus.Labels)

	for name, path := range mounts {
		deviceName, err := underlyingBlockDevice(path)

		if err != nil {
			return nil, fmt.Errorf("finding underlying block device for %q (path %q): %w", name, path, err)
		}

		labels[name] = deviceName
	}

	log.Printf("%+v", labels)

	//mounts, err := self.MountStats()
	//if err != nil {
	//	return nil, fmt.Errorf("reading mount stats: %w", err)
	//}

	//for _, m := range mounts {
	//	labels[m.Mount] = m.Device
	//}

	return prometheus.NewGaugeFunc(
		prometheus.GaugeOpts{
			Name:        "mount_points",
			Help:        "An info metric with a constant '1' value that contains [mount_point]=[device_name] label mappings",
			ConstLabels: labels,
		},
		func() float64 { return 1 },
	), nil
}

func NewMachineNameCollector() (prometheus.Collector, error) {
	opts := prometheus.GaugeOpts{
		Name: "machine_name",
		Help: "An Info metric with a constant '1' value that contains the name of the underlying machine that this process is running on.",
	}

	name, ok := os.LookupEnv("MACHINE_NAME")
	if ok {
		opts.ConstLabels = prometheus.Labels{"name": name}

		return prometheus.NewGaugeFunc(opts, func() float64 { return 1 }), nil
	}

	return nil, fmt.Errorf("unable to discover name of machine")
}

func NewGoBuildInfoCollector() prometheus.Collector {
	return collectors.NewBuildInfoCollector()
}

func underlyingBlockDevice(path string) (string, error) {
	// In general, this seems quite involved to handle every case.
	// https://unix.stackexchange.com/questions/11311/how-do-i-find-on-which-physical-device-a-folder-is-located

	// This first will handle filepaths with only "

	// First, discover the number of the device that's backing "path"
	// See the docs for more information https://man7.org/linux/man-pages/man5/proc.5.html
	//
	// Note:

	var stat unix.Stat_t
	err := unix.Stat(path, &stat)
	if err != nil {
		return "", fmt.Errorf("failed to discover device number for path %q: %w", path, err)
	}

	major, minor := unix.Major(uint64(stat.Dev)), unix.Minor(uint64(stat.Dev))
	deviceNumber := fmt.Sprintf("%d:%d", major, minor)

	// Second, discover the "canonical name" of the device via sysfs. See the following for more information
	// https://www.kernel.org/doc/html/latest/filesystems/sysfs.html#top-level-directory-layout

	// /sys/dev/block/[major]:[minor] is a symlink to /sys/devices
	sysfsDeviceNumberPath := filepath.Join("/", "sys", "dev", "block", deviceNumber)
	sysfsDeviceNamePath, err := os.Readlink(sysfsDeviceNumberPath)
	if err != nil {
		return "", fmt.Errorf("failed to evaluate symlink for device number %q: %w", deviceNumber, err)
	}

	// Is this device a partition?

	if _, err := os.Stat(filepath.Join(sysfsDeviceNamePath, "partition")); errors.Is(err, fs.ErrNotExist) {
		parent := filepath.Dir(sysfsDeviceNamePath)
		return filepath.Base(parent), nil
	}

	return filepath.Base(sysfsDeviceNamePath), nil

}
