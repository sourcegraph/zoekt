package profiler

import (
	"log"
	"os"

	"cloud.google.com/go/profiler"
)

// Init starts the supported profilers IFF the environment variable is set.
func Init(svcName, version string) {
	if os.Getenv("GOOGLE_CLOUD_PROFILER_ENABLED") != "" {
		err := profiler.Start(profiler.Config{
			Service:        svcName,
			ServiceVersion: version,
			MutexProfiling: true,
			AllocForceGC:   true,
		})
		if err != nil {
			log.Printf("could not initialize profiler: %s", err.Error())
		}
	}
}

// InitLightweight starts the supported profilers IFF the environment variable is set.
// Compared to Init, it disables mutex profiling and forced GC to reduce its overhead.
func InitLightweight(svcName, version string) {
	if os.Getenv("GOOGLE_CLOUD_PROFILER_ENABLED") != "" {
		err := profiler.Start(profiler.Config{
			Service:        svcName,
			ServiceVersion: version,
		})
		if err != nil {
			log.Printf("could not initialize profiler: %s", err.Error())
		}
	}
}
