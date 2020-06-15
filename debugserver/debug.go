package debugserver

import (
	"net/http"
	"net/http/pprof"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"golang.org/x/net/trace"
)

func AddHandlers(mux *http.ServeMux, enablePprof bool) {
	trace.AuthRequest = func(req *http.Request) (any, sensitive bool) {
		return true, true
	}

	index := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`
				<a href="vars">Vars</a><br>
				<a href="debug/pprof/">PProf</a><br>
				<a href="metrics">Metrics</a><br>
				<a href="debug/requests">Requests</a><br>
				<a href="debug/events">Events</a><br>
			`))
		_, _ = w.Write([]byte(`
				<br>
				<form method="post" action="gc" style="display: inline;"><input type="submit" value="GC"></form>
				<form method="post" action="freeosmemory" style="display: inline;"><input type="submit" value="Free OS Memory"></form>
			`))
	})
	mux.Handle("/debug", index)
	mux.Handle("/vars", http.HandlerFunc(expvarHandler))
	mux.Handle("/gc", http.HandlerFunc(gcHandler))
	mux.Handle("/freeosmemory", http.HandlerFunc(freeOSMemoryHandler))
	if enablePprof {
		mux.Handle("/debug/pprof/", http.HandlerFunc(pprof.Index))
		mux.Handle("/debug/pprof/cmdline", http.HandlerFunc(pprof.Cmdline))
		mux.Handle("/debug/pprof/profile", http.HandlerFunc(pprof.Profile))
		mux.Handle("/debug/pprof/symbol", http.HandlerFunc(pprof.Symbol))
		mux.Handle("/debug/pprof/trace", http.HandlerFunc(pprof.Trace))
	}
	mux.Handle("/debug/requests", http.HandlerFunc(trace.Traces))
	mux.Handle("/debug/events", http.HandlerFunc(trace.Events))
	mux.Handle("/metrics", promhttp.Handler())
}
