package log_test

import (
	"os"
	"testing"

	"github.com/google/zoekt/log/logtest"
)

func TestMain(m *testing.M) {
	logtest.Init(m)
	os.Exit(m.Run())
}
