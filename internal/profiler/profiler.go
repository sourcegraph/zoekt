package profiler

import (
	"log"
	"os"

	"cloud.google.com/go/profiler"
	ddprofiler "gopkg.in/DataDog/dd-trace-go.v1/profiler"
)

// Init starts the supported profilers IFF the environment variable is set.
func Init(svcName, version string, blockProfileRate int) {
	if os.Getenv("DD_ENV") != "" {
		profileTypes := []ddprofiler.ProfileType{ddprofiler.CPUProfile, ddprofiler.HeapProfile}
		// additional profilers have a performance impact and should be enabled with care
		if os.Getenv("DD_PROFILE_ALL") != "" || blockProfileRate > 0 {
			profileTypes = append(profileTypes, ddprofiler.MutexProfile, ddprofiler.BlockProfile)
		}
		err := ddprofiler.Start(
			ddprofiler.WithService(svcName),
			ddprofiler.WithVersion(version),
			ddprofiler.WithProfileTypes(profileTypes...,
			),
			ddprofiler.BlockProfileRate(blockProfileRate),
		)
		if err != nil {
			log.Printf("could not initialize profiler: %s", err.Error())
		}
		return
	}

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
