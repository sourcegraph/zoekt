package trace

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	log15 "gopkg.in/inconshreveable/log15.v2"

	"github.com/felixge/httpsnoop"
	raven "github.com/getsentry/raven-go"
	"github.com/gorilla/mux"
	opentracing "github.com/opentracing/opentracing-go"
	"github.com/opentracing/opentracing-go/ext"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/sourcegraph/sourcegraph/pkg/api"
	"github.com/sourcegraph/sourcegraph/pkg/conf"
	"github.com/sourcegraph/sourcegraph/pkg/repotrackutil"
	"github.com/sourcegraph/sourcegraph/pkg/version"
)

type key int

const (
	routeNameKey key = iota
	userKey      key = iota
)

var metricLabels = []string{"route", "method", "code", "repo"}
var requestDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
	Namespace: "src",
	Subsystem: "http",
	Name:      "request_duration_seconds",
	Help:      "The HTTP request latencies in seconds.",
	Buckets:   UserLatencyBuckets,
}, metricLabels)

var requestHeartbeat = prometheus.NewGaugeVec(prometheus.GaugeOpts{
	Namespace: "src",
	Subsystem: "http",
	Name:      "requests_last_timestamp_unixtime",
	Help:      "Last time a request finished for a http endpoint.",
}, metricLabels)

func init() {
	if err := raven.SetDSN(os.Getenv("SENTRY_DSN_BACKEND")); err != nil {
		log15.Error("sentry.dsn", "error", err)
	}

	raven.SetRelease(version.Version())
	raven.SetTagsContext(map[string]string{
		"service": filepath.Base(os.Args[0]),
	})

	prometheus.MustRegister(requestDuration)
	prometheus.MustRegister(requestHeartbeat)

	go func() {
		conf.Watch(func() {
			if conf.Get().Critical.Log == nil {
				return
			}

			if conf.Get().Critical.Log.Sentry == nil {
				return
			}

			// An empty dsn value is ignored: not an error.
			if err := raven.SetDSN(conf.Get().Critical.Log.Sentry.Dsn); err != nil {
				log15.Error("sentry.dsn", "error", err)
			}
		})
	}()
}

// Middleware captures and exports metrics to Prometheus, etc.
//
// 🚨 SECURITY: This handler is served to all clients, even on private servers to clients who have
// not authenticated. It must not reveal any sensitive information.
func Middleware(next http.Handler) http.Handler {
	return raven.Recoverer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		ctx := r.Context()

		wireContext, err := opentracing.GlobalTracer().Extract(
			opentracing.HTTPHeaders,
			opentracing.HTTPHeadersCarrier(r.Header))
		if err != nil && err != opentracing.ErrSpanContextNotFound {
			log15.Error("extracting parent span failed", "error", err)
		}

		// start new span
		span := opentracing.StartSpan("", ext.RPCServerOption(wireContext))
		ext.HTTPUrl.Set(span, r.URL.String())
		ext.HTTPMethod.Set(span, r.Method)
		span.SetTag("http.referer", r.Header.Get("referer"))
		defer span.Finish()
		rw.Header().Set("X-Trace", SpanURL(span))
		ctx = opentracing.ContextWithSpan(ctx, span)

		routeName := "unknown"
		ctx = context.WithValue(ctx, routeNameKey, &routeName)

		var userID int32
		ctx = context.WithValue(ctx, userKey, &userID)

		m := httpsnoop.CaptureMetrics(next, rw, r.WithContext(ctx))

		if routeName == "graphql" {
			// We use the query to denote the type of a GraphQL request, e.g. /.api/graphql?Repositories
			if r.URL.RawQuery != "" {
				routeName = "graphql: " + r.URL.RawQuery
			} else {
				routeName = "graphql: unknown"
			}
		}

		// route name is only known after the request has been handled
		span.SetOperationName("Serve: " + routeName)
		span.SetTag("Route", routeName)
		ext.HTTPStatusCode.Set(span, uint16(m.Code))

		labels := prometheus.Labels{
			"route":  routeName,
			"method": strings.ToLower(r.Method),
			"code":   strconv.Itoa(m.Code),
			"repo":   repotrackutil.GetTrackedRepo(api.RepoName(r.URL.Path)),
		}
		requestDuration.With(labels).Observe(m.Duration.Seconds())
		requestHeartbeat.With(labels).Set(float64(time.Now().Unix()))

		log15.Debug("TRACE HTTP",
			"method", r.Method,
			"url", r.URL.String(),
			"routename", routeName,
			"trace", SpanURL(span),
			"userAgent", r.UserAgent(),
			"user", userID,
			"xForwardedFor", r.Header.Get("X-Forwarded-For"),
			"written", m.Written,
			"code", m.Code,
			"duration", m.Duration,
		)

		// Notify sentry if the status code indicates our system had an error (e.g. 5xx).
		if m.Code >= 500 {
			raven.CaptureError(&httpErr{status: m.Code, method: r.Method, path: r.URL.Path}, map[string]string{
				"method":        r.Method,
				"url":           r.URL.String(),
				"routename":     routeName,
				"userAgent":     r.UserAgent(),
				"user":          fmt.Sprintf("%d", userID),
				"xForwardedFor": r.Header.Get("X-Forwarded-For"),
				"written":       fmt.Sprintf("%d", m.Written),
				"duration":      m.Duration.String(),
			})
		}
	}))
}

func TraceRoute(next http.Handler) http.Handler {
	return http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		if p, ok := r.Context().Value(routeNameKey).(*string); ok {
			if routeName := mux.CurrentRoute(r).GetName(); routeName != "" {
				*p = routeName
			}
		}
		next.ServeHTTP(rw, r)
	})
}

func TraceUser(ctx context.Context, userID int32) {
	if p, ok := ctx.Value(userKey).(*int32); ok {
		*p = userID
	}
}

// SetRouteName manually sets the name for the route. This should only be used
// for non-mux routed routes (ie middlewares).
func SetRouteName(r *http.Request, routeName string) {
	if p, ok := r.Context().Value(routeNameKey).(*string); ok {
		*p = routeName
	}
}

type httpErr struct {
	status int
	method string
	path   string
}

func (e *httpErr) Error() string {
	return fmt.Sprintf("HTTP status %d, %s %s", e.status, e.method, e.path)
}
