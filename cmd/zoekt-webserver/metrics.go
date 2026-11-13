package main

import (
	"log/slog"
	"path"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/procfs"

	"github.com/sourcegraph/zoekt/internal/logging"
)

func mustRegisterMemoryMapMetrics(logger *slog.Logger) {
	logger = logging.Scoped(logger, "memoryMapMetrics")

	// The memory map metrics are collected via /proc, which
	// is only available on linux-based operating systems.

	// Instantiate shared FS objects for accessing /proc and /proc/self,
	// and skip metrics registration if we're aren't able to instantiate them
	// for whatever reason.

	fs, err := procfs.NewDefaultFS()
	if err != nil {
		logger.Debug(
			"skipping registration",
			slog.String("reason", "failed to initialize proc FS"),
			slog.String("error", err.Error()),
		)

		return
	}

	info, err := fs.Self()
	if err != nil {
		logger.Debug(
			"skipping registration",
			slog.String("path", path.Join(procfs.DefaultMountPoint, "self")),
			slog.String("reason", "failed to initialize process info object for current process"),
			slog.String("error", err.Error()),
		)

		return
	}

	// Register Prometheus memory map metrics

	prometheus.MustRegister(prometheus.NewGaugeFunc(prometheus.GaugeOpts{
		Name: "proc_metrics_memory_map_max_limit",
		Help: "Upper limit on amount of memory mapped regions a process may have.",
	}, func() float64 {
		vm, err := fs.VM()
		if err != nil {
			logger.Debug(
				"failed to read virtual memory statistics for the current process",
				slog.String("path", path.Join(procfs.DefaultMountPoint, "sys", "vm")),
				slog.String("error", err.Error()),
			)

			return 0
		}

		if vm.MaxMapCount == nil {
			return 0
		}

		return float64(*vm.MaxMapCount)
	}))

	prometheus.MustRegister(prometheus.NewGaugeFunc(prometheus.GaugeOpts{
		Name: "proc_metrics_memory_map_current_count",
		Help: "Amount of memory mapped regions this process is currently using.",
	}, func() float64 {
		procMaps, err := info.ProcMaps()
		if err != nil {
			logger.Debug(
				"failed to read memory mappings for current process",
				slog.String("path", path.Join(procfs.DefaultMountPoint, "self", "maps")),
				slog.String("error", err.Error()),
			)

			return 0
		}

		return float64(len(procMaps))
	}))
}
