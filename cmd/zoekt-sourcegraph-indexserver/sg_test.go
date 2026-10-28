package main

import (
	"testing"
	"testing/quick"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"google.golang.org/protobuf/proto"

	configv1 "github.com/sourcegraph/zoekt/cmd/zoekt-sourcegraph-indexserver/grpc/protos/sourcegraph/zoekt/configuration/v1"
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

func TestUpdateIndexStatusRequest_RoundTrip(t *testing.T) {
	var diff string
	f := func(original updateIndexStatusRequest) bool {
		// Go through the wire format: nil SkippedFiles (unknown) must stay
		// distinct from present but all-zero counts.
		b, err := proto.Marshal(original.ToProto())
		if err != nil {
			t.Fatal(err)
		}
		var decoded configv1.UpdateIndexStatusRequest
		if err := proto.Unmarshal(b, &decoded); err != nil {
			t.Fatal(err)
		}
		var converted updateIndexStatusRequest
		converted.FromProto(&decoded)

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
