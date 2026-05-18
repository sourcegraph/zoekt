package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	sglog "github.com/sourcegraph/log"
	"github.com/sourcegraph/log/logtest"
	"github.com/sourcegraph/zoekt"
	"github.com/sourcegraph/zoekt/internal/mockSearcher"
	"github.com/sourcegraph/zoekt/query"
)

// --- jwtAuthMiddleware ---

func TestJWTAuthMiddleware_NoToken(t *testing.T) {
	v := &fakeVerifier{err: errors.New("missing Bearer token")}
	handler := jwtAuthMiddleware(v, noopLogger(t), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/mcp", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rr.Code)
	}
	if rr.Header().Get("WWW-Authenticate") != `Bearer error="invalid_token"` {
		t.Fatalf("unexpected WWW-Authenticate: %s", rr.Header().Get("WWW-Authenticate"))
	}
	var body map[string]string
	if err := json.NewDecoder(rr.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body["error"] != "invalid_token" {
		t.Fatalf("unexpected error field: %s", body["error"])
	}
	if body["error_description"] == "" {
		t.Fatal("expected non-empty error_description")
	}
}

func TestJWTAuthMiddleware_ValidToken(t *testing.T) {
	v := &fakeVerifier{subject: "user@example.com"}

	var capturedSub string
	handler := jwtAuthMiddleware(v, noopLogger(t), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedSub, _ = subjectFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/mcp", nil)
	req.Header.Set("Authorization", "Bearer sometoken")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	if capturedSub != "user@example.com" {
		t.Fatalf("expected subject in context, got %q", capturedSub)
	}
}

func TestJWTAuthMiddleware_JWKSError_Returns401(t *testing.T) {
	v := &fakeVerifier{err: errors.New("failed to fetch JWKS: connection refused")}
	handler := jwtAuthMiddleware(v, noopLogger(t), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/mcp", nil)
	req.Header.Set("Authorization", "Bearer sometoken")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rr.Code)
	}
}

// --- subjectFromContext ---

func TestSubjectFromContext_Missing(t *testing.T) {
	_, ok := subjectFromContext(context.Background())
	if ok {
		t.Fatal("expected ok=false for empty context")
	}
}

func TestSubjectFromContext_EmptyString(t *testing.T) {
	ctx := context.WithValue(context.Background(), tokenSubjectKey, "")
	_, ok := subjectFromContext(ctx)
	if ok {
		t.Fatal("expected ok=false for empty subject string")
	}
}

func TestSubjectFromContext_Present(t *testing.T) {
	ctx := context.WithValue(context.Background(), tokenSubjectKey, "user@example.com")
	sub, ok := subjectFromContext(ctx)
	if !ok {
		t.Fatal("expected ok=true")
	}
	if sub != "user@example.com" {
		t.Fatalf("unexpected subject: %s", sub)
	}
}

// --- makeWellKnownHandler ---

func TestMakeWellKnownHandler_Success(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer":                 "https://example.okta.com",
			"authorization_endpoint": "https://example.okta.com/oauth2/v1/authorize",
		})
	}))
	defer upstream.Close()

	handler := makeWellKnownHandler(upstream.URL, noopLogger(t))
	req := httptest.NewRequest(http.MethodGet, "/.well-known/oauth-authorization-server", nil)
	rr := httptest.NewRecorder()
	handler(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	var metadata map[string]any
	if err := json.NewDecoder(rr.Body).Decode(&metadata); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if metadata["issuer"] != "https://example.okta.com" {
		t.Fatalf("unexpected issuer: %v", metadata["issuer"])
	}
}

func TestMakeWellKnownHandler_UpstreamError(t *testing.T) {
	handler := makeWellKnownHandler("http://127.0.0.1:0", noopLogger(t))
	req := httptest.NewRequest(http.MethodGet, "/.well-known/oauth-authorization-server", nil)
	rr := httptest.NewRecorder()
	handler(rr, req)

	if rr.Code != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d", rr.Code)
	}
}

func TestMakeWellKnownHandler_UpstreamNon200(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer upstream.Close()

	handler := makeWellKnownHandler(upstream.URL, noopLogger(t))
	req := httptest.NewRequest(http.MethodGet, "/.well-known/oauth-authorization-server", nil)
	rr := httptest.NewRecorder()
	handler(rr, req)

	if rr.Code != http.StatusBadGateway {
		t.Fatalf("expected 502 for upstream 500, got %d", rr.Code)
	}
}

func TestMakeWellKnownHandler_UpstreamInvalidJSON(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("not-json"))
	}))
	defer upstream.Close()

	handler := makeWellKnownHandler(upstream.URL, noopLogger(t))
	req := httptest.NewRequest(http.MethodGet, "/.well-known/oauth-authorization-server", nil)
	rr := httptest.NewRecorder()
	handler(rr, req)

	if rr.Code != http.StatusBadGateway {
		t.Fatalf("expected 502 for invalid JSON, got %d", rr.Code)
	}
}

// --- runSearch ---

func TestRunSearch_ValidQuery(t *testing.T) {
	mock := &mockSearcher.MockSearcher{
		WantSearch: mustParseQuery(t, "hello"),
		SearchResult: &zoekt.SearchResult{
			Files: []zoekt.FileMatch{{FileName: "foo.go"}},
			Stats: zoekt.Stats{FileCount: 1, MatchCount: 1},
		},
	}

	result, err := runSearch(context.Background(), streamAdapter{mock}, "hello", 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.FileCountTotal != 1 {
		t.Fatalf("expected 1 file, got %d", result.FileCountTotal)
	}
	if result.Truncated {
		t.Fatal("expected not truncated")
	}
}

func TestRunSearch_InvalidQuery(t *testing.T) {
	_, err := runSearch(context.Background(), streamAdapter{&mockSearcher.MockSearcher{}}, "(", 10)
	if err == nil {
		t.Fatal("expected error for invalid query")
	}
}

func TestRunSearch_SearcherError(t *testing.T) {
	_, err := runSearch(context.Background(), &errorSearcher{err: errors.New("index unavailable")}, "hello", 10)
	if err == nil {
		t.Fatal("expected error to be propagated from searcher")
	}
}

func TestRunSearch_Truncated(t *testing.T) {
	mock := &mockSearcher.MockSearcher{
		WantSearch: mustParseQuery(t, "hello"),
		SearchResult: &zoekt.SearchResult{
			Files: []zoekt.FileMatch{{FileName: "foo.go"}},
			Stats: zoekt.Stats{FileCount: 5, MatchCount: 5},
		},
	}

	result, err := runSearch(context.Background(), streamAdapter{mock}, "hello", 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Truncated {
		t.Fatal("expected truncated=true when returned files < total file count")
	}
}

// --- addMCPHandlers ---

func TestAddMCPHandlers_SkipsWhenEnvUnset(t *testing.T) {
	t.Setenv("ZOEKT_OKTA_BASE_URL", "")
	mux := http.NewServeMux()
	addMCPHandlers(mux, streamAdapter{&mockSearcher.MockSearcher{}})

	for _, path := range []string{mcpPath, wellKnownPath} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, req)
		if rr.Code != http.StatusServiceUnavailable {
			t.Fatalf("expected route %s to return 503, got %d", path, rr.Code)
		}
		if !strings.Contains(rr.Body.String(), "ZOEKT_OKTA_BASE_URL not set") {
			t.Fatalf("expected body to mention ZOEKT_OKTA_BASE_URL, got %q", rr.Body.String())
		}
	}
}

// --- helpers ---

// fakeVerifier is a test double for tokenVerifier.
type fakeVerifier struct {
	subject string
	err     error
}

func (f *fakeVerifier) verify(_ string) (string, error) {
	return f.subject, f.err
}

// streamAdapter wraps a Searcher to satisfy the Streamer interface.
type streamAdapter struct {
	zoekt.Searcher
}

func (a streamAdapter) StreamSearch(ctx context.Context, q query.Q, opts *zoekt.SearchOptions, sender zoekt.Sender) error {
	sr, err := a.Searcher.Search(ctx, q, opts)
	if err != nil {
		return err
	}
	sender.Send(sr)
	return nil
}

// errorSearcher always returns an error from Search.
type errorSearcher struct {
	err error
}

func (e *errorSearcher) Search(_ context.Context, _ query.Q, _ *zoekt.SearchOptions) (*zoekt.SearchResult, error) {
	return nil, e.err
}

func (e *errorSearcher) StreamSearch(_ context.Context, _ query.Q, _ *zoekt.SearchOptions, _ zoekt.Sender) error {
	return e.err
}

func (*errorSearcher) List(_ context.Context, _ query.Q, _ *zoekt.ListOptions) (*zoekt.RepoList, error) {
	return nil, nil
}

func (*errorSearcher) Close() {}

func (*errorSearcher) String() string { return "errorSearcher" }

func noopLogger(t *testing.T) sglog.Logger {
	t.Helper()
	logger, _ := logtest.Captured(t)
	return logger
}

func mustParseQuery(t *testing.T, s string) query.Q {
	t.Helper()
	q, err := query.Parse(s)
	if err != nil {
		t.Fatalf("parse query %q: %v", s, err)
	}
	return q
}
