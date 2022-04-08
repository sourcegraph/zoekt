// This file contains commands which run in a non daemon mode for testing/debugging.

package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"time"

	"github.com/peterbourgon/ff/v3/ffcli"

	"github.com/google/zoekt/build"
)

func debugIndex() *ffcli.Command {
	fs := flag.NewFlagSet("debug index", flag.ExitOnError)
	conf := rootConfig{}
	conf.registerRootFlags(fs)

	return &ffcli.Command{
		Name:       "index",
		ShortUsage: "index [flags] <repository ID>",
		ShortHelp:  "index a repository",
		FlagSet:    fs,
		Exec: func(ctx context.Context, args []string) error {
			if len(args) == 0 {
				return fmt.Errorf("missing repository ID")
			}
			s, err := newServer(conf)
			if err != nil {
				return err
			}
			id, err := strconv.Atoi(args[0])
			if err != nil {
				return err
			}
			msg, err := s.forceIndex(uint32(id))
			log.Println(msg)
			if err != nil {
				return err
			}
			return nil
		},
	}
}

func debugTrigrams() *ffcli.Command {
	return &ffcli.Command{
		Name:       "trigrams",
		ShortUsage: "trigrams <path/to/shard>",
		ShortHelp:  "list all the trigrams in a shard",
		Exec: func(ctx context.Context, args []string) error {
			if len(args) == 0 {
				return fmt.Errorf("missing path to shard")
			}
			return printShardStats(args[0])
		},
	}
}

func debugMeta() *ffcli.Command {
	return &ffcli.Command{
		Name:       "meta",
		ShortUsage: "meta <path/to/shard>",
		ShortHelp:  "output index and repo metadata",
		Exec: func(ctx context.Context, args []string) error {
			if len(args) == 0 {
				return fmt.Errorf("missing path to shard")
			}
			return printMetaData(args[0])
		},
	}
}

func debugMerge() *ffcli.Command {
	fs := flag.NewFlagSet("debug merge", flag.ExitOnError)
	simulate := fs.Bool("simulate", false, "if set, merging will be simulated")
	targetSize := fs.Int64("merge_target_size", getEnvWithDefaultInt64("SRC_TARGET_SIZE", 2000), "the target size of compound shards in MiB")
	index := fs.String("index", getEnvWithDefaultString("DATA_DIR", build.DefaultDir), "set index directory to use")
	dbg := fs.Bool("debug", srcLogLevelIsDebug(), "turn on more verbose logging.")

	return &ffcli.Command{
		Name:       "merge",
		FlagSet:    fs,
		ShortUsage: "merge [flags] <dir>",
		ShortHelp:  "run a full merge operation inside dir",
		Exec: func(ctx context.Context, args []string) error {
			if *dbg {
				debug = log.New(os.Stderr, "", log.LstdFlags)
			}
			return doMerge(*index, *targetSize*1024*1024, *simulate)
		},
	}
}

func debugList() *ffcli.Command {
	fs := flag.NewFlagSet("debug list", flag.ExitOnError)
	conf := rootConfig{}
	conf.registerRootFlags(fs)

	return &ffcli.Command{
		Name:       "list",
		ShortUsage: "list [flags]",
		ShortHelp:  "list the repositories that are OWNED by this indexserver",
		FlagSet:    fs,
		Exec: func(ctx context.Context, args []string) error {
			s, err := newServer(conf)
			if err != nil {
				return err
			}
			repos, err := s.Sourcegraph.List(context.Background(), listIndexed(s.IndexDir))
			if err != nil {
				return err
			}
			for _, r := range repos.IDs {
				fmt.Println(r)
			}
			return nil
		},
	}
}

func debugListIndexed() *ffcli.Command {
	fs := flag.NewFlagSet("debug list-indexed", flag.ExitOnError)
	conf := rootConfig{}
	conf.registerRootFlags(fs)

	return &ffcli.Command{
		Name:       "list-indexed",
		ShortUsage: "list-indexed [flags]",
		ShortHelp:  "list the repositories that are INDEXED by this indexserver",
		FlagSet:    fs,
		Exec: func(ctx context.Context, args []string) error {
			s, err := newServer(conf)
			if err != nil {
				return err
			}
			indexed := listIndexed(s.IndexDir)
			for _, r := range indexed {
				fmt.Println(r)
			}
			return nil
		},
	}
}

func debugQueue() *ffcli.Command {
	fs := flag.NewFlagSet("debug queue", flag.ExitOnError)

	printHeader := fs.Bool("header", true, "print column headers")

	connectionTimeout := fs.Duration("timeout", 10*time.Second, "max timeout for establishing a connection to the zoekt-sourcegraph-indexserver instance")

	hostname := fs.String("hostname", "localhost", "the hostname of the zoekt-sourcegraph-indexserver instance to connect to")
	port := fs.Uint("port", 6072, "the port of the zoekt-sourcegraph-indexserver instance to connect to")

	return &ffcli.Command{
		Name:       "queue",
		ShortUsage: "queue [flags]",
		ShortHelp:  "list the repositories in the indexing queue, sorted by descending priority",
		FlagSet:    fs,
		Exec: func(ctx context.Context, args []string) error {
			ctx, cancel := context.WithTimeout(ctx, *connectionTimeout)
			defer cancel()

			raw := fmt.Sprintf("http://%s:%d/debug/queue", *hostname, *port)
			address, err := url.Parse(raw)
			if err != nil {
				return fmt.Errorf("parsing URL %q: %s", raw, err)
			}
			address.Query().Add("header", strconv.FormatBool(*printHeader))

			request, err := http.NewRequestWithContext(ctx, http.MethodGet, address.String(), nil)
			if err != nil {
				return fmt.Errorf("constructing request: %w", err)
			}

			request.Header.Set("Accept", "text/plain")
			response, err := http.DefaultClient.Do(request)
			if err != nil {
				return err
			}

			defer response.Body.Close()

			_, err = io.Copy(os.Stdout, response.Body)
			if err != nil {
				return fmt.Errorf("writing to stdout: %w", err)
			}

			return nil
		},
	}
}

func debugCmd() *ffcli.Command {
	fs := flag.NewFlagSet("debug", flag.ExitOnError)

	return &ffcli.Command{
		Name:       "debug",
		ShortUsage: "debug <subcommand>",
		ShortHelp:  "a set of commands for debugging and testing",
		FlagSet:    fs,
		Subcommands: []*ffcli.Command{
			debugIndex(),
			debugList(),
			debugListIndexed(),
			debugMerge(),
			debugMeta(),
			debugTrigrams(),
			debugQueue(),
		},
	}
}
