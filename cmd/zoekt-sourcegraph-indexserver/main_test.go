package main

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

func TestUpdateOptions(t *testing.T) {
	tests := []string{
		"test,test",
		"",
	}

	for _, test := range tests {
		t.Run(test, func(t *testing.T) {
			req, err := http.NewRequest("POST", "/options", nil)
			if err != nil {
				t.Fatal(err)
			}

			form := url.Values{}
			form.Add("large_files", test)

			req.PostForm = form

			rr := httptest.NewRecorder()

			rootURL, _ := url.Parse("http://localhost:3080")

			s := &Server{
				Root:       rootURL,
				IndexDir:   "",
				Interval:   1000,
				CPUCount:   1,
				LargeFiles: "random,value",
			}

			s.ServeHTTP(rr, req)

			if s.LargeFiles != test {
				t.Errorf("did not properly set large files option; got %s wanted %s", s.LargeFiles, test)
			}
		})
	}
}
