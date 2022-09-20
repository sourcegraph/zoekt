package tracer

import (
	"log"
	"os"
	"strconv"

	"github.com/opentracing/opentracing-go"
)

type tracerType string

const (
	tracerTypeNone          tracerType = "none"
	tracerTypeJaeger        tracerType = "jaeger"
	tracerTypeOpenTelemetry tracerType = "opentelemetry"
)

func inferTracerType() tracerType {
	// default to disabled
	isJaegerDisabled, err := strconv.ParseBool(os.Getenv("JAEGER_DISABLED"))
	if err == nil && !isJaegerDisabled {
		return tracerTypeJaeger
	}

	// defaults to disabled
	isOpenTelemetryDisabled, err := strconv.ParseBool(os.Getenv("OPENTELEMETRY_DISABLED"))
	if err == nil && !isOpenTelemetryDisabled {
		return tracerTypeOpenTelemetry
	}

	return tracerTypeNone
}

// Init should only be called from main and only once
// It will initialize the configured tracer, and register it as the global tracer
// This MUST be the same tracer as the one used by Sourcegraph
func Init(svcName, version string) {
	var (
		tt     = inferTracerType()
		tracer opentracing.Tracer
		err    error
	)
	switch tt {
	case tracerTypeJaeger:
		tracer, err = configureJaeger(svcName, version)
		if err != nil {
			log.Printf("failed to configure Jaeger tracer: %v", err)
			return
		}
		log.Printf("INFO: using Jaeger tracer")

	case tracerTypeOpenTelemetry:
		tracer, err = configureOpenTelemetry(svcName, version)
		if err != nil {
			log.Printf("failed to configure OpenTelemetry tracer: %v", err)
			return
		}
		log.Printf("INFO: using OpenTelemetry tracer")
	}

	if tracer != nil {
		opentracing.SetGlobalTracer(tracer)
	}
}
