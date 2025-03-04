package server

import (
	"context"

	sglog "github.com/sourcegraph/log"
	"google.golang.org/grpc"

	proto "github.com/sourcegraph/zoekt/cmd/zoekt-sourcegraph-indexserver/grpc/protos/zoekt/indexserver/v1"
	"github.com/sourcegraph/zoekt/grpc/defaults"
)

type Server struct {
	logger sglog.Logger

	proto.UnimplementedSourcegraphIndexserverServiceServer
}

func NewServer(logger sglog.Logger, additionalOpts ...grpc.ServerOption) *grpc.Server {
	s := defaults.NewServer(logger, additionalOpts...)
	proto.RegisterSourcegraphIndexserverServiceServer(s, newServer(logger))
	return s
}

func newServer(logger sglog.Logger) *Server {
	return &Server{logger: logger}
}

func (s *Server) DeleteAllData(ctx context.Context, req *proto.DeleteAllDataRequest) (*proto.DeleteAllDataResponse, error) {
	s.logger.Warn("DeleteAllData")
	return &proto.DeleteAllDataResponse{}, nil
}
