package main

import "testing"

func TestCloneURLWithCredentials(t *testing.T) {
	cases := []struct {
		name string
		href string
		want string
	}{
		{
			name: "username in link",
			href: "https://alice@bitbucket.example.com/scm/proj/repo.git",
			want: "https://alice:secret@bitbucket.example.com/scm/proj/repo.git",
		},
		{
			name: "no username in link (HTTP access token auth)",
			href: "https://bitbucket.example.com/scm/proj/repo.git",
			want: "https://alice:secret@bitbucket.example.com/scm/proj/repo.git",
		},
		{
			name: "no username in link, non-default port",
			href: "http://bitbucket.example.com:7990/scm/proj/repo.git",
			want: "http://alice:secret@bitbucket.example.com:7990/scm/proj/repo.git",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := cloneURLWithCredentials(tc.href, "alice", "secret"); got != tc.want {
				t.Errorf("cloneURLWithCredentials(%q) = %q, want %q", tc.href, got, tc.want)
			}
		})
	}
}
