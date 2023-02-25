package main

import (
	"fmt"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// Example Usage:
//
// observer := NewRedFMetrics("operation_name", WithLabels("factorA", "factorB"))
//
// start := time.now()
// err := doOperation()
//
// operation.Observe(time.Since(start), err)
//
// m.Observe(duration duration, err error, "label0", "label1"...)

// RedFMetrics contains four common metrics for an operation.
// It's based on the RED method + some additional advice from
// Google SRE's "Monitoring Distributed Systems".
//
// See:
// - https://www.weave.works/blog/the-red-method-key-metrics-for-microservices-architecture/
// - https://sre.google/sre-book/monitoring-distributed-systems/
type RedFMetrics struct {
	Count    *prometheus.CounterVec   // How often did this operation run successfully?
	Duration *prometheus.HistogramVec // How long did this operation run for?

	ErrorCount    *prometheus.CounterVec   // How often did this operation fail?
	ErrorDuration *prometheus.HistogramVec // How long did the failures take?
}

func (m *RedFMetrics) Observe(d time.Duration, err error, lvals ...string) {
	if err != nil {
		m.ErrorCount.WithLabelValues(lvals...).Inc()
		m.ErrorDuration.WithLabelValues(lvals...).Observe(d.Seconds())
		return
	}

	m.Count.WithLabelValues(lvals...).Inc()
	m.ErrorDuration.WithLabelValues(lvals...).Observe(d.Seconds())
}

type redfMetricOptions struct {
	countHelp    string
	durationHelp string

	errorsCountHelp    string
	errorsDurationHelp string

	labels          []string
	durationBuckets []float64
}

// RedfMetricsOption alter the default behavior of NewRedfMetrics.
type RedfMetricsOption func(o *redfMetricOptions)

// WithDurationHelp overrides the default help text for duration metrics.
func WithDurationHelp(text string) RedfMetricsOption {
	return func(o *redfMetricOptions) { o.durationHelp = text }
}

// WithCountHelp overrides the default help text for count metrics.
func WithCountHelp(text string) RedfMetricsOption {
	return func(o *redfMetricOptions) { o.countHelp = text }
}

// WithErrorsCountHelp overrides the default help text for error count metrics.
func WithErrorsCountHelp(text string) RedfMetricsOption {
	return func(o *redfMetricOptions) { o.errorsCountHelp = text }
}

// WithErrorsCountHelp overrides the default help text for error duration metrics.
func WithErrorsDurationHelp(text string) RedfMetricsOption {
	return func(o *redfMetricOptions) { o.errorsDurationHelp = text }
}

// WithLabels overrides the default labels for all metrics.
func WithLabels(labels ...string) RedfMetricsOption {
	return func(o *redfMetricOptions) { o.labels = labels }
}

// WithDurationBuckets overrides the default histogram bucket values for duration metrics.
func WithDurationBuckets(buckets []float64) RedfMetricsOption {
	return func(o *redfMetricOptions) {
		if len(buckets) != 0 {
			o.durationBuckets = buckets
		}
	}
}

func NewRedfMetrics(name string, overrides ...RedfMetricsOption) *RedFMetrics {
	options := &redfMetricOptions{
		countHelp:          fmt.Sprintf("Number of successful %s operations", name),
		durationHelp:       fmt.Sprintf("Time in seconds spent performing %s operations", name),
		errorsCountHelp:    fmt.Sprintf("Number of failed %s operations", name),
		errorsDurationHelp: fmt.Sprintf("Time in seconds spent performing failed %s operations", name),

		labels:          nil,
		durationBuckets: prometheus.DefBuckets,
	}

	for _, override := range overrides {
		override(options)
	}

	count := prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: fmt.Sprintf("%s_total", name),
			Help: options.countHelp,
		},
		options.labels,
	)

	duration := prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name: fmt.Sprintf("%s_duration", name),
			Help: options.countHelp,
		},
		options.labels,
	)

	errorsCount := prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: fmt.Sprintf("%s_errors_total", name),
			Help: options.errorsCountHelp,
		},
		options.labels,
	)

	errorsDuration := prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name: fmt.Sprintf("%s_errors_duration", name),
			Help: options.errorsDurationHelp,
		},
		options.labels,
	)

	return &RedFMetrics{
		Count:    count,
		Duration: duration,

		ErrorCount:    errorsCount,
		ErrorDuration: errorsDuration,
	}
}
