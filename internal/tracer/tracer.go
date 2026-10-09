package tracer

import (
	"log"
	"os"
	"strconv"

	sglog "github.com/sourcegraph/log"
	"go.opentelemetry.io/otel"
)

// Init initializes OpenTelemetry and registers its global tracer provider. It
// should only be called from main and only once. Zoekt and Sourcegraph must use
// compatible OpenTelemetry configuration so trace context propagates between
// them.
func Init(resource sglog.Resource) {
	isDisabled, err := strconv.ParseBool(os.Getenv("OPENTELEMETRY_DISABLED"))
	if err != nil || isDisabled {
		return
	}

	provider, err := configureOpenTelemetry(resource)
	if err != nil {
		log.Printf("failed to configure OpenTelemetry tracer: %v", err)
		return
	}

	otel.SetTracerProvider(provider)
	log.Printf("INFO: using OpenTelemetry tracer")
}
