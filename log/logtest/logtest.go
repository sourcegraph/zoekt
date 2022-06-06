package logtest

import (
	"flag"
	"github.com/google/zoekt/log"
	"github.com/google/zoekt/log/internal/encoders"
	"github.com/google/zoekt/log/internal/logger"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"

	"testing"
	"time"

	"go.uber.org/zap/zapcore"
)

// Init can be used to instantiate the log package for running tests, to be called in
// TestMain for the relevant package. Remember to call (*testing.M).Run() after initializing
// the logger!
//
// testing.M is an unused argument, used to indicate this function should be called in
// TestMain.
func Init(_ *testing.M) {
	// ensure Verbose is set up
	testing.Init()
	flag.Parse()
	// set reasonable defaults
	if testing.Verbose() {
		initLogger(zapcore.DebugLevel)
	} else {
		initLogger(zapcore.WarnLevel)
	}
}

// InitWithLevel does the same thing as Init, but uses the provided log level to configur
// the log level for this package's tests, which can be helpful for exceptionally noisy
// tests.
//
// If your loggers are parameterized, you can also use logtest.NoOp to silence output for
// specific tests.
func InitWithLevel(_ *testing.M, level log.Level) {
	initLogger(level.Parse())
}

func initLogger(level zapcore.Level) {
	// use empty resource development skips including it
	logger.Init(log.Resource{}, level, encoders.OutputConsole, true)
}

type CapturedLog struct {
	Time    time.Time
	Scope   string
	Level   log.Level
	Message string
	Fields  map[string]any
}

type LoggerOptions struct {
	// Level configures the minimum log level to output.
	Level log.Level
	// FailOnErrorLogs indicates that the test should fail if an error log is output.
	FailOnErrorLogs bool
}

func testLoggerOutput(t testing.TB, options LoggerOptions) *zap.Logger {
	// initialize just in case - the underlying call to log.Init is no-op if this has
	// already been done. We allow this in testing for convenience.
	Init(nil)

	initialLogger := logger.Get(true)

	// On cleanup, flush the global logger.
	t.Cleanup(func() { initialLogger.Sync() })

	// Hook test output with cloned logger
	return initialLogger.
		WithOptions(zap.WrapCore(func(c zapcore.Core) zapcore.Core {
			var level zapcore.LevelEnabler = c // by default, use the parent core's leveller
			if options.Level != "" {
				level = zap.NewAtomicLevelAt(options.Level.Parse())
			}

			return newTestingCore(t, level, options.FailOnErrorLogs) // replace the core entirely
		}))
}

// Captured retrieves a logger from scoped to the the given test, and returns a callback,
// dumpLogs, which flushes the logger buffer and returns log entries.
func Captured(t testing.TB) (newLogger *zap.Logger, exportLogs func() []CapturedLog) {
	testLogger := testLoggerOutput(t, LoggerOptions{})

	observerCore, entries := observer.New(zap.DebugLevel) // capture all levels
	newLogger = testLogger.
		WithOptions(zap.WrapCore(func(c zapcore.Core) zapcore.Core {
			return zapcore.NewTee(observerCore, c)
		}))

	return newLogger, func() []CapturedLog {
		entries := entries.TakeAll()
		logs := make([]CapturedLog, len(entries))
		for i, e := range entries {
			logs[i] = CapturedLog{
				Time:    e.Time,
				Scope:   e.LoggerName,
				Level:   log.Level(e.Level.String()),
				Message: e.Message,
				Fields:  e.ContextMap(),
			}
		}
		return logs
	}
}
