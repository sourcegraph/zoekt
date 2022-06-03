package log

import (
	"github.com/google/zoekt/log/encoders"
	"os"
	"sync"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	"github.com/google/uuid"

	"github.com/google/zoekt/log/otfields"
)

var (
	devMode          bool
	globalLogger     *zap.Logger
	globalLoggerInit sync.Once
)

const (
	envSrcDevelopment = "SRC_DEVELOPMENT"
	envSrcLogFormat   = "SRC_LOG_FORMAT"
	envSrcLogLevel    = "SRC_LOG_LEVEL"
)

func DevMode() bool { return devMode }

// Get retrieves the initialized global logger, or panics otherwise (unless safe is true,
// in which case a no-op logger is returned)
func Get(safe bool) *zap.Logger {
	return globalLogger
}

type Resource = otfields.Resource

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
	if IsInitialized() {
		panic("log.Init initialized multiple times")
	}

	level := zap.NewAtomicLevelAt(Level(os.Getenv(envSrcLogLevel)).Parse())
	format := encoders.ParseOutputFormat(os.Getenv(envSrcLogFormat))
	development := os.Getenv(envSrcDevelopment) == "true"

	globalLoggerInit.Do(func() {
		globalLogger = initLogger(r, level, format, development)
	})
	return globalLogger.Sync
}

// IsInitialized indicates if the global logger is initialized.
func IsInitialized() bool {
	return globalLogger != nil
}

func initLogger(r otfields.Resource, level zapcore.LevelEnabler, format encoders2.OutputFormat, development bool) *zap.Logger {
	// Set global
	devMode = development

	logSink, errSink, err := openStderrSinks()
	if err != nil {
		panic(err.Error())
	}

	options := []zap.Option{zap.ErrorOutput(errSink), zap.AddCaller()}
	if development {
		options = append(options, zap.Development())
	}

	logger := zap.New(zapcore.NewCore(
		encoders.BuildEncoder(format, development),
		logSink,
		level,
	), options...)

	if development {
		return logger
	}

	// If not in development, log OpenTelemetry Resource field and generate an InstanceID
	// to uniquely identify this resource.
	//
	// See examples: https://opentelemetry.io/docs/reference/specification/logs/data-model/#example-log-records
	if r.InstanceID == "" {
		r.InstanceID = uuid.New().String()
	}
	return logger.With(zap.Object("Resource", &encoders.ResourceEncoder{Resource: r}))
}

// copied from https://sourcegraph.com/github.com/uber-go/zap/-/blob/config.go?L249
func openStderrSinks() (zapcore.WriteSyncer, zapcore.WriteSyncer, error) {
	sink, closeOut, err := zap.Open("stderr")
	if err != nil {
		return nil, nil, err
	}
	errSink, _, err := zap.Open("stderr")
	if err != nil {
		closeOut()
		return nil, nil, err
	}
	return sink, errSink, nil
}
