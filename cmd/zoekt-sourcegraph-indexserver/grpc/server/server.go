package server

import (
	"context"

	sglog "github.com/sourcegraph/log"
	v1 "github.com/sourcegraph/zoekt/cmd/zoekt-sourcegraph-indexserver/protos/zoekt/indexserver/v1"
	"github.com/sourcegraph/zoekt/grpc/defaults"
	"google.golang.org/grpc"
)

type Server struct {
	logger sglog.Logger

	v1.UnimplementedSourcegraphIndexserverServiceServer
}

func NewServer(logger sglog.Logger, additionalOpts ...grpc.ServerOption) *grpc.Server {
	s := defaults.NewServer(logger, additionalOpts...)
	v1.RegisterSourcegraphIndexserverServiceServer(s, newServer(logger))
	return s
}

func newServer(logger sglog.Logger) *Server {
	return &Server{logger: logger}
}

func (s *Server) DeleteAllData(ctx context.Context, req *v1.DeleteAllDataRequest) (*v1.DeleteAllDataResponse, error) {
	s.logger.Warn("DeleteAllData")
	return &v1.DeleteAllDataResponse{}, nil
}
