package main

import (
	"net/url"
	"testing"

	gerrit "github.com/andygrunwald/go-gerrit"
)

func TestSelectProjectURL(t *testing.T) {
	httpScheme := gerrit.DownloadSchemeInfo{URL: "https://admin@gerrit.example.com/a/${project}"}
	anonScheme := gerrit.DownloadSchemeInfo{URL: "https://gerrit.example.com/a/${project}"}

	for _, tc := range []struct {
		name          string
		schemes       map[string]gerrit.DownloadSchemeInfo
		wantURL       string
		wantNeedsAuth bool
	}{
		{name: "http auth required", schemes: map[string]gerrit.DownloadSchemeInfo{"http": {URL: httpScheme.URL, IsAuthRequired: true}}, wantURL: httpScheme.URL, wantNeedsAuth: true},
		{name: "http no auth required", schemes: map[string]gerrit.DownloadSchemeInfo{"http": {URL: httpScheme.URL}}, wantURL: httpScheme.URL},
		{name: "anonymous http only", schemes: map[string]gerrit.DownloadSchemeInfo{"anonymous http": anonScheme}, wantURL: anonScheme.URL},
		{name: "no supported scheme", schemes: map[string]gerrit.DownloadSchemeInfo{"ssh": anonScheme}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			info := &gerrit.ServerInfo{Download: gerrit.DownloadInfo{Schemes: tc.schemes}}
			gotURL, gotNeedsAuth := selectProjectURL(info)
			if gotURL != tc.wantURL || gotNeedsAuth != tc.wantNeedsAuth {
				t.Fatalf("selectProjectURL() = (%q, %v), want (%q, %v)", gotURL, gotNeedsAuth, tc.wantURL, tc.wantNeedsAuth)
			}
		})
	}
}

// Regression for the reported bug: passwords containing '/' previously broke
// url.Parse because they were spliced into the raw URL string.
func TestBuildCloneURLRoundTripsPasswords(t *testing.T) {
	const template = "https://admin@gerrit.example.com/a/${project}"
	for _, pw := range []string{"secret", "abc/def", "ZOSOKjgV/kgEkN0bzPJp+oGeJLqpXykqWFJpon/Ckg", "p@ss#1?x"} {
		t.Run("password "+pw, func(t *testing.T) {
			u, err := buildCloneURL(template, "legacy/modules", true, url.UserPassword("admin", pw))
			if err != nil {
				t.Fatalf("buildCloneURL: %v", err)
			}

			// git is handed cloneURL.String(), so assert on the serialized form
			// rather than on the fields buildCloneURL just assigned.
			got, err := url.Parse(u.String())
			if err != nil {
				t.Fatalf("url.Parse(%q): %v", u.String(), err)
			}
			if pass, ok := got.User.Password(); !ok || pass != pw {
				t.Fatalf("round-trip password = (%q, %v), want (%q, true)", pass, ok, pw)
			}
			if name := got.User.Username(); name != "admin" {
				t.Fatalf("round-trip username = %q, want \"admin\"", name)
			}
			if got.Host != "gerrit.example.com" || got.Path != "/a/legacy/modules" {
				t.Fatalf("host/path changed: %q/%q", got.Host, got.Path)
			}
		})
	}
}

func TestBuildCloneURLAnonymous(t *testing.T) {
	u, err := buildCloneURL("https://gerrit.example.com/a/${project}", "p", false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if u.User != nil {
		t.Fatalf("user = %v, want nil", u.User)
	}
}

// Without --http-credentials rootURL.User is nil. The clone URL must keep the
// username Gerrit put in the download scheme so git can still resolve the
// password via a credential helper or .netrc.
func TestBuildCloneURLKeepsSchemeUserWithoutCredentials(t *testing.T) {
	u, err := buildCloneURL("https://admin@gerrit.example.com/a/${project}", "legacy/modules", true, nil)
	if err != nil {
		t.Fatalf("buildCloneURL: %v", err)
	}
	if u.User == nil || u.User.Username() != "admin" {
		t.Fatalf("user = %v, want admin", u.User)
	}
	if _, ok := u.User.Password(); ok {
		t.Fatalf("password set, want none: %q", u.String())
	}
}

func TestParseHTTPCredentials(t *testing.T) {
	for _, tc := range []struct {
		name      string
		creds     string
		wantUser  string
		wantPass  string
		wantError bool
	}{
		{name: "simple", creds: "admin:secret", wantUser: "admin", wantPass: "secret"},
		{name: "password containing colon", creds: "admin:pa:ss", wantUser: "admin", wantPass: "pa:ss"},
		{name: "password containing slash", creds: "admin:abc/def", wantUser: "admin", wantPass: "abc/def"},
		{name: "surrounding whitespace", creds: "\n admin:pa:ss\n", wantUser: "admin", wantPass: "pa:ss"},
		{name: "empty password", creds: "admin:", wantUser: "admin", wantPass: ""},
		{name: "no colon", creds: "adminonly", wantError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			user, err := parseHTTPCredentials(tc.creds)
			if tc.wantError {
				if err == nil {
					t.Fatalf("parseHTTPCredentials(%q) succeeded, want error", tc.creds)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseHTTPCredentials(%q): %v", tc.creds, err)
			}
			pass, ok := user.Password()
			if user.Username() != tc.wantUser || !ok || pass != tc.wantPass {
				t.Fatalf("got (%q, %q, %v), want (%q, %q, true)", user.Username(), pass, ok, tc.wantUser, tc.wantPass)
			}
		})
	}
}
