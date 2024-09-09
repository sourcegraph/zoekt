package profiler

import (
	"log"
	"os"

	"cloud.google.com/go/profiler"
	"github.com/sourcegraph/zoekt"
)

// Init starts the supported profilers IFF the environment variable is set.
func Init(svcName string) {
	if os.Getenv("GOOGLE_CLOUD_PROFILER_ENABLED") != "" {
		err := profiler.Start(profiler.Config{
			Service:        svcName,
			ServiceVersion: zoekt.Version,
			MutexProfiling: true,
			AllocForceGC:   true,
		})
		if err != nil {
			log.Printf("could not initialize profiler: %s", err.Error())
		}
	}
}
