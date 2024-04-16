package webserver

import (
	"flag"
	"time"

	"github.com/sourcegraph/zoekt/build"
)

type Options struct {
	LogDir                 string
	LogRefresh             time.Duration
	Listen                 string
	Index                  string
	HTML                   bool
	Search                 bool
	EnableRPC              bool
	EnableIndexserverProxy bool
	Print                  bool
	EnablePprof            bool
	SslCert                string
	SslKey                 string
	HostCustomization      string
	TemplateDir            string
	DumpTemplates          bool
	Version                bool
}

func ParseFlags() Options {
	logDir := flag.String("log_dir", "", "log to this directory rather than stderr.")
	logRefresh := flag.Duration("log_refresh", 24*time.Hour, "if using --log_dir, start writing a new file this often.")

	listen := flag.String("listen", ":6070", "listen on this address.")
	index := flag.String("index", build.DefaultDir, "set index directory to use")
	html := flag.Bool("html", true, "enable HTML interface")
	enableRPC := flag.Bool("rpc", false, "enable go/net RPC")
	enableIndexserverProxy := flag.Bool("indexserver_proxy", false, "proxy requests with URLs matching the path /indexserver/ to <index>/indexserver.sock")
	print := flag.Bool("print", false, "enable local result URLs")
	enablePprof := flag.Bool("pprof", false, "set to enable remote profiling.")
	sslCert := flag.String("ssl_cert", "", "set path to SSL .pem holding certificate.")
	sslKey := flag.String("ssl_key", "", "set path to SSL .pem holding key.")
	hostCustomization := flag.String(
		"host_customization", "",
		"specify host customization, as HOST1=QUERY,HOST2=QUERY")

	templateDir := flag.String("template_dir", "", "set directory from which to load custom .html.tpl template files")
	dumpTemplates := flag.Bool("dump_templates", false, "dump templates into --template_dir and exit.")
	version := flag.Bool("version", false, "Print version number")

	flag.Parse()

	return Options{
		LogDir:                 *logDir,
		LogRefresh:             *logRefresh,
		Listen:                 *listen,
		Index:                  *index,
		HTML:                   *html,
		Search:                 *html,
		EnableRPC:              *enableRPC,
		EnableIndexserverProxy: *enableIndexserverProxy,
		Print:                  *print,
		EnablePprof:            *enablePprof,
		SslCert:                *sslCert,
		SslKey:                 *sslKey,
		HostCustomization:      *hostCustomization,
		TemplateDir:            *templateDir,
		DumpTemplates:          *dumpTemplates,
		Version:                *version,
	}
}
