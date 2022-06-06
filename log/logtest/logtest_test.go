package logtest

import (
	"go.uber.org/zap"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestExport(t *testing.T) {
	logger, exportLogs := Captured(t)
	assert.NotNil(t, logger)

	logger.Info("hello world", zap.String("key", "value"))

	logs := exportLogs()
	assert.Len(t, logs, 1)
	assert.Equal(t, "hello world", logs[0].Message)

	// In dev mode, attributes are not added
	assert.Equal(t, map[string]any{"key": "value"}, logs[0].Fields)
}
