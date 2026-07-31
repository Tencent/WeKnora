package types

import (
	"context"
	"testing"
)

func TestTaskRetryMetadataRoundTrip(t *testing.T) {
	ctx := WithTaskRetryMetadata(context.Background(), 4, 9)

	retryCount, maxRetry, ok := TaskRetryMetadata(ctx)
	if !ok {
		t.Fatal("TaskRetryMetadata() ok = false, want true")
	}
	if retryCount != 4 || maxRetry != 9 {
		t.Fatalf(
			"TaskRetryMetadata() = (%d, %d), want (4, 9)",
			retryCount,
			maxRetry,
		)
	}
}

func TestTaskRetryMetadataMissing(t *testing.T) {
	if _, _, ok := TaskRetryMetadata(context.Background()); ok {
		t.Fatal("TaskRetryMetadata() ok = true without metadata")
	}
}
