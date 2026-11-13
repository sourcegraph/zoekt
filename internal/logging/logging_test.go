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
			"Attributes":   map[string]any{"repo": "foo", "duration": 1.5, "until": 42.0},
		}, {
			"SeverityText":         "WARN",
			"Body":                 "scoped",
			"InstrumentationScope": "server.sub",
			"TraceId":              "trace",
			"SpanId":               "span",
			"Resource":             resource,
			"Attributes":           map[string]any{"repo": "foo", "opts": map[string]any{"n": 1.0}},
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
			"Attributes": map[string]any{"repo": "foo", "duration": 1.5, "until": 42.0},
		}, {
			"severity":             "WARN",
			"message":              "scoped",
			"InstrumentationScope": "server.sub",
			"TraceId":              "trace",
			"SpanId":               "span",
			"Resource":             resource,
			"Attributes":           map[string]any{"repo": "foo", "opts": map[string]any{"n": 1.0}},
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
			slog.Warn("warn", "repo", "foo", "duration", 1500*time.Millisecond, "until", time.Unix(0, 42))
			l := WithTrace(Scoped(Scoped(slog.Default(), "server"), "sub"), "trace", "span")
			l.With("repo", "foo").WithGroup("opts").Warn("scoped", "n", 1)
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

func TestSampler(t *testing.T) {
	s := newSampler(2, 3)
	now := time.Unix(100, 0)

	var got []bool
	for range 8 {
		got = append(got, s.allow(now, slog.LevelWarn, "shed"))
	}
	// The first 2 are logged, then every 3rd after them.
	want := []bool{true, true, false, false, true, false, false, true}
	if d := cmp.Diff(want, got); d != "" {
		t.Errorf("mismatch (-want +got):\n%s", d)
	}

	if !s.allow(now, slog.LevelError, "shed") || !s.allow(now, slog.LevelWarn, "other") {
		t.Error("a different level or message was sampled with another's count")
	}
	if !s.allow(now.Add(time.Second), slog.LevelWarn, "shed") {
		t.Error("the count was not reset in the next second")
	}

	if newSampler(0, 100) != nil || newSampler(-1, 100) != nil {
		t.Error("an initial count of 0 or less does not disable sampling")
	}
}

func TestInitSampling(t *testing.T) {
	defaultLogger, logOut, logFlags := slog.Default(), log.Writer(), log.Flags()
	t.Cleanup(func() {
		slog.SetDefault(defaultLogger)
		log.SetOutput(logOut)
		log.SetFlags(logFlags)
	})
	t.Setenv("SRC_LOG_SAMPLING_INITIAL", "1")
	t.Setenv("SRC_LOG_SAMPLING_THEREAFTER", "0")

	var buf bytes.Buffer
	Init(&buf, Resource{})
	for range 3 {
		slog.Warn("shed")
		log.Print("from log package")
	}

	// Sampling applies to slog records, but never to the log package.
	if got := strings.Count(buf.String(), `"Body":"shed"`); got != 1 {
		t.Errorf("got %d sampled records, want 1", got)
	}
	if got := strings.Count(buf.String(), `"Body":"from log package"`); got != 3 {
		t.Errorf("got %d log package records, want 3", got)
	}
}
