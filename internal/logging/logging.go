// Package logging configures log/slog for zoekt's long-running services. The
// JSON output matches github.com/sourcegraph/log, which those services used
// before, so existing log pipelines keep working.
package logging

import (
	"io"
	"log"
	"log/slog"
	"math"
	"os"
	"path"
	"strconv"
	"strings"
)

// Resource describes the service that is logging. It is included in every
// JSON log line as the OpenTelemetry Resource.
type Resource struct {
	Name       string
	Version    string
	InstanceID string
}

// Init sets slog's default logger to write to w. Like github.com/sourcegraph/log
// it is configured by the environment:
//
//   - SRC_LOG_LEVEL is one of debug, info, warn (the default), error or none.
//   - SRC_LOG_FORMAT is json (the default), json_gcp or condensed.
//   - SRC_DEVELOPMENT=true selects the condensed format.
//
// Output of the standard library log package is also written to w, as INFO
// records regardless of SRC_LOG_LEVEL, since it has never been filtered.
//
// Init can be called again to write to a different w.
func Init(w io.Writer, r Resource) {
	format := os.Getenv("SRC_LOG_FORMAT")
	if os.Getenv("SRC_DEVELOPMENT") == "true" {
		format = "condensed"
	}

	slog.SetDefault(slog.New(newHandler(w, r, format, parseLevel(os.Getenv("SRC_LOG_LEVEL")))))

	// slog.SetDefault sends the log package through the default handler, which
	// would drop all of its output below SRC_LOG_LEVEL. Replace that bridge with
	// one that always logs. It still records the caller of log.Printf etc.
	log.SetOutput(slog.NewLogLogger(newHandler(w, r, format, slog.LevelInfo), slog.LevelInfo).Writer())
}

func newHandler(w io.Writer, r Resource, format string, level slog.Level) slog.Handler {
	if format == "condensed" || format == "console" {
		return slog.NewTextHandler(w, &slog.HandlerOptions{
			AddSource: true,
			Level:     level,
			ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
				if src, ok := a.Value.Any().(*slog.Source); ok && len(groups) == 0 && a.Key == slog.SourceKey {
					return slog.String(slog.SourceKey, shortCaller(src))
				}
				return a
			},
		})
	}

	timeKey, levelKey, messageKey := "Timestamp", "SeverityText", "Body"
	if format == "json_gcp" {
		timeKey, levelKey, messageKey = "timestampNanos", "severity", "message"
	}

	h := slog.NewJSONHandler(w, &slog.HandlerOptions{
		AddSource: true,
		Level:     level,
		ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
			if len(groups) == 0 {
				switch a.Key {
				case slog.TimeKey:
					return slog.Int64(timeKey, a.Value.Time().UnixNano())
				case slog.LevelKey:
					return slog.Attr{Key: levelKey, Value: a.Value}
				case slog.MessageKey:
					return slog.Attr{Key: messageKey, Value: a.Value}
				case slog.SourceKey:
					if src, ok := a.Value.Any().(*slog.Source); ok {
						// A group without a key is inlined, giving two top-level fields.
						return slog.Group("", slog.String("Caller", shortCaller(src)), slog.String("Function", src.Function))
					}
				}
			}
			if a.Value.Kind() == slog.KindDuration {
				return slog.Float64(a.Key, a.Value.Duration().Seconds())
			}
			return a
		},
	})

	name := r.Name
	if name == "" {
		name = "unknown_service"
	}
	resource := []any{slog.String("service.name", name)}
	if r.Version != "" {
		resource = append(resource, slog.String("service.version", r.Version))
	}
	if r.InstanceID != "" {
		resource = append(resource, slog.String("service.instance.id", r.InstanceID))
	}

	return h.WithAttrs([]slog.Attr{slog.Group("Resource", resource...)}).WithGroup("Attributes")
}

// parseLevel parses SRC_LOG_LEVEL the same way github.com/sourcegraph/log does.
func parseLevel(s string) slog.Level {
	switch strings.ToLower(s) {
	case "debug", "dbug":
		return slog.LevelDebug
	case "info":
		return slog.LevelInfo
	case "error", "eror", "crit":
		return slog.LevelError
	case "none":
		return slog.Level(math.MaxInt)
	}
	return slog.LevelWarn
}

// shortCaller returns the file's directory and name with the line, e.g.
// "search/shards.go:42".
func shortCaller(src *slog.Source) string {
	dir, file := path.Split(src.File)
	return path.Join(path.Base(dir), file) + ":" + strconv.Itoa(src.Line)
}
