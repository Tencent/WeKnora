package storageurl

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func publicRewriter(url string) *Rewriter {
	return NewRewriter(stubResolver(url), "TEST")
}

func TestNewRequestRewriter_HandleModeIsDisabled(t *testing.T) {
	w := NewRequestRewriter(context.Background(), ModeHandle, &stubFileService{}, nil)
	assert.False(t, w.Enabled(), "the default mode must not resolve anything")
}

func TestNewRequestRewriter_PublicModeIsEnabled(t *testing.T) {
	w := NewRequestRewriter(context.Background(), ModePublic, &stubFileService{}, nil)
	require.True(t, w.Enabled())
	assert.Equal(t, "https://cdn.example.com/resource://xifDo7NTSL300Lp1goVutw",
		w.Ref(context.Background(), "resource://xifDo7NTSL300Lp1goVutw"))
}

func TestRewriteMessages(t *testing.T) {
	w := publicRewriter("https://cdn.example.com/x.png")
	ctx := context.Background()

	messages := []*types.Message{
		nil,
		{
			Content: "answer ![fig](resource://xifDo7NTSL300Lp1goVutw)",
			Images: types.MessageImages{{
				URL:     "resource://aaaabbbbccccddddeeeeff",
				Caption: "shows ![inline](minio://bucket/10000/exports/a.png)",
			}},
			KnowledgeReferences: types.References{{
				Content:   "chunk ![c](resource://xifDo7NTSL300Lp1goVutw)",
				ImageInfo: `[{"url":"resource://xifDo7NTSL300Lp1goVutw"}]`,
			}},
			AgentSteps: types.AgentSteps{{
				Thought: "looking at ![t](resource://xifDo7NTSL300Lp1goVutw)",
				ToolCalls: []types.ToolCall{{
					Reflection: "saw ![r](resource://xifDo7NTSL300Lp1goVutw)",
					Result:     &types.ToolResult{Output: "chart ![o](resource://xifDo7NTSL300Lp1goVutw)"},
				}},
			}},
		},
	}

	w.RewriteMessages(ctx, messages)

	message := messages[1]
	assert.Equal(t, "answer ![fig](https://cdn.example.com/x.png)", message.Content)
	assert.Equal(t, "https://cdn.example.com/x.png", message.Images[0].URL)
	assert.Equal(t, "shows ![inline](https://cdn.example.com/x.png)", message.Images[0].Caption)
	assert.Equal(t, "chunk ![c](https://cdn.example.com/x.png)", message.KnowledgeReferences[0].Content)
	assert.Equal(t, `[{"url":"https://cdn.example.com/x.png"}]`, message.KnowledgeReferences[0].ImageInfo)
	assert.Equal(t, "looking at ![t](https://cdn.example.com/x.png)", message.AgentSteps[0].Thought)
	assert.Equal(t, "saw ![r](https://cdn.example.com/x.png)", message.AgentSteps[0].ToolCalls[0].Reflection)
	assert.Equal(t, "chart ![o](https://cdn.example.com/x.png)", message.AgentSteps[0].ToolCalls[0].Result.Output)
}

func TestRewriteMessages_DisabledLeavesHandles(t *testing.T) {
	w := NewRewriter(nil, "TEST")
	messages := []*types.Message{{Content: "![a](resource://xifDo7NTSL300Lp1goVutw)"}}
	w.RewriteMessages(context.Background(), messages)
	assert.Equal(t, "![a](resource://xifDo7NTSL300Lp1goVutw)", messages[0].Content)
}

// SSE references payloads share their *SearchResult pointers with the stream
// replay buffer and the assistant message being persisted, so rewriting must not
// mutate the originals.
func TestCopyReferences_DoesNotMutateOriginals(t *testing.T) {
	w := publicRewriter("https://cdn.example.com/x.png")
	original := &types.SearchResult{
		Content:        "chunk ![c](resource://xifDo7NTSL300Lp1goVutw)",
		MatchedContent: "match ![m](resource://xifDo7NTSL300Lp1goVutw)",
		ImageInfo:      `[{"url":"resource://xifDo7NTSL300Lp1goVutw"}]`,
	}
	refs := []*types.SearchResult{original, nil}

	out := w.CopyReferences(context.Background(), refs)

	require.Len(t, out, 2)
	assert.NotSame(t, original, out[0])
	assert.Equal(t, "chunk ![c](resource://xifDo7NTSL300Lp1goVutw)", original.Content,
		"the replay buffer's copy must be untouched")
	assert.Equal(t, "chunk ![c](https://cdn.example.com/x.png)", out[0].Content)
	assert.Equal(t, "match ![m](https://cdn.example.com/x.png)", out[0].MatchedContent)
	assert.Equal(t, `[{"url":"https://cdn.example.com/x.png"}]`, out[0].ImageInfo)
	assert.Nil(t, out[1])
}

func TestCopyReferences_DisabledReturnsInput(t *testing.T) {
	w := NewRewriter(nil, "TEST")
	refs := []*types.SearchResult{{Content: "![a](resource://xifDo7NTSL300Lp1goVutw)"}}
	assert.Equal(t, refs, w.CopyReferences(context.Background(), refs))
}

// Agent tool metadata is tool-defined, so every string leaf is rewritten — and
// the source map, which the replay buffer also holds, must not be mutated.
func TestCopyData_RewritesNestedStringsWithoutMutating(t *testing.T) {
	w := publicRewriter("https://cdn.example.com/x.png")
	data := map[string]interface{}{
		"tool_name":   "chart_export",
		"duration_ms": 42,
		"output":      "![chart](resource://xifDo7NTSL300Lp1goVutw)",
		"nested": map[string]interface{}{
			"images": []interface{}{"resource://xifDo7NTSL300Lp1goVutw", "http://example.com/x.png"},
		},
	}

	out := w.CopyData(context.Background(), data)

	assert.Equal(t, "![chart](resource://xifDo7NTSL300Lp1goVutw)", data["output"],
		"the replay buffer's map must be untouched")
	assert.Equal(t, "![chart](https://cdn.example.com/x.png)", out["output"])
	assert.Equal(t, "chart_export", out["tool_name"])
	assert.Equal(t, 42, out["duration_ms"])

	nested := out["nested"].(map[string]interface{})["images"].([]interface{})
	assert.Equal(t, "https://cdn.example.com/x.png", nested[0])
	assert.Equal(t, "http://example.com/x.png", nested[1])
}

// The references SSE event carries its results twice: in
// StreamResponse.KnowledgeReferences and again in Data. An in-memory stream
// manager keeps the typed slice, so CopyData must traverse it or the Data copy
// leaks the handles the caller asked to have resolved.
func TestCopyData_RewritesTypedReferenceSlices(t *testing.T) {
	w := publicRewriter("https://cdn.example.com/x.png")
	original := &types.SearchResult{Content: "figure ![f](resource://xifDo7NTSL300Lp1goVutw)"}
	data := map[string]interface{}{
		"references": types.References{original},
		"tags":       []string{"resource://xifDo7NTSL300Lp1goVutw", "plain"},
		"metadata":   map[string]string{"thumb": "resource://xifDo7NTSL300Lp1goVutw"},
	}

	out := w.CopyData(context.Background(), data)

	refs := out["references"].(types.References)
	assert.Equal(t, "figure ![f](https://cdn.example.com/x.png)", refs[0].Content)
	assert.Equal(t, "figure ![f](resource://xifDo7NTSL300Lp1goVutw)", original.Content)
	assert.Equal(t,
		[]string{"https://cdn.example.com/x.png", "plain"},
		out["tags"].([]string),
	)
	assert.Equal(t,
		map[string]string{"thumb": "https://cdn.example.com/x.png"},
		out["metadata"].(map[string]string),
	)
}

// Copying every metadata map on every SSE event would be pure garbage, so an
// unchanged map must be returned as-is.
func TestCopyData_ReturnsInputWhenNothingChanges(t *testing.T) {
	w := publicRewriter("https://cdn.example.com/x.png")
	data := map[string]interface{}{"tool_name": "chart_export", "duration_ms": 42}
	assert.Equal(t, data, w.CopyData(context.Background(), data))
}

func TestCopyData_NilAndDisabled(t *testing.T) {
	assert.Nil(t, publicRewriter("https://x/y.png").CopyData(context.Background(), nil))
	data := map[string]interface{}{"output": "![a](resource://xifDo7NTSL300Lp1goVutw)"}
	assert.Equal(t, data, NewRewriter(nil, "TEST").CopyData(context.Background(), data))
}

func TestDefaultMode(t *testing.T) {
	ctx := context.Background()
	t.Setenv(EnvVar, "")
	assert.Equal(t, ModeHandle, DefaultMode(ctx))

	t.Setenv(EnvVar, "public")
	assert.Equal(t, ModePublic, DefaultMode(ctx))

	t.Setenv(EnvVar, "nonsense")
	assert.Equal(t, ModeHandle, DefaultMode(ctx), "a typo must degrade to the safe default")
}

// Anonymous surfaces (embed channels) pin the mode: neither the query parameter
// nor the deployment default may hand a visitor a credential-free URL. The
// downgrade is silent so a client that forwards the parameter keeps working.
func TestResolveMode_ForcedHandleModeWins(t *testing.T) {
	t.Setenv(EnvVar, "public")
	ctx := WithForcedHandleMode(context.Background())

	for _, queryValue := range []string{"", "public", "handle", "nonsense"} {
		mode, err := ResolveMode(ctx, queryValue)
		require.NoError(t, err, "queryValue=%q", queryValue)
		assert.Equal(t, ModeHandle, mode, "queryValue=%q", queryValue)
	}
}

// A KB-restricted API key is denied the /files proxy, so it must not receive
// anonymous file URLs through this parameter either.
func TestResolveMode_RejectsPublicForKBRestrictedKey(t *testing.T) {
	ctx := types.WithTenantAPIKeyScope(context.Background(), types.TenantAPIKeyScope{
		KnowledgeBaseIDs: types.StringArray{"kb-1"},
	})

	_, err := ResolveMode(ctx, "public")
	assert.ErrorIs(t, err, ErrPublicModeForbidden)

	// The deployment default must not smuggle it in either.
	t.Setenv(EnvVar, "public")
	_, err = ResolveMode(ctx, "")
	assert.ErrorIs(t, err, ErrPublicModeForbidden)

	// The default mode stays available: only public URLs are off limits.
	mode, err := ResolveMode(ctx, "handle")
	require.NoError(t, err)
	assert.Equal(t, ModeHandle, mode)
}

// A full-access or tenant-wide key is unaffected.
func TestResolveMode_AllowsPublicForUnrestrictedKey(t *testing.T) {
	ctx := types.WithTenantAPIKeyScope(context.Background(), types.TenantAPIKeyScope{
		Capabilities: types.StringArray{string(types.APIKeyCapabilityRetrieve)},
	})
	mode, err := ResolveMode(ctx, "public")
	require.NoError(t, err)
	assert.Equal(t, ModePublic, mode)
}

func TestResolveMode_QueryWinsOverDeployment(t *testing.T) {
	ctx := context.Background()
	t.Setenv(EnvVar, "public")

	mode, err := ResolveMode(ctx, "handle")
	require.NoError(t, err)
	assert.Equal(t, ModeHandle, mode, "an explicit query value must win")

	mode, err = ResolveMode(ctx, "")
	require.NoError(t, err)
	assert.Equal(t, ModePublic, mode)

	_, err = ResolveMode(ctx, "yes-please")
	assert.Error(t, err, "an invalid query value is a client error")
}
