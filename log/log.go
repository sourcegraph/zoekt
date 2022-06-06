package log

import (
	"github.com/google/zoekt/log/internal/encoders"
	"github.com/google/zoekt/log/internal/logger"
	"github.com/google/zoekt/log/otfields"
	"go.uber.org/zap"
	"os"
)

const (
	envSrcDevelopment = "SRC_DEVELOPMENT"
	envSrcLogFormat   = "SRC_LOG_FORMAT"
	envSrcLogLevel    = "SRC_LOG_LEVEL"
)

type Resource = otfields.Resource

// Get retrieves the initialized logger
// panics for development mode
func Get() *zap.Logger {
	devMode := logger.DevMode()
	safeGet := !devMode
	return logger.Get(safeGet)
}

// Init initializes the logger package's logger of the given resource.
// It must be called on service startup, i.e. 'main()', NOT on an 'init()' function.
// Subsequent calls will panic, so do not call this within a non-service context.
//
// Init returns a callback, sync, that should be called before application exit.
//
// For testing, you can use 'logtest.Init' to initialize the logging library.
//
// If Init is not called, Get will panic.
// Returns the callback to sync the root core.
func Init(r Resource) (sync func() error) {
	level := zap.NewAtomicLevelAt(Level(os.Getenv(envSrcLogLevel)).Parse())
	format := encoders.ParseOutputFormat(os.Getenv(envSrcLogFormat))
	development := os.Getenv(envSrcDevelopment) == "true"

	return logger.Init(r, level, format, development)
}
