package logging

import (
	"bytes"
	"encoding/json"
	"log"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
)

// TestInit checks that output matches the JSON written by
// github.com/sourcegraph/log, which log pipelines depend on.
func TestInit(t *testing.T) {
	defaultLogger, logOut, logFlags := slog.Default(), log.Writer(), log.Flags()
	t.Cleanup(func() {
		slog.SetDefault(defaultLogger)
		log.SetOutput(logOut)
		log.SetFlags(logFlags)
	})

	resource := map[string]any{
		"service.name":        "zoekt-webserver",
		"service.version":     "v1",
		"service.instance.id": "host",
	}

	cases := []struct {
		name   string
		format string
		level  string
		want   []map[string]any
	}{{
		// Warn is the default level, but log.Printf output is never dropped.
		name: "json",
		want: []map[string]any{{
			"SeverityText": "WARN",
			"Body":         "warn",
			"Resource":     resource,
			"Attributes":   map[string]any{"repo": "foo", "duration": 1.5},
		}, {
			"SeverityText": "INFO",
			"Body":         "from log package 1",
			"Resource":     resource,
		}},
	}, {
		name:   "json_gcp",
		format: "json_gcp",
		level:  "info",
		want: []map[string]any{{
			"severity": "INFO",
			"message":  "info",
			"Resource": resource,
		}, {
			"severity":   "WARN",
			"message":    "warn",
			"Resource":   resource,
			"Attributes": map[string]any{"repo": "foo", "duration": 1.5},
		}, {
			"severity": "INFO",
			"message":  "from log package 1",
			"Resource": resource,
		}},
	}}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("SRC_LOG_FORMAT", tc.format)
			t.Setenv("SRC_LOG_LEVEL", tc.level)
			t.Setenv("SRC_DEVELOPMENT", "")

			var buf bytes.Buffer
			Init(&buf, Resource{Name: "zoekt-webserver", Version: "v1", InstanceID: "host"})
			slog.Info("info")
			slog.Warn("warn", "repo", "foo", "duration", 1500*time.Millisecond)
			log.Printf("from log package %d", 1)

			timeKey := "Timestamp"
			if tc.format == "json_gcp" {
				timeKey = "timestampNanos"
			}

			var got []map[string]any
			for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
				var m map[string]any
				if err := json.Unmarshal([]byte(line), &m); err != nil {
					t.Fatalf("invalid JSON %q: %v", line, err)
				}

				if ts, ok := m[timeKey].(float64); !ok || time.Since(time.Unix(0, int64(ts))).Abs() > time.Minute {
					t.Errorf("%s is not the current time in Unix nanoseconds: %v", timeKey, m[timeKey])
				}
				if caller, _ := m["Caller"].(string); !strings.HasPrefix(caller, "logging/logging_test.go:") {
					t.Errorf("Caller is not the logging call site: %v", m["Caller"])
				}
				if fn, _ := m["Function"].(string); !strings.HasPrefix(fn, "github.com/sourcegraph/zoekt/internal/logging.TestInit") {
					t.Errorf("Function is not the logging function: %v", m["Function"])
				}
				delete(m, timeKey)
				delete(m, "Caller")
				delete(m, "Function")
				got = append(got, m)
			}

			if d := cmp.Diff(tc.want, got); d != "" {
				t.Errorf("mismatch (-want +got):\n%s", d)
			}
		})
	}
}
