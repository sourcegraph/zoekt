package main

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestServeHTTPUnixSocket(t *testing.T) {
	socket := filepath.Join(t.TempDir(), "zoekt.sock")
	if err := os.WriteFile(socket, []byte("stale"), 0o600); err != nil {
		t.Fatal(err)
	}

	srv := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/healthz" {
				http.NotFound(w, r)
				return
			}
			_, _ = io.WriteString(w, "ok")
		}),
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- serveHTTP(srv, socket, "", "")
	}()

	client := &http.Client{
		Timeout: time.Second,
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				var d net.Dialer
				return d.DialContext(ctx, "unix", socket)
			},
		},
	}

	var resp *http.Response
	var err error
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		resp, err = client.Get("http://unix/healthz")
		if err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("GET over unix socket failed: %v", err)
	}

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("got status %d, want %d", resp.StatusCode, http.StatusOK)
	}
	body, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "ok" {
		t.Fatalf("got body %q, want %q", string(body), "ok")
	}
	if mode := socketMode(t, socket); mode != 0o777 {
		t.Fatalf("got socket mode %o, want 777", mode)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}

	if err := <-errCh; !errors.Is(err, http.ErrServerClosed) {
		t.Fatalf("serveHTTP returned %v, want %v", err, http.ErrServerClosed)
	}
	if _, err := os.Stat(socket); !os.IsNotExist(err) {
		t.Fatalf("socket was not removed after shutdown: %v", err)
	}
}

func socketMode(t *testing.T, socket string) os.FileMode {
	t.Helper()
	fi, err := os.Stat(socket)
	if err != nil {
		t.Fatal(err)
	}
	return fi.Mode().Perm()
}
