package format

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSuccessEnvelope(t *testing.T) {
	env := Success(map[string]string{"id": "kb_abc"}, &Meta{Context: "prod", RequestID: "cli-01"})
	var buf bytes.Buffer
	require.NoError(t, WriteEnvelope(&buf, env))
	var got map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &got))
	assert.Equal(t, true, got["ok"])
	assert.NotNil(t, got["data"])
	assert.NotNil(t, got["_meta"])
	assert.Nil(t, got["error"])
}

func TestFailureEnvelope(t *testing.T) {
	env := Failure(&ErrorBody{
		Code:      "auth.unauthenticated",
		Message:   "no creds",
		Hint:      "run weknora auth login",
		Retryable: false,
	})
	var buf bytes.Buffer
	require.NoError(t, WriteEnvelope(&buf, env))
	var got map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &got))
	assert.Equal(t, false, got["ok"])
	errBody := got["error"].(map[string]any)
	assert.Equal(t, "auth.unauthenticated", errBody["code"])
	assert.Equal(t, "run weknora auth login", errBody["hint"])
}

func TestEnvelope_RiskAndNotice(t *testing.T) {
	env := Success(map[string]string{"id": "kb_x"}, nil)
	env.Risk = &Risk{Level: RiskHighRiskWrite, Action: "delete kb_x"}
	env.Notice = &Notice{Update: &UpdateNotice{Available: true, Current: "0.2.0", Latest: "0.3.0"}}
	var buf bytes.Buffer
	require.NoError(t, WriteEnvelope(&buf, env))
	var got map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &got))
	risk := got["risk"].(map[string]any)
	assert.Equal(t, "high-risk-write", risk["level"])
	assert.Equal(t, "delete kb_x", risk["action"])
	notice := got["_notice"].(map[string]any)
	upd := notice["update"].(map[string]any)
	assert.Equal(t, true, upd["available"])
	assert.Equal(t, "0.3.0", upd["latest"])
}

func TestEnvelope_NoEscapeHTML(t *testing.T) {
	// Hints / messages may include & or <; ensure we don't HTML-escape them
	// (would break agent jq pipelines).
	env := Failure(&ErrorBody{Code: "x", Message: "a & b < c"})
	var buf bytes.Buffer
	require.NoError(t, WriteEnvelope(&buf, env))
	assert.Contains(t, buf.String(), "a & b < c")
}
