package sysinfo

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"golang.org/x/sys/unix"
)

func NewMountInfoCollector(mounts map[string]string) (prometheus.Collector, error) {
	labels := make(prometheus.Labels, len(mounts))

	for mountName, file := range mounts {
		// For each named mount, we need to find the name block device
		// that backs each path.
		//
		// E.x. For a mount named "indexDir" that points to "~/.zoekt", this logic will
		// discover that "sdb" is the disk that backs "~/.zoekt"
		//
		// In general, it's quite involved to handle every possible case:
		//https://unix.stackexchange.com/questions/11311/how-do-i-find-on-which-physical-device-a-folder-is-located
		//
		// This logic will focus on handling

		var stat unix.Stat_t
		err := unix.Stat(file, &stat)
		if err != nil {
			return nil, fmt.Errorf("failed to discover device number for mount %q (path %q): %w", mountName, file, err)
		}

		major, minor := unix.Major(uint64(stat.Dev)), unix.Minor(uint64(stat.Dev))
		deviceNumber := fmt.Sprintf("%d:%d", major, minor)

		p := filepath.Join("/", "sys", "dev", "block", deviceNumber)
		devicePath, err := os.Readlink(p)
		if err != nil {
			return nil, fmt.Errorf("failed to evaluate sysfs symlink for device number %q: %w", deviceNumber, err)
		}

		if _, err := os.Stat(filepath.Join(devicePath, "partition")); errors.Is(err, fs.ErrNotExist) {
			// If it is a partition, then we want the name of the parent
			devicePath = filepath.Dir(devicePath)
		}

		labels[mountName] = filepath.Base(devicePath)
	}

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
