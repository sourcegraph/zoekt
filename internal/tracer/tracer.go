package tracer

import (
	"log"
	"os"
	"reflect"
	"strconv"

	"github.com/opentracing/opentracing-go"
	"github.com/uber/jaeger-client-go"
	jaegercfg "github.com/uber/jaeger-client-go/config"
	jaegermetrics "github.com/uber/jaeger-lib/metrics"
)

// Init should only be called from main and only once
// It will initialize the configured tracer, and register it as the global tracer
// This MUST be the same tracer as the one used by Sourcegraph
func Init(svcName, version string) {
	isJaegerDisabled, err := strconv.ParseBool(os.Getenv("JAEGER_DISABLED"))
	if err != nil {
		log.Printf("failed to parse JAEGER_DISABLED: %v", err)
		return
	}
	if isJaegerDisabled {
		return
	}

	tracer, err := configureJaeger(svcName, version)
	if err != nil {
		log.Printf("failed to configure Jaeger tracer: %v", err)
		return
	}
	log.Printf("INFO: using Jaeger tracer")
	opentracing.SetGlobalTracer(tracer)
}

func configureJaeger(svcName string, version string) (opentracing.Tracer, error) {
	cfg, err := jaegercfg.FromEnv()
	cfg.ServiceName = svcName
	if err != nil {
		return nil, err
	}
	cfg.Tags = append(cfg.Tags, opentracing.Tag{Key: "service.version", Value: version})
	if reflect.DeepEqual(cfg.Sampler, &jaegercfg.SamplerConfig{}) {
		// Default sampler configuration for when it is not specified via
		// JAEGER_SAMPLER_* env vars. In most cases, this is sufficient
		// enough to connect to Jaeger without any env vars.
		cfg.Sampler.Type = jaeger.SamplerTypeConst
		cfg.Sampler.Param = 1
	}
	tracer, _, err := cfg.NewTracer(
		jaegercfg.Logger(&jaegerLogger{}),
		jaegercfg.Metrics(jaegermetrics.NullFactory),
	)
	if err != nil {
		return nil, err
	}
	return tracer, nil
}

type jaegerLogger struct{}

func (l *jaegerLogger) Error(msg string) {
	log.Printf("ERROR: %s", msg)
}

// Infof logs a message at info priority
func (l *jaegerLogger) Infof(msg string, args ...interface{}) {
	log.Printf(msg, args...)
}
