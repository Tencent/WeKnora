package handler

import (
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
)

// TestBuildSpanTree_AssemblesParentChild covers the basic shape: a root
// with stage children and an image generation grandchild. The handler's
// JSON contract depends on this tree topology; if the linking logic
// regresses, the UI sees a flat list and renders nothing.
func TestBuildSpanTree_AssemblesParentChild(t *testing.T) {
	now := time.Now()
	rows := []types.KnowledgeProcessingSpan{
		{KnowledgeID: "kid", Attempt: 1, SpanID: "root", Name: "knowledge_processing", Kind: types.SpanKindRoot, Status: types.SpanStatusRunning, StartedAt: &now},
		{KnowledgeID: "kid", Attempt: 1, SpanID: "doc", ParentSpanID: "root", Name: types.StageDocReader, Kind: types.SpanKindStage, Status: types.SpanStatusDone, StartedAt: &now},
		{KnowledgeID: "kid", Attempt: 1, SpanID: "mm", ParentSpanID: "root", Name: types.StageMultimodal, Kind: types.SpanKindStage, Status: types.SpanStatusRunning, StartedAt: &now},
		{KnowledgeID: "kid", Attempt: 1, SpanID: "img0", ParentSpanID: "mm", Name: "multimodal.image[0]", Kind: types.SpanKindGeneration, Status: types.SpanStatusRunning, StartedAt: &now},
	}

	tree, currentStage, lastFail := buildSpanTree("kid", 1, rows)
	require := assert.New(t)
	require.NotNil(tree)
	require.Equal("root", tree.SpanID)
	require.Equal(types.StageMultimodal, currentStage,
		"running stage span surfaces as current_stage")
	require.Nil(lastFail)

	// The 5 canonical stages must all appear under root (real or
	// synthesized placeholder).
	stageNames := map[string]string{}
	for _, child := range tree.Children {
		if child.Kind == types.SpanKindStage {
			stageNames[child.Name] = child.Status
		}
	}
	for _, name := range types.AllStages {
		_, ok := stageNames[name]
		require.True(ok, "stage %s must appear in tree", name)
	}
	require.Equal(types.SpanStatusDone, stageNames[types.StageDocReader])
	require.Equal(types.SpanStatusRunning, stageNames[types.StageMultimodal])
	require.Equal(types.SpanStatusPending, stageNames[types.StageEmbedding],
		"missing stage rows must synthesize as pending placeholders")

	// The image subspan must hang under multimodal, not at root level.
	var mmNode *types.SpanTreeNode
	for _, c := range tree.Children {
		if c.Name == types.StageMultimodal {
			mmNode = c
			break
		}
	}
	require.NotNil(mmNode)
	require.Len(mmNode.Children, 1, "image generation subspan must be a child of multimodal stage")
	require.Equal("multimodal.image[0]", mmNode.Children[0].Name)
}

// TestBuildSpanTree_NoRows_SynthesizesPlaceholderRoot ensures the API
// keeps a stable shape for fresh / never-parsed knowledge: the frontend
// always gets a `trace` with five pending stage children, never a
// nil/empty response.
func TestBuildSpanTree_NoRows_SynthesizesPlaceholderRoot(t *testing.T) {
	tree, currentStage, lastFail := buildSpanTree("kid-empty", 0, nil)
	a := assert.New(t)
	a.NotNil(tree)
	a.Equal(types.SpanKindRoot, tree.Kind)
	a.Equal(types.SpanStatusPending, tree.Status)
	a.Equal("", currentStage, "no rows means no running stage")
	a.Nil(lastFail)
	// All 5 stages must be present as pending placeholders so the UI
	// renders a complete timeline even pre-parse.
	a.Len(tree.Children, len(types.AllStages))
	for _, child := range tree.Children {
		a.Equal(types.SpanStatusPending, child.Status)
		a.Equal(types.SpanKindStage, child.Kind)
	}
}

// TestBuildSpanTree_LastFailureSurfaces records that a failed stage is
// reported as last_error so the UI can highlight the responsible step
// even if a later stage was cancelled by cascade.
func TestBuildSpanTree_LastFailureSurfaces(t *testing.T) {
	now := time.Now()
	finished := now.Add(5 * time.Second)
	rows := []types.KnowledgeProcessingSpan{
		{KnowledgeID: "kid", Attempt: 1, SpanID: "root", Name: "knowledge_processing", Kind: types.SpanKindRoot, Status: types.SpanStatusFailed, StartedAt: &now, FinishedAt: &finished},
		{KnowledgeID: "kid", Attempt: 1, SpanID: "doc", ParentSpanID: "root", Name: types.StageDocReader, Kind: types.SpanKindStage, Status: types.SpanStatusFailed, ErrorCode: "DOCREADER_TIMEOUT", ErrorMessage: "slow", StartedAt: &now, FinishedAt: &finished},
		{KnowledgeID: "kid", Attempt: 1, SpanID: "chunk", ParentSpanID: "root", Name: types.StageChunking, Kind: types.SpanKindStage, Status: types.SpanStatusCancelled, ErrorCode: "UPSTREAM_FAILED"},
	}
	_, _, lastFail := buildSpanTree("kid", 1, rows)
	a := assert.New(t)
	a.NotNil(lastFail)
	a.Equal(types.StageDocReader, lastFail.Name,
		"last_error must point at the actually-failed span, not the cascade-cancelled downstream")
	a.Equal("DOCREADER_TIMEOUT", lastFail.ErrorCode)
}
