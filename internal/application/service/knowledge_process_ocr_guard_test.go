package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/infrastructure/docparser"
	"github.com/Tencent/WeKnora/internal/models/embedding"
	"github.com/Tencent/WeKnora/internal/models/vlm"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/hibiken/asynq"
	"github.com/stretchr/testify/require"
)

type ocrGuardRepo struct {
	interfaces.KnowledgeRepository
	row     *types.Knowledge
	fail    error
	updates int
}

func (r *ocrGuardRepo) GetKnowledgeByID(context.Context, uint64, string) (*types.Knowledge, error) {
	return r.row, nil
}
func (r *ocrGuardRepo) UpdateKnowledge(_ context.Context, k *types.Knowledge) error {
	r.updates++
	if r.fail != nil {
		return r.fail
	}
	copy := *k
	r.row = &copy
	return nil
}

type ocrGuardReader struct {
	interfaces.DocumentReader
	result *types.ReadResult
	calls  int
}

func (r *ocrGuardReader) IsConnected() bool { return true }
func (r *ocrGuardReader) Read(context.Context, *types.ReadRequest) (*types.ReadResult, error) {
	r.calls++
	return r.result, nil
}

type ocrGuardFile struct{ interfaces.FileService }

func (ocrGuardFile) GetFile(context.Context, string) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader("PDF fixture")), nil
}

type ocrGuardTenantRepo struct{ interfaces.TenantRepository }

func (ocrGuardTenantRepo) GetTenantByID(context.Context, uint64) (*types.Tenant, error) {
	return &types.Tenant{ID: 1}, nil
}

type ocrGuardKBService struct {
	interfaces.KnowledgeBaseService
	kb *types.KnowledgeBase
}

func (s ocrGuardKBService) GetKnowledgeBaseByID(context.Context, string) (*types.KnowledgeBase, error) {
	return s.kb, nil
}

type ocrGuardTenantService struct{ interfaces.TenantService }

func (ocrGuardTenantService) GetWeKnoraCloudCredentials(context.Context) *types.WeKnoraCloudCredentials {
	return nil
}

type ocrGuardChunks struct {
	interfaces.ChunkRepository
	calls int
}

func (s *ocrGuardChunks) DeleteChunksByKnowledgeID(context.Context, uint64, string) error {
	s.calls++
	return errors.New("unexpected downstream chunk processing")
}

type ocrGuardModel struct {
	interfaces.ModelService
	embedCalls int
	err        error
	model      vlm.VLM
}

func (s *ocrGuardModel) GetEmbeddingModel(context.Context, string) (embedding.Embedder, error) {
	s.embedCalls++
	return nil, errors.New("unexpected downstream embedding")
}
func (s *ocrGuardModel) GetVLMModel(context.Context, string) (vlm.VLM, error) { return s.model, s.err }

type ocrGuardVLM struct{}

func (ocrGuardVLM) Predict(context.Context, [][]byte, string) (string, error) {
	return "", errors.New("must not perform model request")
}
func (ocrGuardVLM) GetModelName() string { return "configured-only" }
func (ocrGuardVLM) GetModelID() string   { return "vlm" }

type ocrGuardTask struct{ calls int }

func (s *ocrGuardTask) Enqueue(*asynq.Task, ...asynq.Option) (*asynq.TaskInfo, error) {
	s.calls++
	return nil, errors.New("unexpected enqueue")
}

// Exercise the real document orchestrator, not only the predicate. Its existing
// parser boundary returns a real ReadResult; chunk/index/enqueue spies must stay
// untouched for both pure scan and mixed PDF capability failures.
func TestScannedPDFProcessDocumentStopsBeforeChunks(t *testing.T) {
	for _, tc := range []struct {
		name        string
		meta        map[string]string
		failPersist bool
	}{
		{"scan", map[string]string{"scanned_page_count": "1", "image_source_type": "scanned_pdf"}, false},
		{"mixed", map[string]string{"scanned_page_count": "1", "text_page_count": "1"}, false},
		{"malformed", map[string]string{"scanned_page_count": ""}, false},
		{"persist error", map[string]string{"scanned_page_count": "1"}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			k := &types.Knowledge{ID: "k", TenantID: 1, KnowledgeBaseID: "kb", FilePath: "fixture.pdf", FileType: "pdf", ParseStatus: types.ParseStatusPending}
			repo := &ocrGuardRepo{row: k}
			reader := &ocrGuardReader{result: &types.ReadResult{MarkdownContent: "native text\n![scan_page_1.jpg](scan_page_1.jpg)", Metadata: tc.meta}}
			chunks := &ocrGuardChunks{}
			models := &ocrGuardModel{}
			task := &ocrGuardTask{}
			kb := &types.KnowledgeBase{ID: "kb", TenantID: 1}
			s := &knowledgeService{repo: repo, documentReader: reader, fileSvc: ocrGuardFile{}, tenantRepo: ocrGuardTenantRepo{}, kbService: ocrGuardKBService{kb: kb}, tenantService: ocrGuardTenantService{}, chunkRepo: chunks, modelService: models, task: task, spanTracker: noopSpanTracker{}}
			// Trigger the state-write fault after the parser is called, avoiding a
			// superficial failure at the orchestrator's initial processing update.
			if tc.failPersist {
				reader.result.Error = ""
				s.documentReader = &ocrGuardFailureReader{ocrGuardReader: reader, repo: repo}
			}
			body, _ := json.Marshal(types.DocumentProcessPayload{TenantID: 1, KnowledgeID: "k", KnowledgeBaseID: "kb", FilePath: "fixture.pdf", FileType: "pdf", FileName: "fixture.pdf"})
			err := func() (callErr error) {
				defer func() {
					if r := recover(); r != nil {
						callErr = fmt.Errorf("unexpected downstream panic: %v", r)
					}
				}()
				return s.ProcessDocument(context.Background(), asynq.NewTask(types.TypeDocumentProcess, body))
			}()
			require.Error(t, err)
			require.Equal(t, 1, reader.calls)
			require.Zero(t, chunks.calls)
			require.Zero(t, models.embedCalls)
			require.Zero(t, task.calls)
			require.Equal(t, "native text\n![scan_page_1.jpg](scan_page_1.jpg)", reader.result.MarkdownContent, "do not erase image placeholders to fabricate successful text")
			if tc.failPersist {
				require.ErrorContains(t, err, "persist scanned PDF")
			} else {
				require.Equal(t, types.ParseStatusFailed, repo.row.ParseStatus)
				require.NotEmpty(t, repo.row.ErrorMessage)
			}
		})
	}
}

type ocrGuardFailureReader struct {
	*ocrGuardReader
	repo *ocrGuardRepo
}

func (r *ocrGuardFailureReader) Read(ctx context.Context, req *types.ReadRequest) (*types.ReadResult, error) {
	result, err := r.ocrGuardReader.Read(ctx, req)
	r.repo.fail = errors.New("state write failed")
	return result, err
}

func TestScannedPDFConvertPreservesNativeText(t *testing.T) {
	k := &types.Knowledge{ID: "native", TenantID: 1, FilePath: "native.pdf", ParseStatus: types.ParseStatusProcessing}
	reader := &ocrGuardReader{result: &types.ReadResult{MarkdownContent: "real native text", Metadata: map[string]string{"scanned_page_count": "0", "image_source_type": "pdf_text_layer"}}}
	s := &knowledgeService{repo: &ocrGuardRepo{row: k}, documentReader: reader, fileSvc: ocrGuardFile{}, tenantService: ocrGuardTenantService{}}
	result, err := s.convert(context.Background(), types.DocumentProcessPayload{FilePath: "native.pdf", FileType: "pdf"}, &types.KnowledgeBase{ID: "kb", TenantID: 1}, k, types.EffectiveProcessConfig{}, true)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, "real native text", result.MarkdownContent)
	require.Equal(t, types.ParseStatusProcessing, k.ParseStatus)
}

func TestScannedPDFConvertNilResultFailsWithoutPanic(t *testing.T) {
	k := &types.Knowledge{ID: "nil", TenantID: 1, FilePath: "nil.pdf", ParseStatus: types.ParseStatusProcessing}
	repo := &ocrGuardRepo{row: k}
	s := &knowledgeService{repo: repo, documentReader: &ocrGuardReader{}, fileSvc: ocrGuardFile{}, tenantService: ocrGuardTenantService{}}
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("nil parser result panic: %v", r)
		}
	}()
	result, err := s.convert(context.Background(), types.DocumentProcessPayload{FilePath: "nil.pdf", FileType: "pdf"}, &types.KnowledgeBase{ID: "kb", TenantID: 1}, k, types.EffectiveProcessConfig{}, true)
	require.Nil(t, result)
	require.Error(t, err)
	require.Equal(t, types.ParseStatusFailed, repo.row.ParseStatus)
}
func TestScannedPDFParserErrorHasPriority(t *testing.T) {
	k := &types.Knowledge{ID: "parser", TenantID: 1, FilePath: "parser.pdf", ParseStatus: types.ParseStatusProcessing}
	repo := &ocrGuardRepo{row: k}
	reader := &ocrGuardReader{result: &types.ReadResult{Error: "original parser failure", Metadata: map[string]string{"scanned_page_count": "invalid"}}}
	s := &knowledgeService{repo: repo, documentReader: reader, fileSvc: ocrGuardFile{}, tenantService: ocrGuardTenantService{}}
	result, err := s.convert(context.Background(), types.DocumentProcessPayload{FilePath: "parser.pdf", FileType: "pdf"}, &types.KnowledgeBase{ID: "kb", TenantID: 1}, k, types.EffectiveProcessConfig{}, true)
	require.Nil(t, result)
	require.NoError(t, err)
	require.Equal(t, "original parser failure", repo.row.ErrorMessage)
}

func TestScannedPDFRequirement(t *testing.T) {
	for _, tc := range []struct {
		name              string
		meta              map[string]string
		required, wantErr bool
	}{
		{"native", map[string]string{"scanned_page_count": "0", "image_source_type": "pdf_text_layer"}, false, false},
		{"plain text", nil, false, false},
		{"scan", map[string]string{"scanned_page_count": "1", "image_source_type": "scanned_pdf"}, true, false},
		{"mixed", map[string]string{"scanned_page_count": "1", "text_page_count": "2"}, true, false},
		{"empty count", map[string]string{"scanned_page_count": ""}, false, true},
		{"bad count", map[string]string{"scanned_page_count": "NaN"}, false, true},
		{"negative", map[string]string{"scanned_page_count": "-1"}, false, true},
		{"missing", map[string]string{"image_source_type": "scanned_pdf"}, false, true},
		{"inconsistent", map[string]string{"scanned_page_count": "0", "image_source_type": "scanned_pdf"}, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			needed, err := scannedPDFRequirement(&types.ReadResult{MarkdownContent: "native text\n![scan](scan.jpg)", Metadata: tc.meta})
			require.Equal(t, tc.required, needed)
			require.Equal(t, tc.wantErr, err != nil)
		})
	}
}

func TestScannedPDFGuardConfiguredCapabilityOnly(t *testing.T) {
	for _, tc := range []struct {
		name     string
		resolver bool
		model    vlm.VLM
		modelErr error
		wantErr  bool
	}{
		{"resolvable", true, ocrGuardVLM{}, nil, false},
		{"nil resolver", false, ocrGuardVLM{}, nil, true},
		{"missing model", true, nil, errors.New("missing row"), true},
		{"nil model", true, nil, nil, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			k := &types.Knowledge{ID: "k", TenantID: 1, ParseStatus: types.ParseStatusProcessing}
			repo := &ocrGuardRepo{row: k}
			s := &knowledgeService{repo: repo, task: &ocrGuardTask{}, modelService: &ocrGuardModel{model: tc.model, err: tc.modelErr}}
			if tc.resolver {
				s.imageResolver = &docparser.ImageResolver{}
			}
			err := s.guardScannedPDFOCR(context.Background(), types.DocumentProcessPayload{EnableMultimodel: true}, k, types.EffectiveProcessConfig{EnableMultimodel: true, VLMConfig: types.VLMConfig{Enabled: true, ModelID: "vlm"}}, &types.ReadResult{Metadata: map[string]string{"scanned_page_count": "1"}})
			require.Equal(t, tc.wantErr, err != nil)
			if tc.wantErr {
				require.Equal(t, types.ParseStatusFailed, repo.row.ParseStatus)
			} else {
				require.Zero(t, repo.updates)
			}
		})
	}
}

func TestScannedPDFReferenceWithoutMetadataFailsClosed(t *testing.T) {
	for _, meta := range []map[string]string{nil, {"scanned_page_count": "0"}} {
		needed, err := scannedPDFRequirement(&types.ReadResult{ImageRefs: []types.ImageRef{{Filename: "scan_page_1.jpg"}}, Metadata: meta})
		require.False(t, needed)
		require.Error(t, err)
	}
}

func TestScannedPDFLegacyConfigurationValidation(t *testing.T) {
	for _, tc := range []struct {
		url, iface string
		wantError  bool
	}{
		{"http://127.0.0.1:8089", "ollama", false},
		{"http://127.0.0.1:8089/v1", "openai", false},
		{"not-a-url", "openai", true},
		{"http://127.0.0.1:8089", "unknown", true},
	} {
		k := &types.Knowledge{ID: "k", TenantID: 1, ParseStatus: types.ParseStatusProcessing}
		repo := &ocrGuardRepo{row: k}
		s := &knowledgeService{repo: repo, imageResolver: &docparser.ImageResolver{}, task: &ocrGuardTask{}}
		err := s.guardScannedPDFOCR(context.Background(), types.DocumentProcessPayload{EnableMultimodel: true}, k, types.EffectiveProcessConfig{EnableMultimodel: true, VLMConfig: types.VLMConfig{ModelName: "explicit", BaseURL: tc.url, InterfaceType: tc.iface}}, &types.ReadResult{Metadata: map[string]string{"scanned_page_count": "1"}})
		require.Equal(t, tc.wantError, err != nil)
	}
}

func TestScannedPDFLegacyPageCountAndExplicitPriority(t *testing.T) {
	for _, tc := range []struct {
		name      string
		meta      map[string]string
		needed    bool
		wantError bool
		source    string
	}{
		{"legacy", map[string]string{"image_source_type": "scanned_pdf", "page_count": "2"}, true, false, "derived_page_count"},
		{"legacy zero", map[string]string{"image_source_type": "scanned_pdf", "page_count": "0"}, false, true, ""},
		{"legacy invalid", map[string]string{"image_source_type": "scanned_pdf", "page_count": "+2"}, false, true, ""},
		{"explicit zero cannot bypass", map[string]string{"image_source_type": "scanned_pdf", "page_count": "2", "scanned_page_count": "0"}, false, true, ""},
		{"explicit invalid cannot fallback", map[string]string{"image_source_type": "scanned_pdf", "page_count": "2", "scanned_page_count": ""}, false, true, ""},
		{"native zero with total pages", map[string]string{"image_source_type": "pdf_text_layer", "page_count": "2", "scanned_page_count": "0"}, false, false, "explicit_scanned_page_count"},
		{"new mixed", map[string]string{"image_source_type": "scanned_pdf", "page_count": "3", "scanned_page_count": "1"}, true, false, "explicit_scanned_page_count"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := &types.ReadResult{Metadata: tc.meta}
			needed, err := scannedPDFRequirement(r)
			require.Equal(t, tc.needed, needed)
			require.Equal(t, tc.wantError, err != nil)
			require.Equal(t, tc.source, r.Metadata["scanned_page_count_source"])
		})
	}
}
func TestScannedPDFMissingTaskAndModelService(t *testing.T) {
	for _, name := range []string{"task", "model service"} {
		t.Run(name, func(t *testing.T) {
			k := &types.Knowledge{ID: "k", TenantID: 1, ParseStatus: types.ParseStatusProcessing}
			repo := &ocrGuardRepo{row: k}
			models := &ocrGuardModel{model: ocrGuardVLM{}}
			tasks := &ocrGuardTask{}
			s := &knowledgeService{repo: repo, imageResolver: &docparser.ImageResolver{}, modelService: models, task: tasks}
			if name == "task" {
				s.task = nil
			} else {
				s.modelService = nil
			}
			err := s.guardScannedPDFOCR(context.Background(), types.DocumentProcessPayload{EnableMultimodel: true}, k, types.EffectiveProcessConfig{EnableMultimodel: true, VLMConfig: types.VLMConfig{Enabled: true, ModelID: "vlm"}}, &types.ReadResult{Metadata: map[string]string{"scanned_page_count": "1"}})
			require.Error(t, err)
			require.Equal(t, types.ParseStatusFailed, repo.row.ParseStatus)
			require.Zero(t, models.embedCalls)
			require.Zero(t, tasks.calls)
		})
	}
}

func TestScannedPDFProcessConfiguredMissingPipeline(t *testing.T) {
	for _, missing := range []string{"resolver", "task", "model service"} {
		t.Run(missing, func(t *testing.T) {
			k := &types.Knowledge{ID: "k", TenantID: 1, KnowledgeBaseID: "kb", FilePath: "fixture.pdf", FileType: "pdf", ParseStatus: types.ParseStatusPending}
			repo := &ocrGuardRepo{row: k}
			reader := &ocrGuardReader{result: &types.ReadResult{MarkdownContent: "![scan](scan.jpg)", Metadata: map[string]string{"scanned_page_count": "1", "image_source_type": "scanned_pdf"}}}
			chunks := &ocrGuardChunks{}
			models := &ocrGuardModel{model: ocrGuardVLM{}}
			tasks := &ocrGuardTask{}
			kb := &types.KnowledgeBase{ID: "kb", TenantID: 1, VLMConfig: types.VLMConfig{Enabled: true, ModelID: "vlm"}}
			s := &knowledgeService{repo: repo, documentReader: reader, fileSvc: ocrGuardFile{}, tenantRepo: ocrGuardTenantRepo{}, kbService: ocrGuardKBService{kb: kb}, tenantService: ocrGuardTenantService{}, chunkRepo: chunks, modelService: models, task: tasks, imageResolver: &docparser.ImageResolver{}, spanTracker: noopSpanTracker{}}
			switch missing {
			case "resolver":
				s.imageResolver = nil
			case "task":
				s.task = nil
			case "model service":
				s.modelService = nil
			}
			body, err := json.Marshal(types.DocumentProcessPayload{TenantID: 1, KnowledgeID: "k", KnowledgeBaseID: "kb", FilePath: "fixture.pdf", FileType: "pdf", FileName: "fixture.pdf", EnableMultimodel: true})
			require.NoError(t, err)
			err = s.ProcessDocument(context.Background(), asynq.NewTask(types.TypeDocumentProcess, body))
			require.Error(t, err)
			require.Equal(t, 1, reader.calls)
			require.Zero(t, chunks.calls)
			require.Zero(t, models.embedCalls)
			require.Zero(t, tasks.calls)
			require.Equal(t, types.ParseStatusFailed, repo.row.ParseStatus)
			require.NotEmpty(t, repo.row.ErrorMessage)
		})
	}
}
func TestScannedPDFLegacyConfiguredNotRuntimeReady(t *testing.T) {
	k := &types.Knowledge{ID: "k", TenantID: 1, ParseStatus: types.ParseStatusProcessing}
	repo := &ocrGuardRepo{row: k}
	tasks := &ocrGuardTask{}
	s := &knowledgeService{repo: repo, imageResolver: &docparser.ImageResolver{}, task: tasks}
	result := &types.ReadResult{Metadata: map[string]string{"image_source_type": "scanned_pdf", "page_count": "2"}}
	err := s.guardScannedPDFOCR(context.Background(), types.DocumentProcessPayload{EnableMultimodel: true}, k, types.EffectiveProcessConfig{EnableMultimodel: true, VLMConfig: types.VLMConfig{ModelName: "explicit", BaseURL: "http://127.0.0.1:8089/v1", InterfaceType: "openai"}}, result)
	require.NoError(t, err)
	require.Equal(t, "derived_page_count", result.Metadata["scanned_page_count_source"])
	require.Zero(t, repo.updates)
	require.Zero(t, tasks.calls)
}
