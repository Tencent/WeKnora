package qdrant

import (
	"errors"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestIsMissingCollection(t *testing.T) {
	if !isMissingCollection(status.Error(codes.NotFound, "collection does not exist")) {
		t.Fatal("NotFound collection error must be treated as an idempotent delete")
	}
	if isMissingCollection(errors.New("network unavailable")) {
		t.Fatal("non-gRPC error must not be treated as a missing collection")
	}
}
