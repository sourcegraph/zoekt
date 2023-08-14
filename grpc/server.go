package grpc

import (
	"context"
	"math"

	"github.com/sourcegraph/zoekt/grpc/chunk"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/sourcegraph/zoekt"
	v1 "github.com/sourcegraph/zoekt/grpc/v1"
	"github.com/sourcegraph/zoekt/query"
	"github.com/sourcegraph/zoekt/stream"
)

func NewServer(s zoekt.Streamer) *Server {
	return &Server{
		streamer: s,
	}
}

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

	sender := gRPCChunkSender(ss)
	sampler := stream.NewSamplingSender(sender)

	err = s.streamer.StreamSearch(ss.Context(), q, zoekt.SearchOptionsFromProto(req.GetOpts()), sampler)
	if err == nil {
		sampler.Flush()
	}
	return err
}

func (s *Server) List(ctx context.Context, req *v1.ListRequest) (*v1.ListResponse, error) {
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
func gRPCChunkSender(ss v1.WebserverService_StreamSearchServer) zoekt.Sender {
	f := func(r *zoekt.SearchResult) {
		result := r.ToProto()

		if len(result.GetFiles()) == 0 { // stats-only result, send it immediately
			_ = ss.Send(result)
			return
		}

		// Otherwise, chunk the file matches into multiple responses

		statsSent := false
		numFilesSent := 0

		sendFunc := func(filesChunk []*v1.FileMatch) error {
			numFilesSent += len(filesChunk)

			var stats *v1.Stats
			if !statsSent { // We only send stats back on the first chunk
				statsSent = true
				stats = result.GetStats()
			}

			progress := result.GetProgress()

			if numFilesSent < len(result.GetFiles()) { // more chunks to come
				progress = &v1.Progress{
					Priority: result.GetProgress().GetPriority(),

					// We want the client to consume the entire set of chunks - so we manually
					// patch the MaxPendingPriority to be >= overall priority.
					MaxPendingPriority: math.Max(
						result.GetProgress().GetPriority(),
						result.GetProgress().GetMaxPendingPriority(),
					),
				}
			}

			// We need to ensure that we send the repository URLs and LineFragments for only the repositories
			// actually mentioned in this chunk of files.
			repoURLs := make(map[string]string, len(result.GetRepoUrls()))
			lineFragments := make(map[string]string, len(result.GetLineFragments()))

			for _, file := range filesChunk {
				repository := file.GetRepository()

				if url, ok := result.GetRepoUrls()[repository]; ok {
					repoURLs[repository] = url
				}

				if fragment, ok := result.GetLineFragments()[repository]; ok {
					lineFragments[repository] = fragment
				}
			}

			response := &v1.SearchResponse{
				Files:    filesChunk,
				Stats:    stats,
				Progress: progress,

				RepoUrls:      repoURLs,
				LineFragments: lineFragments,
			}

			return ss.Send(response)
		}

		chunker := chunk.New(sendFunc)
		err := chunker.Send(result.GetFiles()...)
		if err != nil {
			return
		}

		_ = chunker.Flush()
	}

	return stream.SenderFunc(f)
}
