package modelcmd

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
	"github.com/Tencent/WeKnora/cli/internal/iostreams"
	sdk "github.com/Tencent/WeKnora/client"
)

// fakeUpdateSvc scripts GetModel (fetch baseline) and captures the
// UpdateModelRequest so tests can assert the surgical overlay.
type fakeUpdateSvc struct {
	cur    *sdk.Model
	gotReq *sdk.UpdateModelRequest
	gotID  string
	kv     map[string]json.RawMessage
	kvPuts []json.RawMessage
}

func (f *fakeUpdateSvc) GetModel(_ context.Context, _ string) (*sdk.Model, error) {
	return f.cur, nil
}

func (f *fakeUpdateSvc) UpdateModel(_ context.Context, id string, req *sdk.UpdateModelRequest) (*sdk.Model, error) {
	f.gotID = id
	f.gotReq = req
	return &sdk.Model{ID: id, Name: req.Name, DisplayName: req.DisplayName, Parameters: req.Parameters}, nil
}

func (f *fakeUpdateSvc) GetTenantKV(_ context.Context, key string) (json.RawMessage, error) {
	if raw, ok := f.kv[key]; ok {
		return raw, nil
	}
	return json.RawMessage("null"), nil
}

func (f *fakeUpdateSvc) UpdateTenantKV(_ context.Context, key string, value any) (json.RawMessage, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	if f.kv == nil {
		f.kv = map[string]json.RawMessage{}
	}
	f.kv[key] = raw
	f.kvPuts = append(f.kvPuts, raw)
	return raw, nil
}

func baseModel() *sdk.Model {
	return &sdk.Model{
		ID: "mdl_x", Name: "keep-name", DisplayName: "old", Description: "olddesc",
		Type:       sdk.ModelTypeEmbedding,
		Parameters: sdk.ModelParameters{"base_url": "http://old", "api_key": "SECRET-OLD", "provider": "generic"},
	}
}

// TestModelUpdate_SurgicalOverlay: only touched fields change; the rest (name,
// description, existing params) round-trip from the fetched baseline.
func TestModelUpdate_SurgicalOverlay(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	svc := &fakeUpdateSvc{cur: baseModel()}
	opts := &UpdateOptions{DisplayName: "new-display", flags: modelUpdateFlags{displayName: true}}
	require.NoError(t, runUpdate(context.Background(), opts, &cmdutil.FormatOptions{Mode: cmdutil.FormatJSON}, svc, "mdl_x", nil))

	require.NotNil(t, svc.gotReq)
	assert.Equal(t, "mdl_x", svc.gotID, "id preserved (in-place update)")
	assert.Equal(t, "new-display", svc.gotReq.DisplayName, "display-name overlaid")
	assert.Equal(t, "keep-name", svc.gotReq.Name, "untouched name round-trips")
	assert.Equal(t, "olddesc", svc.gotReq.Description, "untouched description round-trips")
	assert.Equal(t, "http://old", svc.gotReq.Parameters["base_url"], "untouched params round-trip")
	assert.Equal(t, "SECRET-OLD", svc.gotReq.Parameters["api_key"], "existing key preserved when not rotating")
}

// TestModelUpdate_RotateKeyAndBaseURL: --api-key-stdin + --base-url overlay the
// parameters map; the new key comes from stdin, never argv.
func TestModelUpdate_RotateKeyAndBaseURL(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	svc := &fakeUpdateSvc{cur: baseModel()}
	opts := &UpdateOptions{
		BaseURL: "http://new", APIKeyStdin: true,
		StdinReader: strings.NewReader("NEW-KEY\n"),
		flags:       modelUpdateFlags{baseURL: true},
	}
	require.NoError(t, runUpdate(context.Background(), opts, &cmdutil.FormatOptions{Mode: cmdutil.FormatJSON}, svc, "mdl_x", nil))
	assert.Equal(t, "http://new", svc.gotReq.Parameters["base_url"])
	assert.Equal(t, "NEW-KEY", svc.gotReq.Parameters["api_key"], "rotated key read from stdin")
}

// TestModelUpdate_RequiresAtLeastOneFlag: a bare `model update <id>` is rejected
// before any network call, matching agent update.
func TestModelUpdate_RequiresAtLeastOneFlag(t *testing.T) {
	assert.False(t, modelUpdateHasFlag(&UpdateOptions{}))
	assert.True(t, modelUpdateHasFlag(&UpdateOptions{flags: modelUpdateFlags{displayName: true}}))
	assert.True(t, modelUpdateHasFlag(&UpdateOptions{APIKeyStdin: true}))
	assert.True(t, modelUpdateHasFlag(&UpdateOptions{Params: []string{"k=v"}}))
}

// TestModelUpdate_DefaultUpdatesWorkspaceSettings: --default edits the
// workspace default settings, the state the server projects into is_default.
// The full-object PUT must carry the other slots over from GET.
func TestModelUpdate_DefaultUpdatesWorkspaceSettings(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	svc := &fakeUpdateSvc{cur: baseModel(), kv: map[string]json.RawMessage{
		workspaceDefaultSettingsKey: json.RawMessage(
			`{"chat_model_id":"chat-1","embedding_model_id":"emb-old","rerank_model_id":"rerank-1"}`),
	}}
	opts := &UpdateOptions{Default: true, flags: modelUpdateFlags{def: true}}
	require.NoError(t, runUpdate(context.Background(), opts,
		&cmdutil.FormatOptions{Mode: cmdutil.FormatText}, svc, "mdl_x", nil))

	require.Len(t, svc.kvPuts, 1)
	var settings tenantDefaultSettings
	require.NoError(t, json.Unmarshal(svc.kvPuts[0], &settings))
	assert.Equal(t, "mdl_x", settings.EmbeddingModelID)
	assert.Equal(t, "chat-1", settings.ChatModelID, "other slots are preserved")
	assert.Equal(t, "rerank-1", settings.RerankModelID, "other slots are preserved")
	assert.False(t, svc.gotReq.IsDefault, "the ignored model-body field is not sent")
}

// TestModelUpdate_ClearDefaultOnlyWhenThisModelHoldsIt: --default=false unsets
// this model's slot and must never clear a default owned by another model.
func TestModelUpdate_ClearDefaultOnlyWhenThisModelHoldsIt(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	svc := &fakeUpdateSvc{cur: baseModel(), kv: map[string]json.RawMessage{
		workspaceDefaultSettingsKey: json.RawMessage(`{"embedding_model_id":"mdl_x"}`),
	}}
	opts := &UpdateOptions{Default: false, flags: modelUpdateFlags{def: true}}
	require.NoError(t, runUpdate(context.Background(), opts,
		&cmdutil.FormatOptions{Mode: cmdutil.FormatText}, svc, "mdl_x", nil))

	require.Len(t, svc.kvPuts, 1)
	var settings tenantDefaultSettings
	require.NoError(t, json.Unmarshal(svc.kvPuts[0], &settings))
	assert.Empty(t, settings.EmbeddingModelID, "clearing removes this model from the slot")

	other := &fakeUpdateSvc{cur: baseModel(), kv: map[string]json.RawMessage{
		workspaceDefaultSettingsKey: json.RawMessage(`{"embedding_model_id":"other-model"}`),
	}}
	require.NoError(t, runUpdate(context.Background(), opts,
		&cmdutil.FormatOptions{Mode: cmdutil.FormatText}, other, "mdl_x", nil))
	assert.Empty(t, other.kvPuts, "--default=false must not clear another model's default")
}
