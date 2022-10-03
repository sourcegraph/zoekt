package sysinfo

import (
	"fmt"
	"os"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/procfs"
)

func NewMountInfoCollector(fs procfs.FS) (prometheus.Collector, error) {
	self, err := fs.Self()
	if err != nil {
		return nil, fmt.Errorf("retrieving process statistics for current process: %w", err)
	}

	mounts, err := self.MountStats()
	if err != nil {
		return nil, fmt.Errorf("reading mount stats: %w", err)
	}

	labels := make(prometheus.Labels)

	for _, m := range mounts {
		labels[m.Mount] = m.Device
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
