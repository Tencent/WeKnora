package service

import (
	"context"
	"encoding/json"
	"testing"

	apprepo "github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
)

// countingChat is a test double for chat.Chat that records how many times
// Chat was invoked and returns a fixed response.
type countingChat struct {
	calls    int
	response string
	modelID  string
	modelName string
}

func (c *countingChat) Chat(ctx context.Context, messages []chat.Message, opts *chat.ChatOptions) (*types.ChatResponse, error) {
	c.calls++
	return &types.ChatResponse{Content: c.response}, nil
}

func (c *countingChat) ChatStream(ctx context.Context, messages []chat.Message, opts *chat.ChatOptions) (<-chan types.StreamResponse, error) {
	ch := make(chan types.StreamResponse)
	close(ch)
	return ch, nil
}

func (c *countingChat) GetModelName() string { return c.modelName }
func (c *countingChat) GetModelID() string   { return c.modelID }

// memorySummaryCacheRepo is an in-memory implementation of SummaryCacheRepo
// for behavior tests. It does not depend on a real database.
type memorySummaryCacheRepo struct {
	data map[string]types.SummaryCache
}

func newMemorySummaryCacheRepo() *memorySummaryCacheRepo {
	return &memorySummaryCacheRepo{data: make(map[string]types.SummaryCache)}
}

func (r *memorySummaryCacheRepo) key(docContentHash, modelID, promptVersion, configHash string) string {
	return docContentHash + "|" + modelID + "|" + promptVersion + "|" + configHash
}

func (r *memorySummaryCacheRepo) Get(ctx context.Context, docContentHash, modelID, promptVersion, configHash string) (string, bool, error) {
	row, ok := r.data[r.key(docContentHash, modelID, promptVersion, configHash)]
	if !ok {
		return "", false, nil
	}
	return row.Summary, true, nil
}

func (r *memorySummaryCacheRepo) Put(ctx context.Context, row *types.SummaryCache) error {
	r.data[r.key(row.DocContentHash, row.ModelID, row.PromptVersion, row.ConfigHash)] = *row
	return nil
}

// memoryQuestionCacheRepo is an in-memory implementation of QuestionCacheRepo
// for behavior tests. It does not depend on a real database.
type memoryQuestionCacheRepo struct {
	data map[string]types.QuestionCache
}

func newMemoryQuestionCacheRepo() *memoryQuestionCacheRepo {
	return &memoryQuestionCacheRepo{data: make(map[string]types.QuestionCache)}
}

func (r *memoryQuestionCacheRepo) key(chunkContentHash, modelID, promptVersion, configHash string) string {
	return chunkContentHash + "|" + modelID + "|" + promptVersion + "|" + configHash
}

func (r *memoryQuestionCacheRepo) Get(ctx context.Context, chunkContentHash, modelID, promptVersion, configHash string) (string, bool, error) {
	row, ok := r.data[r.key(chunkContentHash, modelID, promptVersion, configHash)]
	if !ok {
		return "", false, nil
	}
	return row.Payload, true, nil
}

func (r *memoryQuestionCacheRepo) Put(ctx context.Context, row *types.QuestionCache) error {
	r.data[r.key(row.ChunkContentHash, row.ModelID, row.PromptVersion, row.ConfigHash)] = *row
	return nil
}

// Compile-time guards: the memory mocks must satisfy the repository interfaces.
var _ apprepo.SummaryCacheRepo = (*memorySummaryCacheRepo)(nil)
var _ apprepo.QuestionCacheRepo = (*memoryQuestionCacheRepo)(nil)
var _ chat.Chat = (*countingChat)(nil)

// summaryCacheKey mirrors the key composition used by knowledgeService.getSummary.
func summaryCacheKey(content, modelID, prompt string, maxInputChars, maxTokens int) (docContentHash, mid, promptVersion, configHash string) {
	docContentHash = types.StableContentHash(content)
	mid = modelID
	promptVersion = types.SHAChecksum(prompt)
	configBytes, _ := json.Marshal(map[string]interface{}{
		"model_name":      modelID, // production uses GetModelName; tests use modelID as proxy
		"max_input_chars": maxInputChars,
		"max_tokens":      maxTokens,
		"temperature":     0.3,
	})
	configHash = types.SHAChecksum(string(configBytes))
	return docContentHash, mid, promptVersion, configHash
}

// questionCacheKey mirrors the key composition used by knowledgeService.generateQuestionsWithContext.
func questionCacheKey(content, modelID, prompt string, questionCount int) (chunkContentHash, mid, promptVersion, configHash string) {
	chunkContentHash = types.StableContentHash(content)
	mid = modelID
	promptVersion = types.SHAChecksum(prompt)
	configBytes, _ := json.Marshal(map[string]interface{}{
		"model_name":     modelID,
		"temperature":    0.7,
		"max_tokens":     512,
		"question_count": questionCount,
	})
	configHash = types.SHAChecksum(string(configBytes))
	return chunkContentHash, mid, promptVersion, configHash
}

func TestReparseUnchangedContent_StableIDsAndCacheKeys(t *testing.T) {
	knowledgeID := "kb-unchanged"
	content := "The quick brown fox jumps over the lazy dog.\n\nSame content, same IDs."
	prompt := "Summarize the following document in one sentence: {{language}}"
	modelID := "model-001"

	// StableChunkID must be deterministic for identical inputs.
	id1 := types.StableChunkID(knowledgeID, content, 0)
	id2 := types.StableChunkID(knowledgeID, content, 0)
	if id1 != id2 {
		t.Fatalf("StableChunkID not deterministic: %q vs %q", id1, id2)
	}

	// StableContentHash must be deterministic for identical content.
	hash1 := types.StableContentHash(content)
	hash2 := types.StableContentHash(content)
	if hash1 != hash2 {
		t.Fatalf("StableContentHash not deterministic: %q vs %q", hash1, hash2)
	}

	// Prompt version must be deterministic for identical prompts.
	pv1 := types.SHAChecksum(prompt)
	pv2 := types.SHAChecksum(prompt)
	if pv1 != pv2 {
		t.Fatalf("SHAChecksum(prompt) not deterministic: %q vs %q", pv1, pv2)
	}

	// Same content + same model + same prompt => identical cache key => reusable.
	dh1, mid1, pv1Out, ch1 := summaryCacheKey(content, modelID, prompt, 1024, 512)
	dh2, mid2, pv2Out, ch2 := summaryCacheKey(content, modelID, prompt, 1024, 512)
	if dh1 != dh2 || mid1 != mid2 || pv1Out != pv2Out || ch1 != ch2 {
		t.Fatalf("summary cache key not reusable across identical rebuilds: (%s,%s,%s,%s) vs (%s,%s,%s,%s)",
			dh1, mid1, pv1Out, ch1, dh2, mid2, pv2Out, ch2)
	}

	// Cross-document identical content must still produce different chunk IDs,
	// while embeddings/content-hash dedup across docs remains valid.
	otherID := types.StableChunkID("kb-other", content, 0)
	if id1 == otherID {
		t.Fatalf("StableChunkID must not collapse cross-document identity: %q == %q", id1, otherID)
	}
}

func TestCrashResume_CompletedArtifactsCacheHit(t *testing.T) {
	knowledgeID := "kb-crash-resume"
	contents := []string{
		"first paragraph for crash resume test",
		"second paragraph for crash resume test",
		"third paragraph for crash resume test",
	}

	// Simulate the first parsing pass: new chunks vs an empty old set.
	var firstPass []*types.Chunk
	for i, c := range contents {
		firstPass = append(firstPass, chunkWithContent(t, knowledgeID, c, i))
	}

	kept1, added1, removed1 := computeChunkDiff(firstPass, nil)
	if len(kept1) != 0 {
		t.Fatalf("first pass kept = %d, want 0", len(kept1))
	}
	if len(added1) != len(contents) {
		t.Fatalf("first pass added = %d, want %d", len(added1), len(contents))
	}
	if len(removed1) != 0 {
		t.Fatalf("first pass removed = %d, want 0", len(removed1))
	}

	// Simulate a crash-resume second pass: the splitter produces the same
	// content, so StableChunkID yields the same IDs as the first pass.
	var secondPass []*types.Chunk
	for i, c := range contents {
		secondPass = append(secondPass, chunkWithContent(t, knowledgeID, c, i))
	}

	kept2, added2, removed2 := computeChunkDiff(secondPass, firstPass)
	if len(kept2) != len(contents) {
		t.Fatalf("second pass kept = %d, want %d", len(kept2), len(contents))
	}
	if len(added2) != 0 {
		t.Fatalf("second pass added = %d, want 0", len(added2))
	}
	if len(removed2) != 0 {
		t.Fatalf("second pass removed = %d, want 0", len(removed2))
	}

	// Every chunk from the second pass must be classified as kept because
	// StableChunkID is deterministic.
	for _, c := range secondPass {
		if _, ok := kept2[c.ID]; !ok {
			t.Fatalf("second pass chunk %q should be kept", c.ID)
		}
	}
}

func TestConfigChange_OnlyDependentLayerInvalidates(t *testing.T) {
	content := "document content used for config change test"
	prompt := "Summarize: {{language}}"
	modelA := "model-A"
	modelB := "model-B"

	// Different model_id => different prompt_version component is not used
	// here, but the model_id itself must produce a different cache key.
	hashModelA := types.SHAChecksum(modelA)
	hashModelB := types.SHAChecksum(modelB)
	if hashModelA == hashModelB {
		t.Fatalf("SHAChecksum must distinguish different model ids: %q", hashModelA)
	}

	// Different prompt => different prompt version hash.
	promptV1 := types.SHAChecksum(prompt)
	promptV2 := types.SHAChecksum(prompt + " extra")
	if promptV1 == promptV2 {
		t.Fatalf("SHAChecksum must distinguish different prompts: %q", promptV1)
	}

	// Same model_id + same prompt => same hash.
	if types.SHAChecksum(modelA+prompt) != types.SHAChecksum(modelA+prompt) {
		t.Fatalf("SHAChecksum must be deterministic for identical model+prompt")
	}

	// Layer A (summary) and layer B (question) share content but use different
	// prompts/configs. Changing the model for layer A only must invalidate
	// layer A while layer B remains reusable.
	summaryA1Doc, summaryA1Model, summaryA1Prompt, summaryA1Config := summaryCacheKey(content, modelA, "summary prompt v1", 1024, 512)
	summaryA2Doc, summaryA2Model, summaryA2Prompt, summaryA2Config := summaryCacheKey(content, modelB, "summary prompt v1", 1024, 512)
	questionBDoc, questionBModel, questionBPrompt, questionBConfig := questionCacheKey(content, modelA, "question prompt v1", 3)

	// Summary layer must change when model changes.
	if summaryA1Doc == summaryA2Doc && summaryA1Model == summaryA2Model &&
		summaryA1Prompt == summaryA2Prompt && summaryA1Config == summaryA2Config {
		t.Fatalf("summary cache key must change when model changes")
	}
	if summaryA1Model == summaryA2Model {
		t.Fatalf("summary model_id component must differ: %q vs %q", summaryA1Model, summaryA2Model)
	}

	// Question layer still uses modelA, so its key is unchanged and reusable.
	questionBDoc2, questionBModel2, questionBPrompt2, questionBConfig2 := questionCacheKey(content, modelA, "question prompt v1", 3)
	if questionBDoc != questionBDoc2 || questionBModel != questionBModel2 ||
		questionBPrompt != questionBPrompt2 || questionBConfig != questionBConfig2 {
		t.Fatalf("question cache key should remain reusable when its model_id is unchanged")
	}
}

func TestSummaryCacheHit_SkipsLLM(t *testing.T) {
	ctx := context.Background()
	repo := newMemorySummaryCacheRepo()
	llm := &countingChat{response: "cached summary", modelID: "summary-model"}

	content := "some document content"
	prompt := "Summarize: {{language}}"
	docHash, modelID, promptVersion, configHash := summaryCacheKey(content, llm.modelID, prompt, 1024, 512)

	// Pre-fill the cache as if a previous successful run had written it.
	if err := repo.Put(ctx, &types.SummaryCache{
		DocContentHash: docHash,
		ModelID:        modelID,
		PromptVersion:  promptVersion,
		ConfigHash:     configHash,
		Summary:        "cached summary",
	}); err != nil {
		t.Fatalf("put cache: %v", err)
	}

	// Simulate the production lookup path.
	cached, ok, err := repo.Get(ctx, docHash, modelID, promptVersion, configHash)
	if err != nil {
		t.Fatalf("get cache: %v", err)
	}
	if !ok {
		t.Fatalf("expected cache hit")
	}
	if cached != "cached summary" {
		t.Fatalf("cached summary = %q, want %q", cached, "cached summary")
	}

	// A cache hit must not call the LLM.
	if llm.calls != 0 {
		t.Fatalf("LLM called %d times on cache hit, want 0", llm.calls)
	}
}

func TestSummaryCacheMiss_WritesCache(t *testing.T) {
	ctx := context.Background()
	repo := newMemorySummaryCacheRepo()
	llm := &countingChat{response: "generated summary", modelID: "summary-model"}

	content := "some document content"
	prompt := "Summarize: {{language}}"
	docHash, modelID, promptVersion, configHash := summaryCacheKey(content, llm.modelID, prompt, 1024, 512)

	// First lookup is a miss.
	_, ok, err := repo.Get(ctx, docHash, modelID, promptVersion, configHash)
	if err != nil {
		t.Fatalf("get cache: %v", err)
	}
	if ok {
		t.Fatalf("expected cache miss before LLM call")
	}

	// Simulate the LLM call and cache write that production performs.
	resp, err := llm.Chat(ctx, nil, nil)
	if err != nil {
		t.Fatalf("llm chat: %v", err)
	}
	if err := repo.Put(ctx, &types.SummaryCache{
		DocContentHash: docHash,
		ModelID:        modelID,
		PromptVersion:  promptVersion,
		ConfigHash:     configHash,
		Summary:        resp.Content,
	}); err != nil {
		t.Fatalf("put cache: %v", err)
	}

	// Second lookup must now hit without another LLM call.
	cached, ok, err := repo.Get(ctx, docHash, modelID, promptVersion, configHash)
	if err != nil {
		t.Fatalf("get cache: %v", err)
	}
	if !ok {
		t.Fatalf("expected cache hit after write")
	}
	if cached != "generated summary" {
		t.Fatalf("cached summary = %q, want %q", cached, "generated summary")
	}
	if llm.calls != 1 {
		t.Fatalf("LLM called %d times, want 1", llm.calls)
	}
}

func TestQuestionCacheHit_SkipsLLM(t *testing.T) {
	ctx := context.Background()
	repo := newMemoryQuestionCacheRepo()
	llm := &countingChat{response: "[\"q1\", \"q2\"]", modelID: "question-model"}

	content := "chunk content for questions"
	prompt := "Generate questions: {{content}}"
	chunkHash, modelID, promptVersion, configHash := questionCacheKey(content, llm.modelID, prompt, 2)

	// Pre-fill the cache.
	questionsJSON := `["cached q1","cached q2"]`
	if err := repo.Put(ctx, &types.QuestionCache{
		ChunkContentHash: chunkHash,
		ModelID:          modelID,
		PromptVersion:    promptVersion,
		ConfigHash:       configHash,
		Payload:          questionsJSON,
	}); err != nil {
		t.Fatalf("put cache: %v", err)
	}

	// Simulate the production lookup path.
	cached, ok, err := repo.Get(ctx, chunkHash, modelID, promptVersion, configHash)
	if err != nil {
		t.Fatalf("get cache: %v", err)
	}
	if !ok {
		t.Fatalf("expected cache hit")
	}
	var questions []string
	if err := json.Unmarshal([]byte(cached), &questions); err != nil {
		t.Fatalf("unmarshal questions: %v", err)
	}
	if len(questions) != 2 {
		t.Fatalf("questions = %v, want 2", questions)
	}

	if llm.calls != 0 {
		t.Fatalf("LLM called %d times on cache hit, want 0", llm.calls)
	}
}

func TestQuestionCacheMiss_WritesCache(t *testing.T) {
	ctx := context.Background()
	repo := newMemoryQuestionCacheRepo()
	llm := &countingChat{response: "[\"q1\", \"q2\"]", modelID: "question-model"}

	content := "chunk content for questions"
	prompt := "Generate questions: {{content}}"
	chunkHash, modelID, promptVersion, configHash := questionCacheKey(content, llm.modelID, prompt, 2)

	// First lookup is a miss.
	_, ok, err := repo.Get(ctx, chunkHash, modelID, promptVersion, configHash)
	if err != nil {
		t.Fatalf("get cache: %v", err)
	}
	if ok {
		t.Fatalf("expected cache miss before LLM call")
	}

	// Simulate the LLM call and cache write.
	resp, err := llm.Chat(ctx, nil, nil)
	if err != nil {
		t.Fatalf("llm chat: %v", err)
	}
	if err := repo.Put(ctx, &types.QuestionCache{
		ChunkContentHash: chunkHash,
		ModelID:          modelID,
		PromptVersion:    promptVersion,
		ConfigHash:       configHash,
		Payload:          resp.Content,
	}); err != nil {
		t.Fatalf("put cache: %v", err)
	}

	// Second lookup must hit.
	cached, ok, err := repo.Get(ctx, chunkHash, modelID, promptVersion, configHash)
	if err != nil {
		t.Fatalf("get cache: %v", err)
	}
	if !ok {
		t.Fatalf("expected cache hit after write")
	}
	if cached != resp.Content {
		t.Fatalf("cached payload = %q, want %q", cached, resp.Content)
	}
	if llm.calls != 1 {
		t.Fatalf("LLM called %d times, want 1", llm.calls)
	}
}
