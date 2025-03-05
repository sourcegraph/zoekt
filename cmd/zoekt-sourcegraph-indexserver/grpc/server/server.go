package server

import (
	"context"

	sglog "github.com/sourcegraph/log"
	"google.golang.org/grpc"

	indexserverv1 "github.com/sourcegraph/zoekt/cmd/zoekt-sourcegraph-indexserver/grpc/protos/zoekt/indexserver/v1"
	"github.com/sourcegraph/zoekt/grpc/defaults"
)

func NewServer(logger sglog.Logger, additionalOpts ...grpc.ServerOption) *grpc.Server {
	s := defaults.NewServer(logger, additionalOpts...)
	indexserverv1.RegisterSourcegraphIndexserverServiceServer(s, &Server{logger: logger})
	return s
}

// Server implements the SourcegraphIndexserverService gRPC service.
type Server struct {
	logger sglog.Logger

	indexserverv1.UnimplementedSourcegraphIndexserverServiceServer
}

// DeleteAllData deletes all shards in the index and trash belonging to the
// tenant associated with the request. This is stubbed out for now.
func (s *Server) DeleteAllData(ctx context.Context, req *indexserverv1.DeleteAllDataRequest) (*indexserverv1.DeleteAllDataResponse, error) {
	s.logger.Warn("DeleteAllData")
	return &indexserverv1.DeleteAllDataResponse{}, nil
}
