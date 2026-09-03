package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/handler/dto"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestKnowledgeHandler_validateInitialMetadataRejectsValueLessRequests(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		message string
		request dto.MetadataValueChangeRequest
	}{
		{
			name:    "policy only",
			message: "initial metadata values must include a value",
			request: dto.MetadataValueChangeRequest{
				MetadataDefinitionID: "definition-policy",
				AllowAutoOverwrite:   boolPtr(true),
			},
		},
		{
			name:    "null value",
			message: "initial metadata values must include a value",
			request: dto.MetadataValueChangeRequest{
				MetadataDefinitionID: "definition-null",
				Value:                json.RawMessage("null"),
			},
		},
		{
			name:    "non-zero expected version",
			message: "initial metadata values must use expected_version 0",
			request: dto.MetadataValueChangeRequest{
				MetadataDefinitionID: "definition-version",
				Value:                json.RawMessage(`"engineers"`),
				ExpectedVersion:      intPtr(3),
			},
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			h := &KnowledgeHandler{
				metadataService: &metadataHandlerServiceStub{
					validateDocument: func(context.Context, string, []types.MetadataValueChange) error {
						t.Fatal("ValidateDocumentMetadataChanges must not run for invalid initial metadata")
						return nil
					},
				},
			}

			err := h.validateInitialMetadata(context.Background(), "kb-1", []dto.MetadataValueChangeRequest{tc.request})
			require.Error(t, err)

			appErr, ok := apperrors.IsAppError(err)
			require.True(t, ok)
			require.Equal(t, http.StatusBadRequest, appErr.HTTPCode)
			require.Equal(t, tc.message, appErr.Message)
		})
	}
}

func TestKnowledgeHandler_applyInitialMetadataWrapsFailuresAsInternalServerError(t *testing.T) {
	t.Parallel()

	called := false
	h := &KnowledgeHandler{
		metadataService: &metadataHandlerServiceStub{
			changeDocument: func(context.Context, types.ChangeDocumentMetadata) (*types.DocumentMetadata, error) {
				called = true
				return nil, apperrors.NewBadRequestError("metadata rejected")
			},
		},
	}

	err := h.applyInitialMetadata(context.Background(), "doc-1", []dto.MetadataValueChangeRequest{{
		MetadataDefinitionID: "definition-1",
		Value:                json.RawMessage(`"engineers"`),
	}})
	require.True(t, called)
	require.Error(t, err)

	appErr, ok := apperrors.IsAppError(err)
	require.True(t, ok)
	require.Equal(t, http.StatusInternalServerError, appErr.HTTPCode)
	require.Equal(t, "knowledge created, but metadata was not saved", appErr.Message)
}

func TestKnowledgeHandler_validateInitialMetadataPassesThroughValues(t *testing.T) {
	t.Parallel()

	called := false
	h := &KnowledgeHandler{
		metadataService: &metadataHandlerServiceStub{
			validateDocument: func(_ context.Context, knowledgeBaseID string, changes []types.MetadataValueChange) error {
				called = true
				require.Equal(t, "kb-1", knowledgeBaseID)
				require.Len(t, changes, 1)
				require.True(t, changes[0].ValueSet)
				require.Equal(t, "engineers", changes[0].Value)
				require.NotNil(t, changes[0].ExpectedVersion)
				require.Equal(t, 0, *changes[0].ExpectedVersion)
				return nil
			},
		},
	}

	err := h.validateInitialMetadata(context.Background(), "kb-1", []dto.MetadataValueChangeRequest{{
		MetadataDefinitionID: "definition-1",
		Value:                json.RawMessage(`"engineers"`),
	}})
	require.NoError(t, err)
	require.True(t, called)
}

func boolPtr(v bool) *bool { return &v }

func intPtr(v int) *int { return &v }
