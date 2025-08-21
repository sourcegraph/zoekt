module github.com/sourcegraph/zoekt

require (
	cloud.google.com/go/profiler v0.4.2
	github.com/AdaLogics/go-fuzz-headers v0.0.0-20230811130428-ced1acdcaa24
	github.com/RoaringBitmap/roaring v1.9.4
	github.com/andygrunwald/go-gerrit v1.0.0
	github.com/bmatcuk/doublestar v1.3.4
	github.com/dustin/go-humanize v1.0.1
	github.com/felixge/fgprof v0.9.5
	github.com/fsnotify/fsnotify v1.8.0
	github.com/gfleury/go-bitbucket-v1 v0.0.0-20240917142304-df385efaac68
	github.com/go-enry/go-enry/v2 v2.9.1
	github.com/go-git/go-git/v5 v5.13.1
	github.com/gobwas/glob v0.2.3
	github.com/google/go-cmp v0.6.0
	github.com/google/go-github/v27 v27.0.6
	github.com/google/slothfs v0.0.0-20190717100203-59c1163fd173
	github.com/grafana/regexp v0.0.0-20240607082908-2cb410fa05da
	github.com/grpc-ecosystem/go-grpc-middleware v1.4.0
	github.com/grpc-ecosystem/go-grpc-middleware/providers/prometheus v1.0.1
	github.com/grpc-ecosystem/go-grpc-middleware/v2 v2.2.0
	github.com/mxk/go-flowrate v0.0.0-20140419014527-cca7078d478f
	github.com/opentracing/opentracing-go v1.2.0
	github.com/peterbourgon/ff/v3 v3.4.0
	github.com/pkg/errors v0.9.1
	github.com/prometheus/client_golang v1.20.5
	github.com/prometheus/procfs v0.15.1
	github.com/rs/xid v1.6.0
	github.com/shirou/gopsutil/v3 v3.24.5
	github.com/sourcegraph/go-ctags v0.0.0-20250729094530-349a251d78d8
	github.com/sourcegraph/log v0.0.0-20241024013702-574f7079c888
	github.com/sourcegraph/mountinfo v0.0.0-20240201124957-b314c0befab1
	github.com/stretchr/testify v1.10.0
	github.com/uber/jaeger-client-go v2.30.0+incompatible
	github.com/uber/jaeger-lib v2.4.1+incompatible
	github.com/xeipuuv/gojsonschema v1.2.0
	gitlab.com/gitlab-org/api/client-go v0.129.0
	go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc v0.58.0
	go.opentelemetry.io/contrib/propagators/jaeger v1.33.0
	go.opentelemetry.io/contrib/propagators/ot v1.33.0
	go.opentelemetry.io/otel v1.33.0
	go.opentelemetry.io/otel/bridge/opentracing v1.33.0
	go.opentelemetry.io/otel/exporters/otlp/otlptrace v1.33.0
	go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc v1.33.0
	go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp v1.33.0
	go.opentelemetry.io/otel/sdk v1.33.0
	go.opentelemetry.io/otel/trace v1.33.0
	go.uber.org/atomic v1.11.0
	go.uber.org/automaxprocs v1.6.0
	golang.org/x/net v0.34.0
	golang.org/x/oauth2 v0.30.0
	golang.org/x/sync v0.11.0
	golang.org/x/sys v0.30.0
	google.golang.org/grpc v1.69.4
	google.golang.org/protobuf v1.36.3
	pgregory.net/rapid v1.2.0
)

require (
	github.com/42wim/httpsig v1.2.2 // indirect
	github.com/davidmz/go-pageant v1.0.2 // indirect
	github.com/go-fed/httpsig v1.1.0 // indirect
	github.com/hashicorp/go-version v1.7.0 // indirect
	github.com/kylelemons/godebug v1.1.0 // indirect
	go.opentelemetry.io/auto/sdk v1.1.0 // indirect
)

require (
	cloud.google.com/go v0.118.0 // indirect
	cloud.google.com/go/auth v0.14.0 // indirect
	cloud.google.com/go/auth/oauth2adapt v0.2.7 // indirect
	cloud.google.com/go/compute/metadata v0.6.0 // indirect
	code.gitea.io/sdk/gitea v0.20.0
	dario.cat/mergo v1.0.1 // indirect
	github.com/HdrHistogram/hdrhistogram-go v1.1.2 // indirect
	github.com/Microsoft/go-winio v0.6.2 // indirect
	github.com/ProtonMail/go-crypto v1.1.5 // indirect
	github.com/beorn7/perks v1.0.1 // indirect
	github.com/bits-and-blooms/bitset v1.20.0 // indirect
	github.com/cenkalti/backoff/v4 v4.3.0 // indirect
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/cloudflare/circl v1.5.0 // indirect
	github.com/cockroachdb/errors v1.11.3 // indirect
	github.com/cockroachdb/logtags v0.0.0-20241215232642-bb51bb14a506 // indirect
	github.com/cockroachdb/redact v1.1.5 // indirect
	github.com/cyphar/filepath-securejoin v0.4.0 // indirect
	github.com/davecgh/go-spew v1.1.1 // indirect
	github.com/emirpasic/gods v1.18.1 // indirect
	github.com/fatih/color v1.18.0 // indirect
	github.com/getsentry/sentry-go v0.31.1 // indirect
	github.com/go-enry/go-oniguruma v1.2.1 // indirect
	github.com/go-git/gcfg v1.5.1-0.20230307220236-3a3c6141e376 // indirect
	github.com/go-git/go-billy/v5 v5.6.2
	github.com/go-logr/logr v1.4.2 // indirect
	github.com/go-logr/stdr v1.2.2 // indirect
	github.com/go-ole/go-ole v1.3.0 // indirect
	github.com/gogo/protobuf v1.3.2 // indirect
	github.com/golang/groupcache v0.0.0-20241129210726-2c02b8208cf8 // indirect
	github.com/google/go-querystring v1.1.0 // indirect
	github.com/google/pprof v0.0.0-20241210010833-40e02aabc2ad // indirect
	github.com/google/s2a-go v0.1.9 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/googleapis/enterprise-certificate-proxy v0.3.4 // indirect
	github.com/googleapis/gax-go/v2 v2.14.1 // indirect
	github.com/grpc-ecosystem/grpc-gateway/v2 v2.25.1 // indirect
	github.com/hashicorp/go-cleanhttp v0.5.2 // indirect
	github.com/hashicorp/go-retryablehttp v0.7.7 // indirect
	github.com/jbenet/go-context v0.0.0-20150711004518-d14ea06fba99 // indirect
	github.com/kevinburke/ssh_config v1.2.0 // indirect
	github.com/klauspost/compress v1.17.11 // indirect
	github.com/kr/pretty v0.3.1 // indirect
	github.com/kr/text v0.2.0 // indirect
	github.com/mattn/go-colorable v0.1.14 // indirect
	github.com/mattn/go-isatty v0.0.20 // indirect
	github.com/mitchellh/mapstructure v1.5.0 // indirect
	github.com/moby/sys/mountinfo v0.7.2 // indirect
	github.com/mschoch/smat v0.2.0 // indirect
	github.com/munnerz/goautoneg v0.0.0-20191010083416-a7dc8b61c822 // indirect
	github.com/pjbgf/sha1cd v0.3.2 // indirect
	github.com/pmezard/go-difflib v1.0.0 // indirect
	github.com/power-devops/perfstat v0.0.0-20240221224432-82ca36839d55 // indirect
	github.com/prometheus/client_model v0.6.1 // indirect
	github.com/prometheus/common v0.62.0 // indirect
	github.com/rogpeppe/go-internal v1.13.1 // indirect
	github.com/sergi/go-diff v1.3.2-0.20230802210424-5b0b94c5c0d3 // indirect
	github.com/skeema/knownhosts v1.3.0 // indirect
	github.com/xanzy/ssh-agent v0.3.3 // indirect
	github.com/xeipuuv/gojsonpointer v0.0.0-20190905194746-02993c407bfb // indirect
	github.com/xeipuuv/gojsonreference v0.0.0-20180127040603-bd5ef7bd5415 // indirect
	github.com/yusufpapurcu/wmi v1.2.4 // indirect
	go.opentelemetry.io/otel/metric v1.33.0 // indirect
	go.opentelemetry.io/proto/otlp v1.5.0 // indirect
	go.uber.org/multierr v1.11.0
	go.uber.org/zap v1.27.0 // indirect
	golang.org/x/crypto v0.35.0 // indirect
	golang.org/x/text v0.22.0 // indirect
	golang.org/x/time v0.11.0 // indirect
	golang.org/x/tools v0.29.0 // indirect
	google.golang.org/api v0.217.0 // indirect
	google.golang.org/genproto v0.0.0-20250115164207-1a7da9e5054f // indirect
	google.golang.org/genproto/googleapis/api v0.0.0-20250115164207-1a7da9e5054f // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20250115164207-1a7da9e5054f // indirect
	gopkg.in/warnings.v0 v0.1.2 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)

go 1.23.4
