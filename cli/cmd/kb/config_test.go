package kb

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
	"github.com/Tencent/WeKnora/cli/internal/iostreams"
	sdk "github.com/Tencent/WeKnora/client"
)

type fakeConfigSvc struct {
	cfg *sdk.KBModelConfigView
	err error
}

func (f *fakeConfigSvc) GetInitializationConfig(_ context.Context, _ string) (*sdk.KBModelConfigView, error) {
	return f.cfg, f.err
}

func TestKBConfig_EmitsConfig(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	svc := &fakeConfigSvc{cfg: &sdk.KBModelConfigView{
		RetrievalReady: true,
		Embedding:      sdk.ModelSlotView{Configured: true, ModelName: "model_emb", Source: "remote"},
		LLM:            sdk.ModelSlotView{Configured: true, ModelName: "model_chat"},
	}}
	require.NoError(t, runConfig(context.Background(), &cmdutil.FormatOptions{Mode: cmdutil.FormatJSON}, svc, "kb_abc"))
	var env struct {
		OK   bool                  `json:"ok"`
		Data sdk.KBModelConfigView `json:"data"`
	}
	require.NoError(t, json.Unmarshal(out.Bytes(), &env))
	assert.True(t, env.OK)
	assert.Equal(t, "model_emb", env.Data.Embedding.ModelName)
	assert.Equal(t, "model_chat", env.Data.LLM.ModelName)
	assert.True(t, env.Data.RetrievalReady)
}

// TestKBConfig_SecretFree: the view type has no apiKey/baseUrl field, so the
// JSON output can never carry provider credentials — the CLI never echoes the
// keys the server returns for the web config form.
func TestKBConfig_SecretFree(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	svc := &fakeConfigSvc{cfg: &sdk.KBModelConfigView{
		Embedding: sdk.ModelSlotView{Configured: true, ModelName: "e", Source: "remote"},
	}}
	require.NoError(t, runConfig(context.Background(), &cmdutil.FormatOptions{Mode: cmdutil.FormatJSON}, svc, "kb_abc"))
	assert.NotContains(t, out.String(), "apiKey")
	assert.NotContains(t, out.String(), "api_key")
	assert.NotContains(t, out.String(), "baseUrl")
}

// TestKBConfig_NilConfig: a nil server config (KB not yet initialized) emits
// retrieval_ready:false, not a crash.
func TestKBConfig_NilConfig(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	svc := &fakeConfigSvc{cfg: nil}
	require.NoError(t, runConfig(context.Background(), &cmdutil.FormatOptions{Mode: cmdutil.FormatJSON}, svc, "kb_abc"))
	var env struct {
		Data sdk.KBModelConfigView `json:"data"`
	}
	require.NoError(t, json.Unmarshal(out.Bytes(), &env))
	assert.False(t, env.Data.RetrievalReady)
	assert.Empty(t, env.Data.Embedding.ModelName)
}
