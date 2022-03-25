package profiler

import (
	"os"

	"cloud.google.com/go/profiler"
	ddprofiler "gopkg.in/DataDog/dd-trace-go.v1/profiler"
)

// Init starts the supported profilers IFF the environment variable is set.
func Init(svcName, version string, blockProfileRate int) error {
	if os.Getenv("DD_ENV") != "" {
		profileTypes := []ddprofiler.ProfileType{ddprofiler.CPUProfile, ddprofiler.HeapProfile}
		// additional profilers have a performance impact and should be enabled with care
		if os.Getenv("DD_PROFILE_ALL") != "" || blockProfileRate > 0 {
			profileTypes = append(profileTypes, ddprofiler.MutexProfile, ddprofiler.BlockProfile)
		}
		return ddprofiler.Start(
			ddprofiler.WithService(svcName),
			ddprofiler.WithVersion(version),
			ddprofiler.WithProfileTypes(profileTypes...,
			),
			ddprofiler.BlockProfileRate(blockProfileRate),
		)
	}

	if os.Getenv("GOOGLE_CLOUD_PROFILER_ENABLED") != "" {
		return profiler.Start(profiler.Config{
			Service:        svcName,
			ServiceVersion: version,
			MutexProfiling: true,
			AllocForceGC:   true,
		})
	}
	return nil
}
