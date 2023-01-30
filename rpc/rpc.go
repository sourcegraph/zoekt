// Package rpc provides a zoekt.Searcher over RPC.
package rpc

import (
	"context"
	"encoding/gob"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"time"

	"github.com/keegancsmith/rpc"
	"github.com/sourcegraph/zoekt"
	"github.com/sourcegraph/zoekt/query"
	"github.com/sourcegraph/zoekt/rpc/internal/srv"
)

// DefaultRPCPath is the rpc path used by zoekt-webserver
const DefaultRPCPath = "/rpc"

// Server returns an http.Handler for searcher which is the server side of the
// RPC calls.
func Server(searcher zoekt.Searcher) http.Handler {
	RegisterGob()
	server := rpc.NewServer()
	if err := server.Register(&srv.Searcher{Searcher: searcher}); err != nil {
		// this should never fail, so we panic.
		panic("unexpected error registering rpc server: " + err.Error())
	}
	return server
}

// Client connects to a Searcher HTTP RPC server at address (host:port) using
// DefaultRPCPath path.
func Client(address string) zoekt.Searcher {
	return ClientAtPath(address, DefaultRPCPath)
}

// ClientAtPath connects to a Searcher HTTP RPC server at address and path
// (http://host:port/path).
func ClientAtPath(address, path string) zoekt.Searcher {
	RegisterGob()
	return &client{addr: address, path: path}
}

type client struct {
	addr, path string

	mu  sync.Mutex // protects client and gen
	cl  *rpc.Client
	gen int // incremented each time we dial
}

func (c *client) Search(ctx context.Context, q query.Q, opts *zoekt.SearchOptions) (*zoekt.SearchResult, error) {
	var reply srv.SearchReply
	err := c.call(ctx, "Searcher.Search", &srv.SearchArgs{Q: q, Opts: opts}, &reply)
	return reply.Result, err
}

func (c *client) List(ctx context.Context, q query.Q, opts *zoekt.ListOptions) (*zoekt.RepoList, error) {
	var reply srv.ListReply
	err := c.call(ctx, "Searcher.List", &srv.ListArgs{Q: q, Opts: opts}, &reply)
	return reply.List, err
}

func (c *client) call(ctx context.Context, serviceMethod string, args interface{}, reply interface{}) error {
	// We try twice. If we fail to dial or fail to call the function we try
	// again after 100ms. Unrolled to make logic clear
	cl, gen, err := c.getRPCClient(ctx, 0)
	if err == nil {
		err = cl.Call(ctx, serviceMethod, args, reply)
		if err != rpc.ErrShutdown {
			return err
		}
	}

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(100 * time.Millisecond):
	}

	cl, _, err = c.getRPCClient(ctx, gen)
	if err != nil {
		return err
	}
	return cl.Call(ctx, serviceMethod, args, reply)
}

// getRPCClient gets the rpc client. If gen matches the current generation, we
// redial and increment the generation. This is used to prevent concurrent
// redialing on network failure.
func (c *client) getRPCClient(ctx context.Context, gen int) (*rpc.Client, int, error) {
	// coarse lock so we only dial once
	c.mu.Lock()
	defer c.mu.Unlock()
	if gen != c.gen {
		return c.cl, c.gen, nil
	}
	var timeout time.Duration
	if deadline, ok := ctx.Deadline(); ok {
		timeout = time.Until(deadline)
	}
	cl, err := rpc.DialHTTPPathTimeout("tcp", c.addr, c.path, timeout)
	if err != nil {
		return nil, c.gen, err
	}
	c.cl = cl
	c.gen++
	return c.cl, c.gen, nil
}

func (c *client) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.cl != nil {
		c.cl.Close()
	}
}

func (c *client) String() string {
	return fmt.Sprintf("rpcSearcher(%s/%s)", c.addr, c.path)
}

var once sync.Once

// RegisterGob registers various query types with gob. It can be called more than
// once, because calls to gob.Register are protected by a sync.Once.
func RegisterGob() {
	once.Do(func() {
		gobRegister(&query.And{})
		gobRegister(&query.BranchRepos{})
		gobRegister(&query.BranchesRepos{})
		gobRegister(&query.Branch{})
		gobRegister(&query.Const{})
		gobRegister(&query.FileNameSet{})
		gobRegister(&query.GobCache{})
		gobRegister(&query.Language{})
		gobRegister(&query.Not{})
		gobRegister(&query.Or{})
		gobRegister(&query.Regexp{})
		gobRegister(&query.RepoRegexp{})
		gobRegister(&query.RepoSet{})
		gobRegister(&query.RepoIDs{})
		gobRegister(&query.Repo{})
		gobRegister(&query.Substring{})
		gobRegister(&query.Symbol{})
		gobRegister(&query.Type{})
		gobRegister(query.RawConfig(41))
	})
}

// gobRegister exists to keep backwards compatibility around renames of the go
// module. This is to avoid breaking the wire protocol due to refactors. In
// particular in August 2022 we renamed the go module from
// github.com/google/zoekt to github.com/sourcegraph/zoekt which breaks the
// wire protocol. So this function will replace those names so we keep using
// google/zoekt.
func gobRegister(value any) {
	name := gobRegister_name(value)

	name = strings.Replace(name, "github.com/sourcegraph/", "github.com/google/", 1)

	gob.RegisterName(name, value)
}

// gobRegister_name is copy-pasta from the stdlib gob.Register, returning the
// name it picks for gob.RegisterName.
func gobRegister_name(value any) string {
	// Default to printed representation for unnamed types
	rt := reflect.TypeOf(value)
	name := rt.String()

	// But for named types (or pointers to them), qualify with import path (but see inner comment).
	// Dereference one pointer looking for a named type.
	star := ""
	if rt.Name() == "" {
		if pt := rt; pt.Kind() == reflect.Pointer {
			star = "*"
			// NOTE: The following line should be rt = pt.Elem() to implement
			// what the comment above claims, but fixing it would break compatibility
			// with existing gobs.
			//
			// Given package p imported as "full/p" with these definitions:
			//     package p
			//     type T1 struct { ... }
			// this table shows the intended and actual strings used by gob to
			// name the types:
			//
			// Type      Correct string     Actual string
			//
			// T1        full/p.T1          full/p.T1
			// *T1       *full/p.T1         *p.T1
			//
			// The missing full path cannot be fixed without breaking existing gob decoders.
			rt = pt
		}
	}
	if rt.Name() != "" {
		if rt.PkgPath() == "" {
			name = star + rt.Name()
		} else {
			name = star + rt.PkgPath() + "." + rt.Name()
		}
	}

	return name
}
