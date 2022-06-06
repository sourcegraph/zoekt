package log_test

import (
	"go.uber.org/zap"
	"testing"

	"github.com/stretchr/testify/assert"

	sglogger "github.com/google/zoekt/log/internal/logger"
	"github.com/google/zoekt/log/logtest"
	"github.com/google/zoekt/log/otfields"
)

func TestLogger(t *testing.T) {
	logger, exportLogs := logtest.Captured(t)
	assert.NotNil(t, logger)

	// If in devmode, the attributes namespace does not get added, but we want to test
	// that behaviour here so we add it back.
	if sglogger.DevMode() {
		logger = logger.With(otfields.AttributesNamespace)
	}

	logger.Debug("a debug message") // 0

	logger = logger.With(zap.String("some", "field"))

	logger.Info("hello world", zap.String("hello", "world")) // 1

	logger.Info("goodbye", zap.String("world", "hello")) // 2
	logger.Warn("another message")                       // 3

	logs := exportLogs()
	assert.Len(t, logs, 4)
	for _, l := range logs {
		assert.NotEmpty(t, l.Message)
	}

	assert.Equal(t, "a debug message", logs[0].Message)
	assert.Empty(t, logs[0].Fields["Attributes"])

	assert.Equal(t, "hello world", logs[1].Message)
	assert.Equal(t, map[string]any{
		"some":  "field",
		"hello": "world",
	}, logs[1].Fields["Attributes"])

	assert.Equal(t, "goodbye", logs[2].Message)
	assert.Equal(t, map[string]any{
		"some":  "field",
		"world": "hello",
	}, logs[2].Fields["Attributes"])

	assert.Equal(t, "another message", logs[3].Message)
	// only field added by With()
	assert.Equal(t, map[string]any{
		"some": "field",
	}, logs[3].Fields["Attributes"])
}
