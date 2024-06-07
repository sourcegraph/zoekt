package zoekt

import (
	"maps"
	"testing"
)

func TestCalculateTermFrequency(t *testing.T) {
	cases := []struct {
		cands               []*candidateMatch
		wantDF              termDocumentFrequency
		wantTermFrequencies map[string]int
	}{{
		cands: []*candidateMatch{
			{substrLowered: []byte("foo")},
			{substrLowered: []byte("foo")},
			{substrLowered: []byte("bar")},
			{
				substrLowered: []byte("bas"),
				fileName:      true,
			},
		},
		wantDF: termDocumentFrequency{
			"foo": 1,
			"bar": 1,
			"bas": 1,
		},
		wantTermFrequencies: map[string]int{
			"foo": 2,
			"bar": 1,
			"bas": 5,
		},
	},
	}

	for _, c := range cases {
		t.Run("", func(t *testing.T) {
			fm := FileMatch{}
			df := make(termDocumentFrequency)
			tf := calculateTermFrequency(c.cands, df)

			if !maps.Equal(df, c.wantDF) {
				t.Errorf("got %v, want %v", df, c.wantDF)
			}

			if !maps.Equal(tf, c.wantTermFrequencies) {
				t.Errorf("got %v, want %v", fm, c.wantTermFrequencies)
			}
		})
	}
}
