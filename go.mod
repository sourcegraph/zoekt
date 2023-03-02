module github.com/sourcegraph/zoekt

require (
	cloud.google.com/go/profiler v0.3.1
	github.com/RoaringBitmap/roaring v1.2.3
	github.com/andygrunwald/go-gerrit v0.0.0-20230211083816-04e01d7217b2
	github.com/bmatcuk/doublestar v1.3.4
	github.com/edsrzf/mmap-go v1.1.0
	github.com/fsnotify/fsnotify v1.6.0
	github.com/gfleury/go-bitbucket-v1 v0.0.0-20220418082332-711d7d5e805f
	github.com/go-enry/go-enry/v2 v2.8.3
	github.com/go-git/go-git/v5 v5.5.2
	github.com/gobwas/glob v0.2.3
	github.com/google/go-cmp v0.5.9
	github.com/google/go-github/v27 v27.0.6
	github.com/google/slothfs v0.0.0-20190717100203-59c1163fd173
	github.com/grafana/regexp v0.0.0-20221123153739-15dc172cd2db
	github.com/hashicorp/go-retryablehttp v0.7.2
	github.com/keegancsmith/rpc v1.3.0
	github.com/keegancsmith/tmpfriend v0.0.0-20180423180255-86e88902a513
	github.com/kylelemons/godebug v1.1.0
	github.com/mxk/go-flowrate v0.0.0-20140419014527-cca7078d478f
	github.com/opentracing/opentracing-go v1.2.0
	github.com/peterbourgon/ff/v3 v3.3.0
	github.com/pkg/errors v0.9.1
	github.com/prometheus/client_golang v1.14.0
	github.com/prometheus/procfs v0.9.0
	github.com/rs/xid v1.4.0
	github.com/shirou/gopsutil/v3 v3.23.1
	github.com/sourcegraph/go-ctags v0.0.0-20230111110657-c27675da7f71
	github.com/sourcegraph/log v0.0.0-20230203201409-49ac5ad3f2ce
	github.com/sourcegraph/mountinfo v0.0.0-20230106004439-7026e28cef67
	github.com/sourcegraph/sourcegraph/protos/frontend/indexedsearch v0.0.0-20230227225858-8dfb96f33e55
	github.com/uber/jaeger-client-go v2.30.0+incompatible
	github.com/uber/jaeger-lib v2.4.1+incompatible
	github.com/xanzy/go-gitlab v0.80.0
	go.opentelemetry.io/contrib/propagators/jaeger v1.14.0
	go.opentelemetry.io/contrib/propagators/ot v1.14.0
	go.opentelemetry.io/otel v1.13.0
	go.opentelemetry.io/otel/bridge/opentracing v1.13.0
	go.opentelemetry.io/otel/exporters/otlp/otlptrace v1.13.0
	go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc v1.13.0
	go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp v1.13.0
	go.opentelemetry.io/otel/sdk v1.13.0
	go.opentelemetry.io/otel/trace v1.13.0
	go.uber.org/atomic v1.10.0
	go.uber.org/automaxprocs v1.5.1
	golang.org/x/net v0.7.0
	golang.org/x/oauth2 v0.5.0
	golang.org/x/sync v0.1.0
	golang.org/x/sys v0.5.0
	google.golang.org/grpc v1.53.0
	gopkg.in/natefinch/lumberjack.v2 v2.2.1
)

require (
	cloud.google.com/go v0.109.0 // indirect
	cloud.google.com/go/compute v1.18.0 // indirect
	cloud.google.com/go/compute/metadata v0.2.3 // indirect
	github.com/HdrHistogram/hdrhistogram-go v1.1.2 // indirect
	github.com/Microsoft/go-winio v0.6.0 // indirect
	github.com/ProtonMail/go-crypto v0.0.0-20230214155104-81033d7f4442 // indirect
	github.com/acomagu/bufpipe v1.0.3 // indirect
	github.com/beorn7/perks v1.0.1 // indirect
	github.com/bits-and-blooms/bitset v1.5.0 // indirect
	github.com/cenkalti/backoff/v4 v4.2.0 // indirect
	github.com/cespare/xxhash/v2 v2.2.0 // indirect
	github.com/cloudflare/circl v1.3.2 // indirect
	github.com/cockroachdb/errors v1.9.1 // indirect
	github.com/cockroachdb/logtags v0.0.0-20230118201751-21c54148d20b // indirect
	github.com/cockroachdb/redact v1.1.3 // indirect
	github.com/emirpasic/gods v1.18.1 // indirect
	github.com/fatih/color v1.14.1 // indirect
	github.com/getsentry/sentry-go v0.18.0 // indirect
	github.com/go-enry/go-oniguruma v1.2.1 // indirect
	github.com/go-git/gcfg v1.5.0 // indirect
	github.com/go-git/go-billy/v5 v5.4.1 // indirect
	github.com/go-logr/logr v1.2.3 // indirect
	github.com/go-logr/stdr v1.2.2 // indirect
	github.com/go-ole/go-ole v1.2.6 // indirect
	github.com/gogo/protobuf v1.3.2 // indirect
	github.com/golang/groupcache v0.0.0-20210331224755-41bb18bfe9da // indirect
	github.com/golang/protobuf v1.5.2 // indirect
	github.com/google/go-querystring v1.1.0 // indirect
	github.com/google/pprof v0.0.0-20230207041349-798e818bf904 // indirect
	github.com/google/uuid v1.3.0 // indirect
	github.com/googleapis/enterprise-certificate-proxy v0.2.3 // indirect
	github.com/googleapis/gax-go/v2 v2.7.0 // indirect
	github.com/grpc-ecosystem/grpc-gateway/v2 v2.15.0 // indirect
	github.com/hashicorp/go-cleanhttp v0.5.2 // indirect
	github.com/hashicorp/go-hclog v0.16.2 // indirect
	github.com/imdario/mergo v0.3.13 // indirect
	github.com/jbenet/go-context v0.0.0-20150711004518-d14ea06fba99 // indirect
	github.com/kevinburke/ssh_config v1.2.0 // indirect
	github.com/kr/pretty v0.3.1 // indirect
	github.com/kr/text v0.2.0 // indirect
	github.com/mattn/go-colorable v0.1.13 // indirect
	github.com/mattn/go-isatty v0.0.17 // indirect
	github.com/matttproud/golang_protobuf_extensions v1.0.4 // indirect
	github.com/mitchellh/mapstructure v1.5.0 // indirect
	github.com/moby/sys/mountinfo v0.6.2 // indirect
	github.com/mschoch/smat v0.2.0 // indirect
	github.com/pjbgf/sha1cd v0.2.3 // indirect
	github.com/power-devops/perfstat v0.0.0-20221212215047-62379fc7944b // indirect
	github.com/prometheus/client_model v0.3.0 // indirect
	github.com/prometheus/common v0.39.0 // indirect
	github.com/rogpeppe/go-internal v1.9.0 // indirect
	github.com/sergi/go-diff v1.3.1 // indirect
	github.com/skeema/knownhosts v1.1.0 // indirect
	github.com/xanzy/ssh-agent v0.3.3 // indirect
	github.com/yusufpapurcu/wmi v1.2.2 // indirect
	go.opencensus.io v0.24.0 // indirect
	go.opentelemetry.io/otel/exporters/otlp/internal/retry v1.13.0 // indirect
	go.opentelemetry.io/proto/otlp v0.19.0 // indirect
	go.uber.org/multierr v1.9.0 // indirect
	go.uber.org/zap v1.24.0 // indirect
	golang.org/x/crypto v0.6.0 // indirect
	golang.org/x/mod v0.8.0 // indirect
	golang.org/x/text v0.7.0 // indirect
	golang.org/x/time v0.3.0 // indirect
	golang.org/x/tools v0.6.0 // indirect
	google.golang.org/api v0.110.0 // indirect
	google.golang.org/appengine v1.6.7 // indirect
	google.golang.org/genproto v0.0.0-20230209215440-0dfe4f8abfcc // indirect
	google.golang.org/protobuf v1.28.1 // indirect
	gopkg.in/warnings.v0 v0.1.2 // indirect
)

go 1.18
