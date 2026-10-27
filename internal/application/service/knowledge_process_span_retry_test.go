package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"testing/iotest"

	werrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/infrastructure/docparser"
	"github.com/Tencent/WeKnora/internal/infrastructure/docparser/anydoc"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

type spanRetryDocReader struct {
	interfaces.DocumentReader
	result *types.ReadResult
	err    error
}

func (r *spanRetryDocReader) Read(context.Context, *types.ReadRequest) (*types.ReadResult, error) {
	return r.result, r.err
}

type spanRetryFileService struct {
	interfaces.FileService
	getErr, readErr error
}

func (s spanRetryFileService) GetFile(context.Context, string) (io.ReadCloser, error) {
	if s.getErr != nil {
		return nil, s.getErr
	}
	if s.readErr != nil {
		return io.NopCloser(iotest.ErrReader(s.readErr)), nil
	}
	return io.NopCloser(strings.NewReader("document")), nil
}

// Exercise convert's actual return value: a parser response with Error is
// acknowledged, whereas transport/file errors are returned for queue retry.
func TestConvertFailureSpanMatchesTaskOutcome(t *testing.T) {
	transient := errors.New("temporary read failure")
	for _, tc := range []struct {
		name                    string
		missingReader           bool
		parserError             string
		getErr, readErr, rpcErr error
		retryable               bool
		code                    string
	}{
		{name: "reader unavailable", missingReader: true, code: werrors.ErrCodeDocReaderUnavailable},
		{
			name: "parser rejects document", parserError: "unsupported document",
			code: werrors.ErrCodeDocReaderParseFailed,
		},
		{name: "file fetch fails", getErr: transient, retryable: true, code: werrors.ErrCodeDocReaderParseFailed},
		{name: "file read fails", readErr: transient, retryable: true, code: werrors.ErrCodeDocReaderParseFailed},
		{name: "reader call fails", rpcErr: transient, retryable: true, code: werrors.ErrCodeDocReaderParseFailed},
		{
			name: "CSV expansion budget exceeded", rpcErr: docparser.ErrCSVMarkdownTooLarge,
			code: werrors.ErrCodeDocReaderParseFailed,
		},
	} {
		for _, retried := range []int{0, 3} {
			name := tc.name + "/retry_available"
			if retried == 3 {
				name = tc.name + "/last_retry"
			}
			t.Run(name, func(t *testing.T) {
				tracker, db := setupSpanTrackerTest(t)
				ctx := types.WithTaskRetryMetadata(context.Background(), retried, 3)
				knowledge := &types.Knowledge{
					ID: "doc", TenantID: 1, KnowledgeBaseID: "kb", FilePath: "doc.pdf",
					ParseStatus: types.ParseStatusProcessing,
				}
				_, attempt, err := tracker.OpenAttempt(ctx, knowledge.ID, "")
				require.NoError(t, err)
				ctx = withAttempt(ctx, attempt)
				svc := &knowledgeService{
					repo:          &embedFailureKnowledgeRepo{knowledge: knowledge},
					spanTracker:   tracker,
					tenantService: &stubTenantServiceForModelDelete{},
					fileSvc:       spanRetryFileService{getErr: tc.getErr, readErr: tc.readErr},
				}
				if !tc.missingReader {
					svc.documentReader = &spanRetryDocReader{
						result: &types.ReadResult{Error: tc.parserError}, err: tc.rpcErr,
					}
				}
				result, err := svc.convert(ctx, types.DocumentProcessPayload{
					FilePath: "doc.pdf", FileName: "doc.pdf", FileType: "pdf",
				}, &types.KnowledgeBase{ID: "kb"}, knowledge, types.EffectiveProcessConfig{}, retried == 3)
				require.Nil(t, result)
				if tc.retryable {
					require.Error(t, err)
				} else {
					require.NoError(t, err)
				}
				status, code, message := rootSpan(t, db, knowledge.ID, attempt)
				if tc.retryable && retried < 3 {
					require.Equal(t, types.ParseStatusProcessing, knowledge.ParseStatus)
					require.Equal(t, types.SpanStatusRunning, status)
					require.Empty(t, code)
					require.Empty(t, message)
				} else {
					require.Equal(t, types.ParseStatusFailed, knowledge.ParseStatus)
					require.Equal(t, types.SpanStatusFailed, status)
					require.Equal(t, tc.code, code)
					require.NotEmpty(t, message)
				}
			})
		}
	}
}

// An anydoc resource_limit is a property of the file. Retries burn memory and
// still fail, and the knowledge row must say which parser limit was hit
// instead of sending the user to DocReader logs (anydoc runs in-process).
func TestConvertAnydocResourceLimitIsPermanent(t *testing.T) {
	const limitDetail = "resource limit exceeded (max_xml_nodes): part exceeds 2000000 xml nodes"
	rpcErr := fmt.Errorf("anydoc conversion failed for %q: %w: %s",
		"xlsx50k.xlsx", anydoc.ErrResourceLimit, limitDetail)

	tracker, db := setupSpanTrackerTest(t)
	// Retries are still available. A permanent rejection must not use them.
	ctx := types.WithTaskRetryMetadata(context.Background(), 0, 3)
	knowledge := &types.Knowledge{
		ID: "doc", TenantID: 1, KnowledgeBaseID: "kb", FilePath: "xlsx50k.xlsx",
		ParseStatus: types.ParseStatusProcessing,
	}
	_, attempt, err := tracker.OpenAttempt(ctx, knowledge.ID, "")
	require.NoError(t, err)
	ctx = withAttempt(ctx, attempt)
	svc := &knowledgeService{
		repo:           &embedFailureKnowledgeRepo{knowledge: knowledge},
		spanTracker:    tracker,
		tenantService:  &stubTenantServiceForModelDelete{},
		fileSvc:        spanRetryFileService{},
		documentReader: &spanRetryDocReader{err: rpcErr},
	}
	result, err := svc.convert(ctx, types.DocumentProcessPayload{
		FilePath: "xlsx50k.xlsx", FileName: "xlsx50k.xlsx", FileType: "xlsx",
	}, &types.KnowledgeBase{ID: "kb"}, knowledge, types.EffectiveProcessConfig{
		ChunkingConfig: types.ChunkingConfig{
			ParserEngineRules: []types.ParserEngineRule{{
				FileTypes: []string{"xlsx"},
				Engine:    "builtin",
			}},
		},
	}, false)

	// nil error is what ProcessDocument returns to asynq, so the task is not
	// retried. ReadResult.Error is the existing permanent-failure path.
	require.Nil(t, result)
	require.NoError(t, err)
	require.Equal(t, types.ParseStatusFailed, knowledge.ParseStatus)
	require.NotContains(t, knowledge.ErrorMessage, "DocReader logs")
	require.Contains(t, knowledge.ErrorMessage, "too large")
	require.Contains(t, knowledge.ErrorMessage, "max_xml_nodes")
	require.Contains(t, knowledge.ErrorMessage, "2000000")

	status, code, message := rootSpan(t, db, knowledge.ID, attempt)
	require.Equal(t, types.SpanStatusFailed, status)
	require.Equal(t, werrors.ErrCodeDocReaderParseFailed, code)
	require.Equal(t, knowledge.ErrorMessage, message)
}
