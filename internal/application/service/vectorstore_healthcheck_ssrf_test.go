package service

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
)

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
