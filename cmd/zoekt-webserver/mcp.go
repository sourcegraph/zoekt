package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/lestrrat-go/jwx/v2/jwk"
	"github.com/lestrrat-go/jwx/v2/jwt"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	sglog "github.com/sourcegraph/log"

	"github.com/sourcegraph/zoekt"
	"github.com/sourcegraph/zoekt/index"
	"github.com/sourcegraph/zoekt/query"
)

const (
	mcpPath       = "/mcp"
	wellKnownPath = "/.well-known/oauth-authorization-server"
)

type mcpContextKey string

const tokenSubjectKey mcpContextKey = "token_subject"

var proxyClient = &http.Client{Timeout: 5 * time.Second}

func subjectFromContext(ctx context.Context) (string, bool) {
	sub, ok := ctx.Value(tokenSubjectKey).(string)
	return sub, ok && sub != ""
}

// addMCPHandlers registers the MCP server and OAuth discovery routes on mux.
func addMCPHandlers(mux *http.ServeMux, searcher zoekt.Streamer) {
	logger := sglog.Scoped("mcp")

	oktaBaseURL := os.Getenv("ZOEKT_OKTA_BASE_URL")
	if oktaBaseURL == "" {
		logger.Warn("ZOEKT_OKTA_BASE_URL not set, MCP routes will return 503")
		unavailable := "MCP not configured: ZOEKT_OKTA_BASE_URL not set"
		mux.HandleFunc(mcpPath, func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, unavailable, http.StatusServiceUnavailable)
		})
		mux.HandleFunc(wellKnownPath, func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, unavailable, http.StatusServiceUnavailable)
		})
		return
	}

	verifier, err := newJWTVerifier(context.Background(), oktaBaseURL, logger)
	if err != nil {
		logger.Error("failed to initialize JWT verifier", sglog.Error(err))
		errMsg := fmt.Sprintf("MCP unavailable: JWT verifier init failed: %v", err)
		mux.HandleFunc(mcpPath, func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, errMsg, http.StatusServiceUnavailable)
		})
		mux.HandleFunc(wellKnownPath, makeWellKnownHandler(oktaBaseURL, logger))
		return
	}

	mcpServer := buildMCPServer(searcher, logger)
	httpServer := server.NewStreamableHTTPServer(mcpServer)

	mux.Handle(mcpPath, jwtAuthMiddleware(verifier, logger, httpServer))
	mux.HandleFunc(wellKnownPath, makeWellKnownHandler(oktaBaseURL, logger))
}

// tokenVerifier is an interface for JWT verification, allowing test doubles.
type tokenVerifier interface {
	verify(authHeader string) (string, error)
}

// jwtAuthMiddleware validates the Bearer token and injects the subject into the request context.
func jwtAuthMiddleware(v tokenVerifier, logger sglog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sub, err := v.verify(r.Header.Get("Authorization"))
		if err != nil {
			if isJWKSError(err) {
				logger.Error("JWKS verification infrastructure failure",
					sglog.Error(err),
					sglog.String("remote_addr", r.RemoteAddr),
				)
			}
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("WWW-Authenticate", `Bearer error="invalid_token"`)
			w.WriteHeader(http.StatusUnauthorized)
			if encErr := json.NewEncoder(w).Encode(map[string]string{
				"error":             "invalid_token",
				"error_description": "Authentication required",
			}); encErr != nil {
				logger.Warn("failed to write 401 response body", sglog.Error(encErr))
			}
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), tokenSubjectKey, sub)))
	})
}

// isJWKSError distinguishes infrastructure failures (JWKS fetch) from routine token
// validation failures (bad token, expired, wrong issuer), so callers can log
// infrastructure failures without spamming logs for every bad client request.
func isJWKSError(err error) bool {
	return strings.Contains(err.Error(), "failed to fetch JWKS")
}

// makeWellKnownHandler proxies Okta's OAuth metadata so Claude Code (or MCP client) can discover
// /authorize and /token to authenticate. No Dynamic Client Registration (DCR) : a client_id is provided.
func makeWellKnownHandler(oktaBaseURL string, logger sglog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		resp, err := proxyClient.Get(oktaBaseURL + "/.well-known/oauth-authorization-server")
		if err != nil {
			logger.Error("failed to fetch Okta metadata", sglog.Error(err))
			http.Error(w, fmt.Sprintf("failed to reach Okta: %v", err), http.StatusBadGateway)
			return
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			logger.Error("Okta metadata returned non-200", sglog.Int("status", resp.StatusCode))
			http.Error(w, fmt.Sprintf("Okta returned %d", resp.StatusCode), http.StatusBadGateway)
			return
		}

		var metadata map[string]any
		if err := json.NewDecoder(resp.Body).Decode(&metadata); err != nil {
			logger.Error("failed to decode Okta metadata", sglog.Error(err))
			http.Error(w, "upstream error", http.StatusBadGateway)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		if encErr := json.NewEncoder(w).Encode(metadata); encErr != nil {
			logger.Warn("failed to write well-known response body", sglog.Error(encErr))
		}
	}
}

// jwtVerifier validates Okta JWT tokens via JWKS.
type jwtVerifier struct {
	jwksURL string
	issuer  string
	cache   *jwk.Cache
}

func newJWTVerifier(ctx context.Context, oktaBaseURL string, logger sglog.Logger) (*jwtVerifier, error) {
	jwksURL := oktaBaseURL + "/oauth2/v1/keys"

	cache := jwk.NewCache(ctx)
	if err := cache.Register(jwksURL, jwk.WithMinRefreshInterval(15*time.Minute)); err != nil {
		return nil, fmt.Errorf("failed to register JWKS URL: %w", err)
	}
	go func() {
		if _, err := cache.Refresh(ctx, jwksURL); err != nil {
			logger.Error("initial JWKS fetch failed; authentication unavailable until cache refreshes",
				sglog.String("url", jwksURL),
				sglog.Error(err),
			)
		}
	}()

	return &jwtVerifier{
		jwksURL: jwksURL,
		issuer:  oktaBaseURL,
		cache:   cache,
	}, nil
}

// verify extracts and validates the Bearer token, returning the subject claim.
func (v *jwtVerifier) verify(authHeader string) (string, error) {
	if !strings.HasPrefix(authHeader, "Bearer ") {
		return "", fmt.Errorf("missing Bearer token")
	}
	tokenStr := strings.TrimPrefix(authHeader, "Bearer ")

	// CachedSet resolves keys from RAM; triggers a JWKS refresh only on unknown kid (okta key rotation).
	tok, err := jwt.Parse([]byte(tokenStr),
		jwt.WithKeySet(jwk.NewCachedSet(v.cache, v.jwksURL)),
		jwt.WithIssuer(v.issuer),
		jwt.WithValidate(true),
	)
	if err != nil {
		return "", fmt.Errorf("invalid token: %w", err)
	}

	return tok.Subject(), nil
}

// buildMCPServer creates the MCP server with the zoekt_search tool.
func buildMCPServer(searcher zoekt.Streamer, logger sglog.Logger) *server.MCPServer {
	s := server.NewMCPServer("zoekt-search", index.Version,
		server.WithToolCapabilities(false),
	)

	zoektSearchTool := mcp.NewTool("zoekt_search",
		mcp.WithDescription("Search code across all internal LBC repositories using Zoekt."),
		mcp.WithString("query",
			mcp.Required(),
			mcp.Description(`Zoekt query string. Examples:
              "needle"           full-text search
              "r:reponame"       repo filter
              "file:template"    filename filter
              "lang:yaml"        language filter
              "sym:data"         symbol definitions
              "fork:no"          exclude forks
              "-lang:go"         negation (exclude)
              "foo or bar"       logical OR (AND is implicit)`),
		),
		mcp.WithNumber("num",
			mcp.Description("Max number of results (default 200)"),
		),
	)

	s.AddTool(zoektSearchTool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		queryStr, err := req.RequireString("query")
		if err != nil {
			return mcp.NewToolResultError("query parameter is required"), nil
		}

		num := 200
		if n := req.GetFloat("num", 0); n > 0 {
			num = int(n) //nolint:gosec
		}

		sub, ok := subjectFromContext(ctx)
		if !ok {
			logger.Error("zoekt_search reached tool handler without authenticated subject")
			return mcp.NewToolResultError("internal error: unauthenticated request"), nil
		}
		logger.Info("zoekt_search called",
			sglog.String("subject", sub),
			sglog.String("query", queryStr),
			sglog.Int("num", num),
		)

		results, err := runSearch(ctx, searcher, queryStr, num)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("search failed: %v", err)), nil
		}

		out, err := json.Marshal(results)
		if err != nil {
			logger.Error("failed to marshal search results",
				sglog.Error(err),
				sglog.String("query", queryStr),
			)
			return mcp.NewToolResultError("internal error: failed to serialize results"), nil
		}
		return mcp.NewToolResultText(string(out)), nil
	})

	return s
}

type searchResult struct {
	FileCountTotal  int               `json:"file_count_total"`
	MatchCountTotal int               `json:"match_count_total"`
	Truncated       bool              `json:"truncated"`
	Files           []zoekt.FileMatch `json:"files"`
}

func runSearch(ctx context.Context, searcher zoekt.Streamer, queryStr string, num int) (*searchResult, error) {
	q, err := query.Parse(queryStr)
	if err != nil {
		return nil, fmt.Errorf("invalid query: %w", err)
	}

	opts := &zoekt.SearchOptions{
		MaxDocDisplayCount: num,
	}

	sr, err := searcher.Search(ctx, q, opts)
	if err != nil {
		return nil, err
	}

	return &searchResult{
		FileCountTotal:  sr.Stats.FileCount,
		MatchCountTotal: sr.Stats.MatchCount,
		Truncated:       len(sr.Files) < sr.Stats.FileCount,
		Files:           sr.Files,
	}, nil
}
