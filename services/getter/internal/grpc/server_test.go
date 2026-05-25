package grpc

import (
	"context"
	"net"
	"testing"

	"github.com/stretchr/testify/require"
	grpcpkg "google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	v1 "github.com/Harporis/harporis/contracts/gen/go/harporis/v1"
)

func TestServer_Health(t *testing.T) {
	srv := New(Options{AllowLocalStart: false})

	gs := grpcpkg.NewServer()
	v1.RegisterGetterServiceServer(gs, srv)

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	go gs.Serve(lis)
	defer gs.Stop()

	conn, err := grpcpkg.NewClient(lis.Addr().String(), grpcpkg.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	defer conn.Close()

	client := v1.NewGetterServiceClient(conn)
	resp, err := client.Health(context.Background(), &v1.HealthRequest{})
	require.NoError(t, err)
	require.Equal(t, "SERVING", resp.Status)
}

func TestServer_StartScanLocal_DisabledByDefault(t *testing.T) {
	srv := New(Options{AllowLocalStart: false})
	_, err := srv.StartScanLocal(context.Background(), &v1.ScanRequest{ScanId: "x"})
	require.Error(t, err)
}
