package server

import (
	"context"
	"math"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/sourcegraph/zoekt"
	"github.com/sourcegraph/zoekt/grpc/chunk"
	webserverv1 "github.com/sourcegraph/zoekt/grpc/protos/zoekt/webserver/v1"
	"github.com/sourcegraph/zoekt/query"
)

func NewServer(s zoekt.Streamer) *Server {
	return &Server{
		streamer: s,
	}
}

type Server struct {
	webserverv1.UnimplementedWebserverServiceServer
	streamer zoekt.Streamer
}

func (s *Server) Search(ctx context.Context, req *webserverv1.SearchRequest) (*webserverv1.SearchResponse, error) {
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

func (s *Server) StreamSearch(req *webserverv1.StreamSearchRequest, ss webserverv1.WebserverService_StreamSearchServer) error {
	request := req.GetRequest()

	q, err := query.QFromProto(request.GetQuery())
	if err != nil {
		return status.Error(codes.InvalidArgument, err.Error())
	}

	sender := gRPCChunkSender(ss)
	sampler := newSamplingSender(sender)

	err = s.streamer.StreamSearch(ss.Context(), q, zoekt.SearchOptionsFromProto(request.GetOpts()), sampler)
	if err == nil {
		sampler.Flush()
	}
	return err
}

func (s *Server) List(ctx context.Context, req *webserverv1.ListRequest) (*webserverv1.ListResponse, error) {
	q, err := query.QFromProto(req.GetQuery())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	repoList, err := s.streamer.List(ctx, q, zoekt.ListOptionsFromProto(req.GetOpts()))
	if err != nil {
		return nil, err
	}

	return repoList.ToProto(), nil
}

// gRPCChunkSender is a zoekt.Sender that sends small chunks of FileMatches to the provided gRPC stream.
func gRPCChunkSender(ss webserverv1.WebserverService_StreamSearchServer) zoekt.Sender {
	f := func(r *zoekt.SearchResult) {
		result := r.ToStreamProto().GetResponseChunk()

		if len(result.GetFiles()) == 0 { // stats-only result, send it immediately
			_ = ss.Send(&webserverv1.StreamSearchResponse{
				ResponseChunk: result,
			})
			return
		}

		// Otherwise, chunk the file matches into multiple responses

		statsSent := false
		numFilesSent := 0

		sendFunc := func(filesChunk []*webserverv1.FileMatch) error {
			numFilesSent += len(filesChunk)

			var stats *webserverv1.Stats
			if !statsSent { // We only send stats back on the first chunk
				statsSent = true
				stats = result.GetStats()
			}

			progress := result.GetProgress()

			if numFilesSent < len(result.GetFiles()) { // more chunks to come
				progress = &webserverv1.Progress{
					Priority: result.GetProgress().GetPriority(),

					// We want the client to consume the entire set of chunks - so we manually
					// patch the MaxPendingPriority to be >= overall priority.
					MaxPendingPriority: math.Max(
						result.GetProgress().GetPriority(),
						result.GetProgress().GetMaxPendingPriority(),
					),
				}
			}

			return ss.Send(&webserverv1.StreamSearchResponse{
				ResponseChunk: &webserverv1.SearchResponse{
					Files: filesChunk,

					Stats:    stats,
					Progress: progress,
				},
			})
		}

		_ = chunk.SendAll(sendFunc, result.GetFiles()...)
	}

	return zoekt.SenderFunc(f)
}
