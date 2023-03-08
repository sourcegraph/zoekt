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

func TestRepoPathRanks_RoundTrip(t *testing.T) {
	var diff string
	f := func(original RepoPathRanks) bool {
		var converted RepoPathRanks
		converted.FromProto(original.ToProto())

		options := []cmp.Option{
			cmpopts.EquateEmpty(),
		}

		if diff = cmp.Diff(original, converted, options...); diff != "" {
			return false
		}
		return true
	}

	if err := quick.Check(f, nil); err != nil {
		t.Errorf("RepoPathRanks diff (-want +got):\n%s", diff)
	}
}

func TestUpdateIndexStatusRequest_RoundTrip(t *testing.T) {
	var diff string
	f := func(original updateIndexStatusRequest) bool {
		var converted updateIndexStatusRequest
		converted.FromProto(original.ToProto())

		options := []cmp.Option{
			cmpopts.EquateEmpty(),
		}

		if diff = cmp.Diff(original, converted, options...); diff != "" {
			return false
		}
		return true
	}

	if err := quick.Check(f, nil); err != nil {
		t.Errorf("updateIndexStatusRequest diff (-want +got):\n%s", diff)
	}
}
