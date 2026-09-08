package session

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/contentprovenance"
	"github.com/Tencent/WeKnora/internal/types"
)

const handlerTestAuditSecret = "test-content-pipeline-secret-32-bytes"

func TestProductionIdentityComesFromSignedPipelineRequest(t *testing.T) {
	apiContext := types.WithPrincipal(context.Background(), types.Principal{Type: types.PrincipalAPITenant, ID: "tenant-key"})
	envelope := contentprovenance.NewEnvelope(
		contentprovenance.Job{TaskID: "job-1", VideoID: "video-1", TranscriptGeneration: "generation-1", JobType: "graph"},
		"session-1", "agent-1", "extract-video-knowledge", "query", []string{"kb-1"}, []string{"knowledge-1"},
	)
	encoded, signed, err := contentprovenance.Sign(handlerTestAuditSecret, envelope)
	require.NoError(t, err)

	identity, err := productionIdentityFromSignedRequest(
		apiContext, handlerTestAuditSecret, encoded, signed, "content_pipeline", "session-1", "agent-1", "query",
		[]string{"extract-video-knowledge"}, []string{"kb-1"}, []string{"knowledge-1"},
	)
	require.NoError(t, err)
	require.Equal(t, pipelineProductionIdentity{
		TaskID: "job-1", VideoID: "video-1", TranscriptGeneration: "generation-1",
	}, identity)

	identity, err = productionIdentityFromSignedRequest(
		apiContext, handlerTestAuditSecret, "", "", "web", "session-1", "agent-1", "query", nil, nil, nil,
	)
	require.NoError(t, err)
	require.Empty(t, identity.TaskID)
}

func TestProductionIdentityRejectsUnsignedForgedSession(t *testing.T) {
	apiContext := types.WithPrincipal(context.Background(), types.Principal{Type: types.PrincipalAPITenant, ID: "tenant-key"})

	_, err := productionIdentityFromSignedRequest(
		apiContext, handlerTestAuditSecret, "", "", "content_pipeline", "forged-session", "agent-1", "query",
		[]string{"extract-video-knowledge"}, []string{"kb-1"}, []string{"knowledge-1"},
	)
	require.Error(t, err)

	envelope := contentprovenance.NewEnvelope(
		contentprovenance.Job{TaskID: "job-1", VideoID: "video-1", TranscriptGeneration: "generation-1", JobType: "graph"},
		"session-1", "agent-1", "extract-video-knowledge", "query", []string{"kb-1"}, []string{"knowledge-1"},
	)
	encoded, signed, err := contentprovenance.Sign(handlerTestAuditSecret, envelope)
	require.NoError(t, err)

	_, err = productionIdentityFromSignedRequest(
		apiContext, handlerTestAuditSecret, encoded, signed, "content_pipeline", "forged-session", "agent-1", "query",
		[]string{"extract-video-knowledge"}, []string{"kb-1"}, []string{"knowledge-1"},
	)
	require.ErrorIs(t, err, contentprovenance.ErrMismatchedRequest)

	webContext := types.WithPrincipal(context.Background(), types.Principal{Type: types.PrincipalWebUser, ID: "user-1"})
	_, err = productionIdentityFromSignedRequest(
		webContext, handlerTestAuditSecret, encoded, signed, "content_pipeline", "session-1", "agent-1", "query",
		[]string{"extract-video-knowledge"}, []string{"kb-1"}, []string{"knowledge-1"},
	)
	require.ErrorIs(t, err, contentprovenance.ErrMismatchedRequest)
}
