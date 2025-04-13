//go:build !linux

package main

import "log/slog"

func mustRegisterMemoryMapMetrics(*slog.Logger) {
	// The memory map metrics are collected via /proc, which
	// is only available on linux-based operating systems.
}
