package grpc

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/sourcegraph/zoekt"
	v1 "github.com/sourcegraph/zoekt/grpc/v1"
	"github.com/sourcegraph/zoekt/query"
	"github.com/sourcegraph/zoekt/stream"
)

type Server struct {
	v1.UnimplementedWebserverServiceServer
	streamer zoekt.Streamer
}

func (s *Server) Search(ctx context.Context, req *v1.SearchRequest) (*v1.SearchResponse, error) {
	q, err := query.QFromProto(req.GetQuery())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	res, err := s.streamer.Search(ctx, q, zoekt.SearchOptionsFromProto(req.GetOpts()))
	if err != nil {
		return nil, err
	}

	return res.ToProto(), nil
}

func (s *Server) StreamSearch(req *v1.SearchRequest, ss v1.WebserverService_StreamSearchServer) error {
	q, err := query.QFromProto(req.GetQuery())
	if err != nil {
		return status.Error(codes.InvalidArgument, err.Error())
	}

	onMatch := stream.SenderFunc(func(res *zoekt.SearchResult) {
		ss.Send(res.ToProto())
	})

	return s.streamer.StreamSearch(ss.Context(), q, zoekt.SearchOptionsFromProto(req.GetOpts()), onMatch)
}

func (s *Server) List(ctx context.Context, req *v1.ListRequest) (*v1.ListResponse, error) {
	q, err := query.QFromProto(req.GetQuery())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	return s.streamer.List(ctx, q, &zoekt.ListOptions{})
	return nil, status.Errorf(codes.Unimplemented, "method List not implemented")
}
