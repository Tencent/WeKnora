package chunkcmd

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
	"github.com/Tencent/WeKnora/cli/internal/iostreams"
	sdk "github.com/Tencent/WeKnora/client"
)

type fakeViewSvc struct {
	resp *sdk.Chunk
	err  error
}

func (f *fakeViewSvc) GetChunkByIDOnly(_ context.Context, _ string) (*sdk.Chunk, error) {
	return f.resp, f.err
}

func TestView_Happy_RendersAllFields(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	svc := &fakeViewSvc{resp: &sdk.Chunk{
		ID:              "c1",
		SeqID:           42,
		ChunkIndex:      0,
		KnowledgeID:     "doc_abc",
		KnowledgeBaseID: "kb_abc",
		ChunkType:       "text",
		IsEnabled:       true,
		Content:         "the quick brown fox",
		CreatedAt:       "2026-05-15T11:00:00Z",
		UpdatedAt:       "2026-05-15T12:00:00Z",
	}}
	require.NoError(t, runView(context.Background(), &ViewOptions{ChunkID: "c1"}, nil, svc))
	body := out.String()
	assert.Contains(t, body, "c1")
	assert.Contains(t, body, "doc_abc")
	assert.Contains(t, body, "kb_abc")
	assert.Contains(t, body, "text")
	assert.Contains(t, body, "the quick brown fox", "content must render in full")
}

func TestView_HumanLabels_DocAndKB(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	svc := &fakeViewSvc{resp: &sdk.Chunk{
		ID: "c1", KnowledgeID: "doc_abc", KnowledgeBaseID: "kb_abc",
	}}
	require.NoError(t, runView(context.Background(), &ViewOptions{ChunkID: "c1"}, nil, svc))
	body := out.String()
	// Human KV uses friendlier DOC_ID / KB_ID labels (the SDK's
	// knowledge_id / knowledge_base_id are kept only in --json output).
	assert.Contains(t, body, "doc_id")
	assert.Contains(t, body, "kb_id")
	assert.NotContains(t, body, "knowledge_id")
	assert.NotContains(t, body, "knowledge_base_id")
}

func TestView_OmitsZeroOrEmpty(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	svc := &fakeViewSvc{resp: &sdk.Chunk{ID: "c_min", Content: "x"}}
	require.NoError(t, runView(context.Background(), &ViewOptions{ChunkID: "c_min"}, nil, svc))
	body := out.String()
	// status / start_at / end_at all zero → must be omitted from the human KV.
	assert.NotContains(t, body, "status:")
	assert.NotContains(t, body, "start_at:")
	assert.NotContains(t, body, "end_at:")
	// tag_id / image_info empty → omitted.
	assert.NotContains(t, body, "tag_id:")
	assert.NotContains(t, body, "image_info:")
}

func TestView_JSON_BareSDKShape(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	svc := &fakeViewSvc{resp: &sdk.Chunk{
		ID: "c_json", KnowledgeID: "doc_abc", KnowledgeBaseID: "kb_abc",
	}}
	require.NoError(t, runView(context.Background(), &ViewOptions{ChunkID: "c_json"}, &cmdutil.JSONOptions{}, svc))
	var got sdk.Chunk
	require.NoError(t, json.Unmarshal(out.Bytes(), &got))
	assert.Equal(t, "c_json", got.ID)
	assert.Equal(t, "doc_abc", got.KnowledgeID)
	// JSON uses SDK snake_case keys (knowledge_id), not human relabel doc_id.
	assert.Contains(t, out.String(), `"knowledge_id":"doc_abc"`)
	assert.Contains(t, out.String(), `"knowledge_base_id":"kb_abc"`)
	assert.NotContains(t, out.String(), `"doc_id"`)
	assert.NotContains(t, out.String(), `"kb_id"`)
}

func TestView_404(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	svc := &fakeViewSvc{err: errors.New("HTTP error 404: not found")}
	err := runView(context.Background(), &ViewOptions{ChunkID: "missing"}, nil, svc)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "resource.not_found")
}
