package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"

	"github.com/hashicorp/go-retryablehttp"

	"github.com/google/zoekt/internal/check"
)

func canConnectToFrontendInternal(sg Sourcegraph) check.Check {
	return check.Check{
		Name:        "can_connect_to_sourcegraph_frontend_internal",
		Description: "Zoekt can reach the internal API of Sourcegraph frontend",
		Run: func(ctx context.Context) (string, error) {
			cl, ok := sg.(*sourcegraphClient)
			if !ok {
				// This can only happen if we run indexserver without a Sourcegraph instance.
				return "", fmt.Errorf("not a sourcegraphClient")
			}

			u := cl.Root.ResolveReference(&url.URL{Path: "/.internal/ping"})
			req, err := retryablehttp.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
			if err != nil {
				return "", err
			}

			resp, err := cl.doRequest(req)
			if err != nil {
				return "", err
			}
			defer resp.Body.Close()

			if resp.StatusCode != 200 {
				return "", fmt.Errorf("url=%s status=%d", u.String(), resp.StatusCode)
			}

			b, err := io.ReadAll(resp.Body)
			if err != nil {
				return "", err
			}

			return string(b), nil
		},
	}
}
