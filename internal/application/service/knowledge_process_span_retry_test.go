package service

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"testing/iotest"

	werrors "github.com/Tencent/WeKnora/internal/errors"
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
