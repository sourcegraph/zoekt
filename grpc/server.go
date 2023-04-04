package grpc

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/sourcegraph/zoekt"
	"github.com/sourcegraph/zoekt/grpc/v1"
)

type Server struct {
	v1.UnimplementedWebserverServiceServer
	zoekt.Streamer
}

func (s *Server) Search(ctx context.Context, req *v1.SearchRequest) (*v1.SearchResponse, error) {
	res, err := s.Streamer.Search(ctx, zoekt.QFromProto(req.GetQuery()), zoekt.SearchOptionsFromProto(req.GetOpts()))
	if err != nil {
		return nil, err
	}

	return res.ToProto(), nil
}

func (s *Server) StreamSearch(req *v1.SearchRequest, stream v1.WebserverService_StreamSearchServer) error {
	return status.Errorf(codes.Unimplemented, "method StreamSearch not implemented")
}
func (s *Server) List(ctx context.Context, req *v1.ListRequest) (*v1.ListResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "method List not implemented")
}
