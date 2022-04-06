// This file contains commands which run in a non daemon mode for testing/debugging.

package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"text/tabwriter"
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
	var connectionTimeout time.Duration
	var printHeader bool

	var serverAddress string

	fs := flag.NewFlagSet("debug queue", flag.ExitOnError)
	fs.DurationVar(&connectionTimeout, "timeout", 10*time.Second, "max timeout for establishing a connection to a zoekt-sourcegraph-indexserver instance")
	fs.BoolVar(&printHeader, "header", false, "whether to print the headers for each column")

	fs.StringVar(&serverAddress, "address", "http://localhost:6072", "the address of the zoekt-sourcegraph-indexserver instance to connect to")

	return &ffcli.Command{
		Name:       "queue",
		ShortUsage: "queue [flags]",
		ShortHelp:  "list the repositories in the indexing queue, sorted by descending priority",
		FlagSet:    fs,
		Exec: func(ctx context.Context, args []string) error {

			fullAddress := fmt.Sprintf("%s/debug/queue", serverAddress)
			request, err := http.NewRequest(http.MethodGet, fullAddress, nil)
			if err != nil {
				return fmt.Errorf("constructing request: %w", err)
			}

			request.Header.Set("Accept", "application/x-gob-stream")
			request.Header.Set("Cache-Control", "no-cache")
			request.Header.Set("Connection", "keep-alive")
			request.Header.Set("Transfer-Encoding", "chunked")

			ctx, cancel := context.WithTimeout(ctx, connectionTimeout)
			defer cancel()

			request = request.WithContext(ctx)
			response, err := http.DefaultClient.Do(request)
			if err != nil {
				return fmt.Errorf("connecting to %q: %w", fullAddress, err)
			}

			defer response.Body.Close()

			if response.StatusCode != http.StatusOK {
				b, err := ioutil.ReadAll(io.LimitReader(response.Body, 1024))
				if err != nil {
					return fmt.Errorf("reading response body from failed request: %w", err)
				}

				return &url.Error{
					Op:  "Get",
					URL: fullAddress,
					Err: fmt.Errorf("%s: %s", response.Status, string(b)),
				}
			}

			writer := tabwriter.NewWriter(os.Stdout, 0, 8, 4, ' ', 0)
			defer writer.Flush()

			if printHeader {
				_, err := fmt.Fprintf(writer, "Position\tName\tID\tBranches\n")
				if err != nil {
					return fmt.Errorf("writing headers to output: %w", err)
				}
			}

			decoder := newQueueItemStreamDecoder(response.Body)
			position := 0
			for decoder.Next() {
				item := decoder.Item()

				var branches []string
				for _, b := range item.Opts.Branches {
					branches = append(branches, b.String())
				}

				_, err := fmt.Fprintf(writer, "%d\t%s\t%d\t%s\n", position, item.Opts.Name, item.RepoID, strings.Join(branches, ", "))
				if err != nil {
					return fmt.Errorf("writing entry to stdout: %w", err)
				}

				position++
			}

			err = decoder.Err()
			if err != nil {
				return fmt.Errorf("decoding item stream: %w", err)
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
