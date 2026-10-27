package service

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/hibiken/asynq"
	"github.com/stretchr/testify/require"
)

type csvLimitTemporaryRepo struct {
	interfaces.TemporaryDocumentRepository
	document *types.TemporaryDocument
	message  string
	saveErr  error
}

func (r *csvLimitTemporaryRepo) GetByID(context.Context, uint64, string) (*types.TemporaryDocument, error) {
	return r.document, nil
}

func (r *csvLimitTemporaryRepo) MarkProcessing(context.Context, uint64, string, time.Time) error {
	return nil
}

func (r *csvLimitTemporaryRepo) MarkFailed(_ context.Context, _ uint64, _ string, message string) error {
	r.message = message
	return r.saveErr
}

func TestTemporaryDocumentCSVExpansionFailure(t *testing.T) {
	for _, saveErr := range []error{nil, errors.New("database unavailable")} {
		t.Run("save_failure="+strconv.FormatBool(saveErr != nil), func(t *testing.T) {
			options, err := json.Marshal(types.TemporaryDocumentCreateOptions{ParserEngine: "simple"})
			require.NoError(t, err)
			doc := &types.TemporaryDocument{
				ID: "doc", TenantID: 1, FileType: ".csv", FileName: "wide.csv", ResourceRef: "local://wide.csv",
				ProcessingOptions: options,
			}
			repo := &csvLimitTemporaryRepo{document: doc, saveErr: saveErr}
			data := []byte(strings.Repeat("x\n", 10000) + strings.Repeat(",", 9999) + "x\n")
			svc := &temporaryDocumentService{
				repo:          repo,
				fileService:   &stagingFileService{files: map[string][]byte{doc.ResourceRef: data}},
				tenantService: &stubTenantServiceForModelDelete{},
			}
			raw, err := json.Marshal(types.TemporaryDocumentTaskPayload{TenantID: 1, DocumentID: "doc"})
			require.NoError(t, err)
			err = svc.Process(context.Background(), asynq.NewTask("temporary-document", raw))
			if saveErr == nil {
				require.NoError(t, err)
			} else {
				require.ErrorIs(t, err, saveErr)
			}
			require.Contains(t, repo.message, "CSV conversion exceeds maximum Markdown size")
		})
	}
}
