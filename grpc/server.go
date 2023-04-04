package grpc

import (
	"github.com/sourcegraph/zoekt/grpc/v1"
)

type Server struct {
	v1.UnimplementedWebserverServiceServer
}
