package service

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/hibiken/asynq"
	"github.com/stretchr/testify/require"
)

type parserResultRepo struct {
	interfaces.TemporaryDocumentRepository
	document      *types.TemporaryDocument
	readyCalls    int
	failedMessage string
}

func (r *parserResultRepo) GetByID(context.Context, uint64, string) (*types.TemporaryDocument, error) {
	return r.document, nil
}

func (r *parserResultRepo) MarkProcessing(context.Context, uint64, string, time.Time) error {
	return nil
}

func (r *parserResultRepo) MarkReady(
	context.Context, uint64, string, string, types.JSON, types.JSON, types.JSON, int, int, time.Time,
) error {
	r.readyCalls++
	return nil
}

func (r *parserResultRepo) MarkFailed(_ context.Context, _ uint64, _ string, message string) error {
	r.failedMessage = message
	return nil
}

type parserResultFiles struct {
	interfaces.FileService
}

func (parserResultFiles) GetFile(context.Context, string) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader("document bytes")), nil
}

type parserResultTenants struct {
	interfaces.TenantService
}

func (parserResultTenants) GetTenantByID(context.Context, uint64) (*types.Tenant, error) {
	return nil, nil
}

type parserResultReader struct {
	interfaces.DocumentReader
}

func (parserResultReader) Read(context.Context, *types.ReadRequest) (*types.ReadResult, error) {
	return &types.ReadResult{Error: "parser rejected document"}, nil
}

func TestTemporaryDocumentParserResultErrorMarksDocumentFailed(t *testing.T) {
	options, err := json.Marshal(types.TemporaryDocumentCreateOptions{ParserEngine: "builtin"})
	require.NoError(t, err)
	repo := &parserResultRepo{document: &types.TemporaryDocument{
		ID: "document-1", TenantID: 7, FileName: "document.pdf", FileType: ".pdf",
		ResourceRef: "document.pdf", ProcessingOptions: types.JSON(options),
	}}
	payload, err := json.Marshal(types.TemporaryDocumentTaskPayload{TenantID: 7, DocumentID: "document-1"})
	require.NoError(t, err)
	svc := &temporaryDocumentService{
		repo: repo, fileService: parserResultFiles{}, documentReader: parserResultReader{},
		tenantService: parserResultTenants{},
	}

	// Lite mode has no Asynq retry metadata, so a saved failure is terminal.
	err = svc.Process(t.Context(), asynq.NewTask("parse", payload))

	require.NoError(t, err)
	require.Zero(t, repo.readyCalls)
	require.Contains(t, repo.failedMessage, "parser rejected document")
}
