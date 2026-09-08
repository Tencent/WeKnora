package contentprovenance

import (
	"testing"

	"github.com/stretchr/testify/require"
)

const testSecret = "test-content-pipeline-secret-32-bytes"

func TestSignedEnvelopeMatchesExactAgentRequest(t *testing.T) {
	envelope := NewEnvelope(
		Job{TaskID: "job-1", VideoID: "video-1", TranscriptGeneration: "generation-1", JobType: "graph"},
		"session-1", "agent-1", "extract-video-knowledge", "query",
		[]string{"kb-1"}, []string{"knowledge-2", "knowledge-1"},
	)
	encoded, signed, err := Sign(testSecret, envelope)
	require.NoError(t, err)

	verified, err := Verify(testSecret, encoded, signed)
	require.NoError(t, err)
	require.Equal(t, envelope, verified)
	require.True(t, verified.MatchesRequest(
		"session-1", "agent-1", "query", []string{"extract-video-knowledge"},
		[]string{"kb-1"}, []string{"knowledge-1", "knowledge-2"},
	))
}

func TestSignedEnvelopeRejectsForgeryAndRequestChanges(t *testing.T) {
	envelope := NewEnvelope(
		Job{TaskID: "job-1", VideoID: "video-1", TranscriptGeneration: "generation-1", JobType: "graph"},
		"session-1", "agent-1", "extract-video-knowledge", "query", []string{"kb-1"}, []string{"knowledge-1"},
	)
	encoded, signed, err := Sign(testSecret, envelope)
	require.NoError(t, err)

	tamperedSignature := signed[:len(signed)-1] + "0"
	if tamperedSignature == signed {
		tamperedSignature = signed[:len(signed)-1] + "1"
	}
	_, err = Verify(testSecret, encoded, tamperedSignature)
	require.ErrorIs(t, err, ErrInvalidSignature)
	verified, err := Verify(testSecret, encoded, signed)
	require.NoError(t, err)
	require.False(t, verified.MatchesRequest(
		"forged-session", "agent-1", "query", []string{"extract-video-knowledge"},
		[]string{"kb-1"}, []string{"knowledge-1"},
	))
	require.False(t, verified.MatchesRequest(
		"session-1", "agent-1", "changed query", []string{"extract-video-knowledge"},
		[]string{"kb-1"}, []string{"knowledge-1"},
	))
}

func TestSignFailsClosedWithoutSecretOrTaskIdentity(t *testing.T) {
	valid := NewEnvelope(
		Job{TaskID: "job-1", VideoID: "video-1", TranscriptGeneration: "generation-1", JobType: "graph"},
		"session-1", "agent-1", "extract-video-knowledge", "query", nil, nil,
	)
	_, _, err := Sign("", valid)
	require.ErrorIs(t, err, ErrMissingSecret)

	valid.TaskID = ""
	_, _, err = Sign(testSecret, valid)
	require.ErrorIs(t, err, ErrInvalidEnvelope)
}
