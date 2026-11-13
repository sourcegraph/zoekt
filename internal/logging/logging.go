// Package logging configures log/slog for zoekt's long-running services. The
// JSON output matches github.com/sourcegraph/log, which those services used
// before, so existing log pipelines keep working.
package logging

import (
	"context"
	"io"
	"log"
	"log/slog"
	"math"
	"os"
	"path"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
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
//   - SRC_LOG_SAMPLING_INITIAL and SRC_LOG_SAMPLING_THEREAFTER (both 100 by
//     default) sample repeated messages: each second, the first INITIAL
//     records with the same level and message are logged, then every
//     THEREAFTER-th. INITIAL of 0 or less disables sampling.
//
// Output of the standard library log package is also written to w, as INFO
// records regardless of SRC_LOG_LEVEL and without sampling, since it has never
// been filtered.
//
// Init can be called again to write to a different w.
func Init(w io.Writer, r Resource) {
	format := os.Getenv("SRC_LOG_FORMAT")
	if os.Getenv("SRC_DEVELOPMENT") == "true" {
		format = "condensed"
	}

	h := newHandler(w, r, format, parseLevel(os.Getenv("SRC_LOG_LEVEL")))
	h.sampler = newSampler(envInt("SRC_LOG_SAMPLING_INITIAL", 100), envInt("SRC_LOG_SAMPLING_THEREAFTER", 100))
	slog.SetDefault(slog.New(h))

	// slog.SetDefault sends the log package through the default handler, which
	// would drop all of its output below SRC_LOG_LEVEL. Replace that bridge with
	// one that always logs. It still records the caller of log.Printf etc.
	log.SetOutput(slog.NewLogLogger(newHandler(w, r, format, slog.LevelInfo), slog.LevelInfo).Writer())
}

// Scoped returns a logger whose records have the given InstrumentationScope.
// Scoping a scoped logger joins the names with a dot, like
// github.com/sourcegraph/log.
func Scoped(l *slog.Logger, name string) *slog.Logger {
	h, ok := l.Handler().(*handler)
	if !ok {
		return l.With("scope", name)
	}
	h2 := *h
	if h2.scope != "" {
		name = h2.scope + "." + name
	}
	h2.scope = name
	return slog.New(&h2)
}

// WithTrace returns a logger whose records have the given TraceId and SpanId.
func WithTrace(l *slog.Logger, traceID, spanID string) *slog.Logger {
	h, ok := l.Handler().(*handler)
	if !ok {
		return l.With("TraceId", traceID, "SpanId", spanID)
	}
	h2 := *h
	h2.traceID, h2.spanID = traceID, spanID
	return slog.New(&h2)
}

// handler adds the top-level fields of github.com/sourcegraph/log's format to
// the records it passes to inner, nests their attributes under group, and
// samples them.
type handler struct {
	inner   slog.Handler
	group   string
	sampler *sampler

	scope, traceID, spanID string

	// goas holds the arguments of WithAttrs and WithGroup calls in order.
	goas []groupOrAttrs
}

type groupOrAttrs struct {
	group string
	attrs []slog.Attr
}

func (h *handler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.inner.Enabled(ctx, level)
}

func (h *handler) Handle(ctx context.Context, r slog.Record) error {
	if h.sampler != nil && !h.sampler.allow(r.Time, r.Level, r.Message) {
		return nil
	}

	attrs := make([]slog.Attr, 0, r.NumAttrs())
	r.Attrs(func(a slog.Attr) bool {
		attrs = append(attrs, a)
		return true
	})
	for i := len(h.goas) - 1; i >= 0; i-- {
		if g := h.goas[i].group; g != "" {
			if len(attrs) > 0 {
				attrs = []slog.Attr{{Key: g, Value: slog.GroupValue(attrs...)}}
			}
		} else {
			attrs = append(slices.Clone(h.goas[i].attrs), attrs...)
		}
	}

	var top []slog.Attr
	if h.scope != "" {
		top = append(top, slog.String("InstrumentationScope", h.scope))
	}
	if h.traceID != "" {
		top = append(top, slog.String("TraceId", h.traceID), slog.String("SpanId", h.spanID))
	}
	if h.group != "" && len(attrs) > 0 {
		top = append(top, slog.Attr{Key: h.group, Value: slog.GroupValue(attrs...)})
	} else {
		top = append(top, attrs...)
	}

	nr := slog.NewRecord(r.Time, r.Level, r.Message, r.PC)
	nr.AddAttrs(top...)
	return h.inner.Handle(ctx, nr)
}

func (h *handler) WithAttrs(attrs []slog.Attr) slog.Handler {
	if len(attrs) == 0 {
		return h
	}
	return h.with(groupOrAttrs{attrs: attrs})
}

func (h *handler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}
	return h.with(groupOrAttrs{group: name})
}

func (h *handler) with(goa groupOrAttrs) *handler {
	h2 := *h
	h2.goas = append(slices.Clip(h.goas), goa)
	return &h2
}

// sampler limits how often records with the same level and message are
// logged within each second, like zap's sampler used by
// github.com/sourcegraph/log.
type sampler struct {
	initial, thereafter int

	mu     sync.Mutex
	second int64
	counts map[samplerKey]int
}

type samplerKey struct {
	level slog.Level
	msg   string
}

func newSampler(initial, thereafter int) *sampler {
	if initial <= 0 {
		return nil
	}
	return &sampler{initial: initial, thereafter: thereafter, counts: map[samplerKey]int{}}
}

func (s *sampler) allow(t time.Time, level slog.Level, msg string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	if sec := t.Unix(); sec != s.second {
		s.second = sec
		clear(s.counts)
	}
	k := samplerKey{level, msg}
	s.counts[k]++
	n := s.counts[k]
	return n <= s.initial || (s.thereafter > 0 && (n-s.initial)%s.thereafter == 0)
}

func newHandler(w io.Writer, r Resource, format string, level slog.Level) *handler {
	if format == "condensed" || format == "console" {
		return &handler{inner: slog.NewTextHandler(w, &slog.HandlerOptions{
			AddSource: true,
			Level:     level,
			ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
				if src, ok := a.Value.Any().(*slog.Source); ok && len(groups) == 0 && a.Key == slog.SourceKey {
					return slog.String(slog.SourceKey, shortCaller(src))
				}
				return a
			},
		})}
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
			switch a.Value.Kind() {
			case slog.KindDuration:
				return slog.Float64(a.Key, a.Value.Duration().Seconds())
			case slog.KindTime:
				return slog.Int64(a.Key, a.Value.Time().UnixNano())
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

	return &handler{
		inner: h.WithAttrs([]slog.Attr{slog.Group("Resource", resource...)}),
		group: "Attributes",
	}
}

func envInt(key string, def int) int {
	if n, err := strconv.Atoi(os.Getenv(key)); err == nil {
		return n
	}
	return def
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
