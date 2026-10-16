package service

import (
	"context"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	secutils "github.com/Tencent/WeKnora/internal/utils"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestTestConnection_MilvusRejectsAuthenticationFailure(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := grpc.NewServer(grpc.UnknownServiceHandler(func(_ any, _ grpc.ServerStream) error {
		return status.Error(codes.Unauthenticated, "invalid credentials")
	}))
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() {
		server.Stop()
		_ = listener.Close()
		secutils.SetSSRFWhitelistFromRaw("")
	})
	secutils.SetSSRFWhitelistFromRaw("127.0.0.1")

	svc := NewVectorStoreService(&mockVectorStoreRepo{}, nil, nil, nil, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, err = svc.TestConnection(ctx, types.MilvusRetrieverEngineType, types.ConnectionConfig{
		Addr: listener.Addr().String(), Username: "user", Password: "wrong",
	})
	if err == nil || !strings.Contains(err.Error(), "authentication failed") {
		t.Fatalf("expected Milvus authentication failure, got %v", err)
	}
}

func TestTestConnection_MilvusBlocksUnsafeAddrAtDialSink(t *testing.T) {
	svc := NewVectorStoreService(&mockVectorStoreRepo{}, nil, nil, nil, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := svc.TestConnection(ctx, types.MilvusRetrieverEngineType, types.ConnectionConfig{
		Addr: "169.254.169.254:19530",
	})
	if err == nil {
		t.Fatal("expected milvus connectivity probe to reject unsafe address")
	}
	if !strings.Contains(err.Error(), "failed to connect to milvus") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestTestConnection_DorisBlocksUnsafeAddrAtDialSink(t *testing.T) {
	svc := NewVectorStoreService(&mockVectorStoreRepo{}, nil, nil, nil, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := svc.TestConnection(ctx, types.DorisRetrieverEngineType, types.ConnectionConfig{
		Addr:     "169.254.169.254:9030",
		Database: "weknora",
		Username: "root",
	})
	if err == nil {
		t.Fatal("expected doris connectivity probe to reject unsafe address")
	}
	if !strings.Contains(err.Error(), "failed to connect to doris") {
		t.Fatalf("unexpected error: %v", err)
	}
}
