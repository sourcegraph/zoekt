package main

import (
	"testing"
	"testing/quick"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
)

func TestIndexOptions_RoundTrip(t *testing.T) {
	var diff string
	f := func(original indexOptionsItem) bool {
		var converted indexOptionsItem
		converted.FromProto(original.ToProto())

		options := []cmp.Option{
			// The CloneURL field doesn't exist in the subset of fields that proto.ZoektIndexOptions contains.
			cmpopts.IgnoreFields(indexOptionsItem{}, "CloneURL"),
		}

		if diff = cmp.Diff(original, converted, options...); diff != "" {
			return false
		}
		return true
	}

	if err := quick.Check(f, nil); err != nil {
		t.Errorf("indexOptionsItem diff (-want +got):\n%s", diff)
	}
}
