package grpc

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	v1 "github.com/Harporis/harporis/contracts/gen/go/harporis/v1"
)

// LocalStartFunc is injected from main when AllowLocalStart is true.
// It owns starting the scan asynchronously.
type LocalStartFunc func(context.Context, *v1.ScanRequest) (*v1.StartScanLocalResponse, error)

type Options struct {
	AllowLocalStart bool
	StartLocal      LocalStartFunc
}

type Server struct {
	v1.UnimplementedGetterServiceServer
	opts Options
}

func New(o Options) *Server { return &Server{opts: o} }

func (s *Server) Health(_ context.Context, _ *v1.HealthRequest) (*v1.HealthResponse, error) {
	return &v1.HealthResponse{Status: "SERVING"}, nil
}

func (s *Server) StartScanLocal(ctx context.Context, req *v1.ScanRequest) (*v1.StartScanLocalResponse, error) {
	if !s.opts.AllowLocalStart || s.opts.StartLocal == nil {
		return nil, status.Error(codes.PermissionDenied, "StartScanLocal disabled in this build/config")
	}
	return s.opts.StartLocal(ctx, req)
}
