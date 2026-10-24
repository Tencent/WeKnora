package session

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/application/service"
	"github.com/Tencent/WeKnora/internal/models/vlm"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

type inlineOCRDocumentReader struct {
	interfaces.DocumentReader
	result *types.ReadResult
}

func (r *inlineOCRDocumentReader) Read(context.Context, *types.ReadRequest) (*types.ReadResult, error) {
	return r.result, nil
}

type inlineOCRModelService struct {
	interfaces.ModelService
	model       vlm.VLM
	requestedID string
	calls       int
}

func (s *inlineOCRModelService) GetVLMModel(_ context.Context, modelID string) (vlm.VLM, error) {
	s.requestedID = modelID
	s.calls++
	return s.model, nil
}

type inlineOCRVLM struct {
	images   [][]byte
	prompt   string
	response string
	calls    int
}

func (m *inlineOCRVLM) Predict(_ context.Context, images [][]byte, prompt string) (string, error) {
	m.images = append(m.images, images...)
	m.prompt = prompt
	m.calls++
	return m.response, nil
}

func (*inlineOCRVLM) GetModelName() string { return "inline-ocr-test" }
func (*inlineOCRVLM) GetModelID() string   { return "inline-ocr-test" }

func TestProcessWithDocumentReaderRunsConfiguredOCRForScannedPDF(t *testing.T) {
	reader := &inlineOCRDocumentReader{result: &types.ReadResult{
		MarkdownContent: "![page_1.jpg](resource://page-1)\n![page_2.jpg](resource://page-2)",
		ImageRefs: []types.ImageRef{
			{Filename: "page_1.jpg", ImageData: []byte("page-one")},
			{Filename: "page_2.jpg", ImageData: []byte("page-two")},
		},
	}}
	vlmModel := &inlineOCRVLM{response: "第一页扫描文字内容"}
	modelService := &inlineOCRModelService{model: vlmModel}
	processor := &AttachmentProcessor{documentReader: reader, modelService: modelService}
	attachment := &types.MessageAttachment{}
	options := service.AttachmentImageUnderstandingOptions{
		VLMModelID:         "agent-vlm",
		ImageUnderstanding: true,
		OCRMaxPages:        1,
	}

	err := processor.processWithDocumentReader(
		context.Background(), []byte("pdf"), "scan.pdf", ".pdf", attachment, 7, options,
	)
	require.NoError(t, err)
	require.Contains(t, attachment.Content, "第一页扫描文字内容")
	require.Contains(t, attachment.Content, "resource://page-1")
	require.Equal(t, "agent-vlm", modelService.requestedID)
	require.Equal(t, 1, modelService.calls)
	require.Equal(t, 1, vlmModel.calls)
	require.Equal(t, [][]byte{[]byte("page-one")}, vlmModel.images)
	require.Contains(t, vlmModel.prompt, "page from a scanned PDF document")
}

func TestProcessWithDocumentReaderKeepsScannedPDFOCROptIn(t *testing.T) {
	const markdown = "![page_1.jpg](resource://page-1)"
	reader := &inlineOCRDocumentReader{result: &types.ReadResult{
		MarkdownContent: markdown,
		ImageRefs:       []types.ImageRef{{Filename: "page_1.jpg", ImageData: []byte("page-one")}},
	}}
	vlmModel := &inlineOCRVLM{response: "unexpected OCR"}
	modelService := &inlineOCRModelService{model: vlmModel}
	processor := &AttachmentProcessor{documentReader: reader, modelService: modelService}
	attachment := &types.MessageAttachment{}

	err := processor.processWithDocumentReader(
		context.Background(), []byte("pdf"), "scan.pdf", ".pdf", attachment, 7,
		service.AttachmentImageUnderstandingOptions{VLMModelID: "agent-vlm"},
	)
	require.NoError(t, err)
	require.Equal(t, markdown, attachment.Content)
	require.Zero(t, modelService.calls)
	require.Zero(t, vlmModel.calls)
}

func TestProcessWithDocumentReaderDoesNotRunScannedPDFOCRForOtherFormats(t *testing.T) {
	const markdown = "![page_1.jpg](resource://page-1)"
	reader := &inlineOCRDocumentReader{result: &types.ReadResult{
		MarkdownContent: markdown,
		ImageRefs:       []types.ImageRef{{Filename: "page_1.jpg", ImageData: []byte("page-one")}},
	}}
	vlmModel := &inlineOCRVLM{response: "unexpected OCR"}
	modelService := &inlineOCRModelService{model: vlmModel}
	processor := &AttachmentProcessor{documentReader: reader, modelService: modelService}
	attachment := &types.MessageAttachment{}

	err := processor.processWithDocumentReader(
		context.Background(), []byte("docx"), "scan.docx", ".docx", attachment, 7,
		service.AttachmentImageUnderstandingOptions{
			VLMModelID:         "agent-vlm",
			ImageUnderstanding: true,
		},
	)
	require.NoError(t, err)
	require.Equal(t, markdown, attachment.Content)
	require.Zero(t, modelService.calls)
	require.Zero(t, vlmModel.calls)
}
