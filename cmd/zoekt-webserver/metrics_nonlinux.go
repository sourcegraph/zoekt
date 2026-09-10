//go:build !linux

package main

import sglog "github.com/sourcegraph/log"

func mustRegisterMemoryMapMetrics(sglog.Logger) {
	// The memory map metrics are collected via /proc, which
	// is only available on linux-based operating systems.
}
