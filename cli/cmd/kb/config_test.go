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
	cfg *sdk.InitializationConfig
	err error
}

func (f *fakeConfigSvc) GetInitializationConfig(_ context.Context, _ string) (*sdk.InitializationConfig, error) {
	return f.cfg, f.err
}

func TestKBConfig_EmitsConfig(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	svc := &fakeConfigSvc{cfg: &sdk.InitializationConfig{EmbeddingModelID: "model_emb", ChatModelID: "model_chat"}}
	require.NoError(t, runConfig(context.Background(), &cmdutil.FormatOptions{Mode: cmdutil.FormatJSON}, svc, "kb_abc"))
	var env struct {
		OK   bool                     `json:"ok"`
		Data sdk.InitializationConfig `json:"data"`
	}
	require.NoError(t, json.Unmarshal(out.Bytes(), &env))
	assert.True(t, env.OK)
	assert.Equal(t, "model_emb", env.Data.EmbeddingModelID)
	assert.Equal(t, "model_chat", env.Data.ChatModelID)
}

// TestKBConfig_NilConfig: a nil server config (KB not yet initialized) emits an
// empty object, not a crash.
func TestKBConfig_NilConfig(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	svc := &fakeConfigSvc{cfg: nil}
	require.NoError(t, runConfig(context.Background(), &cmdutil.FormatOptions{Mode: cmdutil.FormatJSON}, svc, "kb_abc"))
	var env struct {
		Data sdk.InitializationConfig `json:"data"`
	}
	require.NoError(t, json.Unmarshal(out.Bytes(), &env))
	assert.Empty(t, env.Data.EmbeddingModelID)
}
