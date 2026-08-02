package service

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/Tencent/WeKnora/internal/artifact"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/models/embedding"
	"github.com/Tencent/WeKnora/internal/models/vlm"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type pipelineCountingEmbedder struct {
	mu      sync.Mutex
	batches [][]string
}

func (e *pipelineCountingEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	vectors, err := e.BatchEmbed(ctx, []string{text})
	if err != nil {
		return nil, err
	}
	return vectors[0], nil
}

func (e *pipelineCountingEmbedder) BatchEmbed(
	_ context.Context,
	texts []string,
) ([][]float32, error) {
	e.mu.Lock()
	e.batches = append(e.batches, append([]string(nil), texts...))
	e.mu.Unlock()
	result := make([][]float32, len(texts))
	for index, text := range texts {
		result[index] = []float32{float32(len(text)), float32(len([]rune(text)))}
	}
	return result, nil
}

func (e *pipelineCountingEmbedder) BatchEmbedWithPool(
	ctx context.Context,
	_ embedding.Embedder,
	texts []string,
) ([][]float32, error) {
	return e.BatchEmbed(ctx, texts)
}

func (e *pipelineCountingEmbedder) GetModelName() string { return "counting-embedding" }
func (e *pipelineCountingEmbedder) GetDimensions() int   { return 2 }
func (e *pipelineCountingEmbedder) GetModelID() string   { return "counting-embedding-v1" }

func (e *pipelineCountingEmbedder) callCount() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.batches)
}

type pipelineCountingChat struct {
	mu    sync.Mutex
	calls map[string]int
}

func newPipelineCountingChat() *pipelineCountingChat {
	return &pipelineCountingChat{calls: make(map[string]int)}
}

func (c *pipelineCountingChat) Chat(
	_ context.Context,
	messages []chat.Message,
	_ *chat.ChatOptions,
) (*types.ChatResponse, error) {
	if len(messages) == 0 {
		return nil, fmt.Errorf("counting chat requires a message")
	}
	stage, _, _ := strings.Cut(messages[0].Content, "|")
	c.mu.Lock()
	c.calls[stage]++
	c.mu.Unlock()
	return &types.ChatResponse{Content: "canonical-" + stage}, nil
}

func (c *pipelineCountingChat) ChatStream(
	context.Context,
	[]chat.Message,
	*chat.ChatOptions,
) (<-chan types.StreamResponse, error) {
	result := make(chan types.StreamResponse)
	close(result)
	return result, nil
}

func (c *pipelineCountingChat) GetModelName() string { return "counting-chat" }
func (c *pipelineCountingChat) GetModelID() string   { return "counting-chat-v1" }

func (c *pipelineCountingChat) callCount(stage string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls[stage]
}

type pipelineCountingVLM struct {
	mu      sync.Mutex
	prompts []string
}

func (v *pipelineCountingVLM) Predict(
	_ context.Context,
	_ [][]byte,
	prompt string,
) (string, error) {
	v.mu.Lock()
	v.prompts = append(v.prompts, prompt)
	v.mu.Unlock()
	// Deliberately canonical across prompt revisions. This proves that a
	// changed VLM request invalidates that stage without invalidating consumers
	// when the verified output is unchanged.
	return "canonical-image-output", nil
}

func (v *pipelineCountingVLM) GetModelName() string { return "counting-vlm" }
func (v *pipelineCountingVLM) GetModelID() string   { return "counting-vlm-v1" }

func (v *pipelineCountingVLM) callCount() int {
	v.mu.Lock()
	defer v.mu.Unlock()
	return len(v.prompts)
}

type artifactPipelineHarness struct {
	service           *knowledgeService
	reader            *countingDocReader
	embeddingProvider *pipelineCountingEmbedder
	chatProvider      *pipelineCountingChat
	vlmProvider       *pipelineCountingVLM
	embedder          embedding.Embedder
	chat              chat.Chat
	vlm               vlm.VLM
}

type artifactPipelineResult struct {
	chunkIDs    []string
	vectors     [][]float32
	parseOutput string
	ocrOutput   string
	caption     string
	summary     string
	questions   string
	wikiMap     string
	graphEntity string
	graphEdge   string
	wikiReduce  string
	desired     desiredChunkSet
}

func newArtifactPipelineHarness(
	t *testing.T,
	runtime *artifact.Runtime,
) *artifactPipelineHarness {
	t.Helper()
	service := setupDocReaderArtifactService(t)
	if runtime != nil {
		service.artifactRuntime = runtime
	}
	reader := &countingDocReader{result: &types.ReadResult{
		MarkdownContent: "# Report\nstable body",
		Metadata:        map[string]string{"pages": "1"},
	}}
	embeddingProvider := &pipelineCountingEmbedder{}
	chatProvider := newPipelineCountingChat()
	vlmProvider := &pipelineCountingVLM{}
	return &artifactPipelineHarness{
		service:           service,
		reader:            reader,
		embeddingProvider: embeddingProvider,
		chatProvider:      chatProvider,
		vlmProvider:       vlmProvider,
		embedder: embedding.NewArtifactCachedEmbedder(
			embeddingProvider,
			service.artifactRuntime,
			embedding.ArtifactCacheConfig{
				TenantID: 1,
				Processor: artifact.ProcessorIdentity{
					ModelID:   "counting-embedding-v1",
					ModelName: "counting-embedding",
					Provider:  "counting",
				},
				Dimensions: 2,
			},
		),
		chat: chat.NewArtifactCachedChat(
			chatProvider,
			service.artifactRuntime,
			chat.ArtifactCacheConfig{
				TenantID: 1,
				Processor: artifact.ProcessorIdentity{
					ModelID:   "counting-chat-v1",
					ModelName: "counting-chat",
					Provider:  "counting",
				},
			},
		),
		vlm: vlm.NewArtifactCachedVLM(
			vlmProvider,
			service.artifactRuntime,
			vlm.ArtifactCacheConfig{
				TenantID: 1,
				Processor: artifact.ProcessorIdentity{
					ModelID:   "counting-vlm-v1",
					ModelName: "counting-vlm",
					Provider:  "counting",
				},
			},
		),
	}
}

func (h *artifactPipelineHarness) run(
	t *testing.T,
	parserOverride string,
	vlmPromptRevision string,
	existing []*types.Chunk,
) artifactPipelineResult {
	t.Helper()
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(1))
	parsed, err := h.service.callDocReaderWithArtifact(ctx, h.reader, &types.ReadRequest{
		FileContent:  []byte("stable file bytes"),
		FileName:     "report.pdf",
		FileType:     "pdf",
		Title:        "Report",
		ParserEngine: "counting-parser",
		ParserEngineOverrides: map[string]string{
			"pdf_force_scanned": parserOverride,
		},
	})
	require.NoError(t, err)

	knowledge := &types.Knowledge{
		ID:              "knowledge-e2e",
		TenantID:        1,
		KnowledgeBaseID: "kb-e2e",
	}
	sourceChunks := []types.ParsedChunk{
		{Content: parsed.MarkdownContent, Seq: 0, Start: 0, End: len(parsed.MarkdownContent), ParentIndex: -1},
		{Content: "second stable block", Seq: 1, Start: len(parsed.MarkdownContent), End: len(parsed.MarkdownContent) + 19, ParentIndex: -1},
	}
	desired, err := buildDesiredDocumentChunks(knowledge, sourceChunks, nil, existing)
	require.NoError(t, err)

	imageBytes := [][]byte{[]byte("stable-image-bytes")}
	ocr, err := h.vlm.Predict(
		vlm.WithArtifactStage(ctx, vlm.ArtifactStage{
			Stage:        "vlm_ocr",
			OutputSchema: "vlm.ocr.text.v1",
		}),
		imageBytes,
		"ocr|"+vlmPromptRevision,
	)
	require.NoError(t, err)
	caption, err := h.vlm.Predict(
		vlm.WithArtifactStage(ctx, vlm.ArtifactStage{
			Stage:        "vlm_caption",
			OutputSchema: "vlm.caption.text.v1",
		}),
		imageBytes,
		"caption|"+vlmPromptRevision,
	)
	require.NoError(t, err)

	embeddingInputs := make([]string, len(desired.Text))
	for index, chunk := range desired.Text {
		embeddingInputs[index] = chunk.EmbeddingContent()
	}
	embeddingContext := context.WithValue(ctx, types.EmbedDocumentContextKey, true)
	vectors, err := h.embedder.BatchEmbed(embeddingContext, embeddingInputs)
	require.NoError(t, err)

	callStage := func(stage, schema, input string) string {
		response, callErr := h.chat.Chat(
			chat.WithArtifactStage(ctx, chat.ArtifactStage{
				Stage:        stage,
				OutputSchema: schema,
			}),
			[]chat.Message{{Role: "user", Content: stage + "|" + input}},
			&chat.ChatOptions{Temperature: 0, Seed: 7, MaxTokens: 128},
		)
		require.NoError(t, callErr)
		return response.Content
	}
	summary := callStage("summary", "summary.text.v1", parsed.MarkdownContent+"|"+caption)
	questions := callStage("question", "questions.text.v1", parsed.MarkdownContent)
	wikiMap := callStage("wiki_map", "wiki.map.text.v1", parsed.MarkdownContent+"|"+caption)
	graphEntity := callStage("graph_extract.entities", "graph.entities.text.v1", parsed.MarkdownContent)
	graphEdge := callStage(
		"graph_extract.relationships",
		"graph.relationships.text.v1",
		parsed.MarkdownContent+"|"+graphEntity,
	)

	// Wiki Reduce intentionally has no ArtifactStage marker. It must reflect
	// the current live contributor set on every run.
	reduce, err := h.chat.Chat(
		ctx,
		[]chat.Message{{Role: "user", Content: "wiki_reduce|" + wikiMap}},
		&chat.ChatOptions{Temperature: 0, Seed: 7, MaxTokens: 128},
	)
	require.NoError(t, err)

	chunkIDs := make([]string, len(desired.All))
	for index, chunk := range desired.All {
		chunkIDs[index] = chunk.ID
	}
	return artifactPipelineResult{
		chunkIDs:    chunkIDs,
		vectors:     vectors,
		parseOutput: parsed.MarkdownContent,
		ocrOutput:   ocr,
		caption:     caption,
		summary:     summary,
		questions:   questions,
		wikiMap:     wikiMap,
		graphEntity: graphEntity,
		graphEdge:   graphEdge,
		wikiReduce:  reduce.Content,
		desired:     desired,
	}
}

func TestArtifactPipelineColdWarmCountingProviders(t *testing.T) {
	harness := newArtifactPipelineHarness(t, nil)
	cold := harness.run(t, "false", "prompt-v1", nil)
	warm := harness.run(t, "false", "prompt-v1", cold.desired.All)

	assert.Equal(t, 1, harness.reader.callCount(), "Parse increment on warm run")
	assert.Equal(t, 2, harness.vlmProvider.callCount(), "OCR/Caption increment on warm run")
	assert.Equal(t, 1, harness.embeddingProvider.callCount(), "Embedding increment on warm run")
	for _, stage := range []string{
		"summary",
		"question",
		"wiki_map",
		"graph_extract.entities",
		"graph_extract.relationships",
	} {
		assert.Equal(t, 1, harness.chatProvider.callCount(stage), stage+" increment on warm run")
	}
	assert.Equal(t, 2, harness.chatProvider.callCount("wiki_reduce"), "Wiki Reduce must remain live")

	assert.Equal(t, cold.chunkIDs, warm.chunkIDs)
	assert.Equal(t, cold.vectors, warm.vectors)
	assert.Equal(t, cold.parseOutput, warm.parseOutput)
	assert.Equal(t, cold.ocrOutput, warm.ocrOutput)
	assert.Equal(t, cold.caption, warm.caption)
	assert.Equal(t, cold.summary, warm.summary)
	assert.Equal(t, cold.questions, warm.questions)
	assert.Equal(t, cold.wikiMap, warm.wikiMap)
	assert.Equal(t, cold.graphEntity, warm.graphEntity)
	assert.Equal(t, cold.graphEdge, warm.graphEdge)
	assert.Equal(t, cold.wikiReduce, warm.wikiReduce)
	assert.Empty(t, warm.desired.Added)
	assert.Empty(t, warm.desired.Stale)
	assert.Len(t, warm.desired.Updated, len(cold.desired.All))
	for _, vector := range warm.vectors {
		assert.Len(t, vector, 2)
	}
}

func TestArtifactPipelineLayeredInvalidationStopsAtCanonicalOutput(t *testing.T) {
	harness := newArtifactPipelineHarness(t, nil)
	cold := harness.run(t, "false", "prompt-v1", nil)

	// Parser options and both VLM prompts change, but their deterministic
	// canonical outputs remain identical. Direct stages recompute; consumers
	// continue to hit.
	changed := harness.run(t, "true", "prompt-v2", cold.desired.All)
	assert.Equal(t, 2, harness.reader.callCount())
	assert.Equal(t, 4, harness.vlmProvider.callCount())
	assert.Equal(t, 1, harness.embeddingProvider.callCount())
	for _, stage := range []string{
		"summary",
		"question",
		"wiki_map",
		"graph_extract.entities",
		"graph_extract.relationships",
	} {
		assert.Equal(t, 1, harness.chatProvider.callCount(stage))
	}
	assert.Equal(t, cold.chunkIDs, changed.chunkIDs)
	assert.Equal(t, cold.vectors, changed.vectors)
}

func TestArtifactPipelineStoreUnavailableCompletesFailOpen(t *testing.T) {
	harness := newArtifactPipelineHarness(t, artifact.NewRuntime(nil, nil))
	result := harness.run(t, "false", "prompt-v1", nil)

	assert.Len(t, result.chunkIDs, 2)
	assert.Len(t, result.vectors, 2)
	assert.Equal(t, 1, harness.reader.callCount())
	assert.Equal(t, 2, harness.vlmProvider.callCount())
	assert.Equal(t, 1, harness.embeddingProvider.callCount())
	assert.Equal(t, 1, harness.chatProvider.callCount("summary"))
	assert.Equal(t, 1, harness.chatProvider.callCount("question"))
	assert.Equal(t, 1, harness.chatProvider.callCount("wiki_map"))
	assert.Equal(t, 1, harness.chatProvider.callCount("graph_extract.entities"))
	assert.Equal(t, 1, harness.chatProvider.callCount("graph_extract.relationships"))
}
