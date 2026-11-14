package session

import (
	"context"
	"sync"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/vlm"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// scannedPDFReader returns what the docreader yields for a scanned PDF: no
// text layer, only page images.
type scannedPDFReader struct {
	interfaces.DocumentReader
}

func (scannedPDFReader) Read(context.Context, *types.ReadRequest) (*types.ReadResult, error) {
	return &types.ReadResult{
		MarkdownContent: "![page 1](images/page-1.png)",
		ImageRefs: []types.ImageRef{
			{Filename: "page-1.png", OriginalRef: "images/page-1.png", ImageData: []byte("page-1")},
		},
	}, nil
}

type attachmentFileServiceStub struct {
	interfaces.FileService
}

func (attachmentFileServiceStub) SaveBytes(context.Context, []byte, uint64, string, bool) (string, error) {
	return "local://attachments/scan.pdf", nil
}

type attachmentOCRVLMStub struct {
	mu    sync.Mutex
	calls int
}

func (s *attachmentOCRVLMStub) Predict(context.Context, [][]byte, string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	return "扫描件第一页的文字", nil
}

func (s *attachmentOCRVLMStub) PredictWithOptions(
	ctx context.Context, img [][]byte, prompt string, _ *vlm.PredictOptions,
) (string, error) {
	return s.Predict(ctx, img, prompt)
}

func (s *attachmentOCRVLMStub) GetModelName() string { return "vlm" }
func (s *attachmentOCRVLMStub) GetModelID() string   { return "vlm-1" }

type attachmentVLMModelServiceStub struct {
	interfaces.ModelService
	vlm vlm.VLM
}

func (s *attachmentVLMModelServiceStub) GetVLMModel(context.Context, string) (vlm.VLM, error) {
	return s.vlm, nil
}

func TestProcessAttachmentScannedPDFUsesVLMOCR(t *testing.T) {
	stub := &attachmentOCRVLMStub{}
	p := NewAttachmentProcessor(attachmentFileServiceStub{}, scannedPDFReader{}, nil,
		&attachmentVLMModelServiceStub{vlm: stub})

	attachment, err := p.ProcessAttachment(context.Background(), []byte("%PDF-1.7"), "scan.pdf", 8, 1,
		AttachmentProcessOptions{VLMModelID: "vlm-1", ScannedDocumentOCR: true})
	require.NoError(t, err)

	assert.Contains(t, attachment.Content, "扫描件第一页的文字")
	assert.Equal(t, 1, stub.calls)
}

func TestProcessAttachmentScannedPDFWithoutOCROptIn(t *testing.T) {
	stub := &attachmentOCRVLMStub{}
	p := NewAttachmentProcessor(attachmentFileServiceStub{}, scannedPDFReader{}, nil,
		&attachmentVLMModelServiceStub{vlm: stub})

	attachment, err := p.ProcessAttachment(context.Background(), []byte("%PDF-1.7"), "scan.pdf", 8, 1,
		AttachmentProcessOptions{VLMModelID: "vlm-1"})
	require.NoError(t, err)

	assert.Equal(t, "![page 1](images/page-1.png)", attachment.Content)
	assert.Zero(t, stub.calls)
}
