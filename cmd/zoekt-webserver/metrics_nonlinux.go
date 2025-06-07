//go:build !linux

package main

import (
	sglog "github.com/sourcegraph/log"
)

func mustRegisterMemoryMapMetrics(logger sglog.Logger) {
	// The memory map metrics are collected via /proc, which
	// is only available on linux-based operating systems.

	// Other platforms do not have the same virtual memory statistics as Linux.
	// For example, Windows does not have the concept of
	// a count of memory maps, and a max number of memory maps
	return
}
