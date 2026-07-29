package types

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestIngestionOperationObservationToJSONMap(t *testing.T) {
	chunkIndex := 3
	imageIndex := 1

	observation := IngestionOperationObservation{
		Operation:      IngestionOperationMultimodalOCR,
		Stage:          StageMultimodal,
		ModelID:        "vlm-test",
		ModelType:      "vlm",
		Provider:       "test",
		OperationCount: 1,
		RequestCount:   1,
		BatchCount:     1,
		TotalItems:     1,
		ComputedItems:  1,
		ReusedItems:    0,
		InputBytes:     1024,
		OutputChars:    42,
		CacheStatus:    IngestionCacheStatusNotSupported,
		Success:        true,
		ChunkID:        "chunk-1",
		ChunkIndex:     &chunkIndex,
		ImageIndex:     &imageIndex,
	}

	got := observation.ToJSONMap()

	require.Equal(
		t,
		string(IngestionOperationMultimodalOCR),
		got["operation"],
	)
	require.Equal(t, StageMultimodal, got["stage"])
	require.Equal(t, "vlm-test", got["model_id"])
	require.Equal(t, "vlm", got["model_type"])
	require.Equal(t, "test", got["provider"])

	require.Equal(t, 1, got["operation_count"])
	require.Equal(t, 1, got["request_count"])
	require.Equal(t, 1, got["batch_count"])

	require.Equal(t, 1, got["total_items"])
	require.Equal(t, 1, got["computed_items"])
	require.Equal(t, 0, got["reused_items"])

	require.Equal(t, int64(1024), got["input_bytes"])
	require.Equal(t, 42, got["output_chars"])

	require.Equal(
		t,
		string(IngestionCacheStatusNotSupported),
		got["cache_status"],
	)
	require.Equal(t, true, got["success"])

	require.Equal(t, "chunk-1", got["chunk_id"])
	require.Equal(t, 3, got["chunk_index"])
	require.Equal(t, 1, got["image_index"])

	require.NotContains(t, got, "error_code")
	require.NotContains(t, got, "artifact_kind")
	require.NotContains(t, got, "input_digest_prefix")
	require.NotContains(t, got, "dependency_digest_prefix")
	require.NotContains(t, got, "artifact_schema_version")
}

func TestIngestionOperationObservationToJSONMapIncludesZeroRequestCount(
	t *testing.T,
) {
	observation := IngestionOperationObservation{
		Operation:      IngestionOperationMultimodalOCR,
		Stage:          StageMultimodal,
		OperationCount: 1,
		RequestCount:   0,
		TotalItems:     1,
		ComputedItems:  0,
		ReusedItems:    0,
		CacheStatus:    IngestionCacheStatusNotSupported,
		Success:        false,
		ErrorCode:      "VLM_RESOLVE_FAILED",
	}

	got := observation.ToJSONMap()

	require.Equal(t, 1, got["operation_count"])
	require.Equal(t, 0, got["request_count"])
	require.Equal(t, 1, got["total_items"])
	require.Equal(t, 0, got["computed_items"])
	require.Equal(t, false, got["success"])
	require.Equal(t, "VLM_RESOLVE_FAILED", got["error_code"])
}

func TestIngestionOperationObservationToJSONMapClampsNegativeCounts(
	t *testing.T,
) {
	observation := IngestionOperationObservation{
		Operation:      IngestionOperationEmbeddingChunk,
		OperationCount: -1,
		RequestCount:   -2,
		BatchCount:     -3,
		TotalItems:     -4,
		ComputedItems:  -5,
		ReusedItems:    -6,
		InputChars:     -7,
		InputBytes:     -8,
		OutputChars:    -9,
		OutputBytes:    -10,
	}

	got := observation.ToJSONMap()

	require.Equal(t, 0, got["operation_count"])
	require.Equal(t, 0, got["request_count"])
	require.Equal(t, 0, got["batch_count"])
	require.Equal(t, 0, got["total_items"])
	require.Equal(t, 0, got["computed_items"])
	require.Equal(t, 0, got["reused_items"])
	require.Equal(t, 0, got["input_chars"])
	require.Equal(t, int64(0), got["input_bytes"])
	require.Equal(t, 0, got["output_chars"])
	require.Equal(t, int64(0), got["output_bytes"])
}

func TestIngestionOperationContext(t *testing.T) {
	ctx := WithIngestionOperation(
		context.Background(),
		IngestionOperationMultimodalOCR,
	)

	require.Equal(
		t,
		IngestionOperationMultimodalOCR,
		IngestionOperationFromContext(ctx),
	)
}

func TestIngestionOperationFromContextReturnsEmptyWhenMissing(t *testing.T) {
	require.Equal(
		t,
		IngestionOperation(""),
		IngestionOperationFromContext(context.Background()),
	)
}

func TestIngestionOperationFromContextHandlesNilContext(t *testing.T) {
	require.Equal(
		t,
		IngestionOperation(""),
		IngestionOperationFromContext(nil),
	)
}
