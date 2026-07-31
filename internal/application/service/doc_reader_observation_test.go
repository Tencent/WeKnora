package service

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/types"
)

type countingDocReader struct {
	mu sync.Mutex

	result     *types.ReadResult
	err        error
	requests   int
	operations []types.IngestionOperation
}

func (r *countingDocReader) Read(
	ctx context.Context,
	_ *types.ReadRequest,
) (*types.ReadResult, error) {
	operation := types.IngestionOperationFromContext(ctx)
	r.mu.Lock()
	r.requests++
	r.operations = append(r.operations, operation)
	r.mu.Unlock()
	return r.result, r.err
}

func (r *countingDocReader) Snapshot() (int, []types.IngestionOperation) {
	r.mu.Lock()
	defer r.mu.Unlock()
	operations := append([]types.IngestionOperation(nil), r.operations...)
	return r.requests, operations
}

func TestDocReaderObservation_RecordsRealParserRequest(t *testing.T) {
	inner := &countingDocReader{result: &types.ReadResult{
		MarkdownContent: "parsed markdown",
		ImageRefs: []types.ImageRef{
			{ImageData: []byte{1, 2, 3}},
		},
	}}
	observed := newIngestionObservedDocReader(inner)
	req := &types.ReadRequest{FileContent: []byte("source document")}

	result, err := observed.Read(context.Background(), req)
	require.NoError(t, err)
	require.Equal(t, "parsed markdown", result.MarkdownContent)

	requests, operations := inner.Snapshot()
	require.Equal(t, 1, requests)
	require.Equal(t, []types.IngestionOperation{types.IngestionOperationParseDocument}, operations)

	snapshot := observed.Snapshot()
	require.Equal(t, 1, snapshot.RequestCount)
	require.Equal(t, int64(len(req.FileContent)), snapshot.InputBytes)
	require.Equal(t, len(result.MarkdownContent), snapshot.OutputChars)
	require.Equal(t, int64(len(result.MarkdownContent)+3), snapshot.OutputBytes)
	require.Equal(t, 1, snapshot.Images)

	output := parseDocumentOperationOutput("mineru", snapshot, true)
	require.Equal(t, string(types.IngestionOperationParseDocument), output["operation"])
	require.Equal(t, types.StageDocReader, output["stage"])
	require.Equal(t, "mineru", output["model_id"])
	require.Equal(t, "document_parser", output["model_type"])
	require.Equal(t, 1, output["request_count"])
	require.Equal(t, "not_supported", output["cache_status"])
}

func TestDocReaderObservation_FailureStillCountsRequest(t *testing.T) {
	inner := &countingDocReader{err: errors.New("parser unavailable")}
	observed := newIngestionObservedDocReader(inner)

	_, err := observed.Read(context.Background(), &types.ReadRequest{FileContent: []byte("input")})
	require.EqualError(t, err, "parser unavailable")

	snapshot := observed.Snapshot()
	require.Equal(t, 1, snapshot.RequestCount)
	require.Equal(t, 1, snapshot.ErrorCount)
	output := parseDocumentOperationOutput("remote", snapshot, false)
	require.Equal(t, 1, output["request_count"])
	require.Equal(t, 0, output["computed_items"])
	require.Equal(t, false, output["success"])
}

func TestDocReaderObservation_PreCacheBaselineRecomputesSameDocument(t *testing.T) {
	inner := &countingDocReader{result: &types.ReadResult{MarkdownContent: "same result"}}
	req := &types.ReadRequest{FileContent: []byte("unchanged document")}

	for range 2 {
		observed := newIngestionObservedDocReader(inner)
		_, err := observed.Read(context.Background(), req)
		require.NoError(t, err)
		require.Equal(t, 1, observed.Snapshot().RequestCount)
	}

	requests, operations := inner.Snapshot()
	require.Equal(t, 2, requests)
	require.Equal(t, []types.IngestionOperation{
		types.IngestionOperationParseDocument,
		types.IngestionOperationParseDocument,
	}, operations)
}

func TestDocReaderObservation_ConcurrentRequests(t *testing.T) {
	inner := &countingDocReader{result: &types.ReadResult{MarkdownContent: "result"}}
	observed := newIngestionObservedDocReader(inner)
	const requests = 24

	var wg sync.WaitGroup
	wg.Add(requests)
	for range requests {
		go func() {
			defer wg.Done()
			_, _ = observed.Read(context.Background(), &types.ReadRequest{FileContent: []byte("input")})
		}()
	}
	wg.Wait()

	require.Equal(t, requests, observed.Snapshot().RequestCount)
}

func TestDocReaderObservation_SpanMatchesParserCount(t *testing.T) {
	const knowledgeID = "knowledge-parser-observation"
	ctx := context.Background()
	tracker, db := setupSpanTrackerTest(t)

	_, attempt, err := tracker.OpenAttempt(ctx, knowledgeID, "")
	require.NoError(t, err)
	span := tracker.BeginStage(
		ctx,
		knowledgeID,
		attempt,
		types.StageDocReader,
		types.JSONMap{"file_type": "pdf"},
	)
	require.NotNil(t, span)

	inner := &countingDocReader{result: &types.ReadResult{MarkdownContent: "parsed"}}
	observed := newIngestionObservedDocReader(inner)
	_, err = observed.Read(context.Background(), &types.ReadRequest{FileContent: []byte("source")})
	require.NoError(t, err)

	parserRequests, _ := inner.Snapshot()
	observation := observed.Snapshot()
	require.Equal(t, parserRequests, observation.RequestCount)

	tracker.EndSpan(ctx, span, parseDocumentOperationOutput("mineru", observation, true))

	var stored types.KnowledgeProcessingSpan
	require.NoError(t, db.Where(
		"knowledge_id = ? AND attempt = ? AND name = ?",
		knowledgeID,
		attempt,
		types.StageDocReader,
	).Take(&stored).Error)
	require.Equal(t, types.SpanStatusDone, stored.Status)
	require.Equal(t, string(types.IngestionOperationParseDocument), stored.Output["operation"])
	require.EqualValues(t, parserRequests, stored.Output["request_count"])
	require.Equal(t, "not_supported", stored.Output["cache_status"])
}
