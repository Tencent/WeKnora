package service

import (
	"context"
	"sync"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

type docReaderRequestSnapshot struct {
	RequestCount int
	ErrorCount   int
	InputBytes   int64
	OutputChars  int
	OutputBytes  int64
	Images       int
}

// ingestionObservedDocReader records calls entering the selected document
// parser without changing the request, result, timeout, or retry behavior.
type ingestionObservedDocReader struct {
	inner interfaces.DocReader

	mu sync.Mutex

	requestCount int
	errorCount   int
	inputBytes   int64
	outputChars  int
	outputBytes  int64
	images       int
}

var _ interfaces.DocReader = (*ingestionObservedDocReader)(nil)

func newIngestionObservedDocReader(
	inner interfaces.DocReader,
) *ingestionObservedDocReader {
	return &ingestionObservedDocReader{inner: inner}
}

func (r *ingestionObservedDocReader) Read(
	ctx context.Context,
	req *types.ReadRequest,
) (*types.ReadResult, error) {
	ctx = types.WithIngestionOperation(ctx, types.IngestionOperationParseDocument)

	var inputBytes int64
	if req != nil {
		inputBytes = int64(len(req.FileContent))
	}
	r.mu.Lock()
	r.requestCount++
	r.inputBytes += inputBytes
	r.mu.Unlock()

	result, err := r.inner.Read(ctx, req)
	r.mu.Lock()
	defer r.mu.Unlock()
	if err != nil {
		r.errorCount++
	}
	if result != nil {
		r.outputChars += len(result.MarkdownContent)
		r.outputBytes += int64(len(result.MarkdownContent))
		r.images += len(result.ImageRefs)
		for _, image := range result.ImageRefs {
			r.outputBytes += int64(len(image.ImageData))
		}
	}
	return result, err
}

func (r *ingestionObservedDocReader) Snapshot() docReaderRequestSnapshot {
	r.mu.Lock()
	defer r.mu.Unlock()
	return docReaderRequestSnapshot{
		RequestCount: r.requestCount,
		ErrorCount:   r.errorCount,
		InputBytes:   r.inputBytes,
		OutputChars:  r.outputChars,
		OutputBytes:  r.outputBytes,
		Images:       r.images,
	}
}

func parseDocumentOperationOutput(
	engine string,
	observation docReaderRequestSnapshot,
	success bool,
) types.JSONMap {
	computedItems := 0
	if success {
		computedItems = observation.RequestCount
	}
	return types.IngestionOperationObservation{
		Operation: types.IngestionOperationParseDocument,
		Stage:     types.StageDocReader,
		ModelID:   engine,
		ModelType: "document_parser",

		OperationCount: 1,
		RequestCount:   observation.RequestCount,
		BatchCount:     observation.RequestCount,
		TotalItems:     observation.RequestCount,
		ComputedItems:  computedItems,
		ReusedItems:    0,

		InputBytes:  observation.InputBytes,
		OutputChars: observation.OutputChars,
		OutputBytes: observation.OutputBytes,

		CacheStatus: types.IngestionCacheStatusNotSupported,
		Success:     success,
	}.ToJSONMap()
}
