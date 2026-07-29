package modelcount

import (
	"context"
	"errors"
	"sync"

	"github.com/Tencent/WeKnora/internal/models/vlm"
	"github.com/Tencent/WeKnora/internal/types"
)

// VLMCall records one call that entered CountingVLM.Predict.
//
// Prompts and image bytes are deliberately not retained. Tests only need the
// operation and input size to verify that a real model-interface call occurred.
type VLMCall struct {
	Operation  types.IngestionOperation
	ImageCount int
	InputBytes int64
}

// VLMSnapshot is an immutable copy of the calls observed by CountingVLM.
//
// Snapshot values may be safely read while other goroutines continue calling
// Predict.
type VLMSnapshot struct {
	PredictRequestCount int
	OCRRequestCount     int
	CaptionRequestCount int
	InputImageCount     int
	InputBytes          int64
	Calls               []VLMCall
}

// CountingVLM is a thread-safe test implementation of the VLM interface.
//
// It records calls entering Predict and returns configured responses without
// contacting an external model provider.
type CountingVLM struct {
	mu sync.Mutex

	modelID   string
	modelName string

	ocrResponse     string
	captionResponse string
	defaultResponse string

	ocrError     error
	captionError error
	defaultError error

	failOnRequest int
	failError     error

	predictRequestCount int
	ocrRequestCount     int
	captionRequestCount int
	inputImageCount     int
	inputBytes          int64
	calls               []VLMCall
}

var _ vlm.VLM = (*CountingVLM)(nil)

// CountingVLMOptions configures a CountingVLM.
type CountingVLMOptions struct {
	ModelID   string
	ModelName string

	OCRResponse     string
	CaptionResponse string
	DefaultResponse string

	OCRError     error
	CaptionError error
	DefaultError error

	// FailOnRequest makes the Nth Predict call fail. Zero disables it.
	FailOnRequest int
	FailError     error
}

// NewCountingVLM creates a thread-safe test VLM.
func NewCountingVLM(options CountingVLMOptions) *CountingVLM {
	modelID := options.ModelID
	if modelID == "" {
		modelID = "counting-vlm"
	}

	modelName := options.ModelName
	if modelName == "" {
		modelName = "counting-vlm"
	}

	failError := options.FailError
	if failError == nil {
		failError = errors.New("counting VLM configured failure")
	}

	return &CountingVLM{
		modelID:         modelID,
		modelName:       modelName,
		ocrResponse:     options.OCRResponse,
		captionResponse: options.CaptionResponse,
		defaultResponse: options.DefaultResponse,
		ocrError:        options.OCRError,
		captionError:    options.CaptionError,
		defaultError:    options.DefaultError,
		failOnRequest:   options.FailOnRequest,
		failError:       failError,
	}
}

// Predict records one model-interface request and returns the configured
// response for the operation carried by ctx.
func (c *CountingVLM) Predict(
	ctx context.Context,
	images [][]byte,
	_ string,
) (string, error) {
	operation := types.IngestionOperationFromContext(ctx)

	var inputBytes int64
	for _, image := range images {
		inputBytes += int64(len(image))
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	c.predictRequestCount++
	requestNumber := c.predictRequestCount
	c.inputImageCount += len(images)
	c.inputBytes += inputBytes

	call := VLMCall{
		Operation:  operation,
		ImageCount: len(images),
		InputBytes: inputBytes,
	}
	c.calls = append(c.calls, call)

	switch operation {
	case types.IngestionOperationMultimodalOCR:
		c.ocrRequestCount++
	case types.IngestionOperationMultimodalCaption:
		c.captionRequestCount++
	}

	if c.failOnRequest > 0 && requestNumber == c.failOnRequest {
		return "", c.failError
	}

	switch operation {
	case types.IngestionOperationMultimodalOCR:
		return c.ocrResponse, c.ocrError

	case types.IngestionOperationMultimodalCaption:
		return c.captionResponse, c.captionError

	default:
		return c.defaultResponse, c.defaultError
	}
}

// GetModelName returns the test model name.
func (c *CountingVLM) GetModelName() string {
	return c.modelName
}

// GetModelID returns the test model ID.
func (c *CountingVLM) GetModelID() string {
	return c.modelID
}

// Snapshot returns an immutable copy of the current counters.
func (c *CountingVLM) Snapshot() VLMSnapshot {
	c.mu.Lock()
	defer c.mu.Unlock()

	calls := make([]VLMCall, len(c.calls))
	copy(calls, c.calls)

	return VLMSnapshot{
		PredictRequestCount: c.predictRequestCount,
		OCRRequestCount:     c.ocrRequestCount,
		CaptionRequestCount: c.captionRequestCount,
		InputImageCount:     c.inputImageCount,
		InputBytes:          c.inputBytes,
		Calls:               calls,
	}
}
