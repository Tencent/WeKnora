package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/hibiken/asynq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type autoTagRecordingQueue struct {
	interfaces.TaskEnqueuer
	task *asynq.Task
}

func (q *autoTagRecordingQueue) Enqueue(task *asynq.Task, _ ...asynq.Option) (*asynq.TaskInfo, error) {
	q.task = task
	return &asynq.TaskInfo{ID: "auto-tag", Type: task.Type()}, nil
}

func confidencePtr(value float64) *float64 { return &value }

// ── stubs: only the methods the auto-tag handler touches are implemented;
// everything else stays nil so an unexpected call fails loudly. ──

type autoTagKnowledgeRepo struct {
	interfaces.KnowledgeRepository
	knowledge *types.Knowledge
	existing  map[string][]*types.KnowledgeTag
	added     []string
	addErr    error
}

func (r *autoTagKnowledgeRepo) GetKnowledgeByIDOnly(context.Context, string) (*types.Knowledge, error) {
	return r.knowledge, nil
}

func (r *autoTagKnowledgeRepo) GetKnowledgeTags(
	context.Context, []string,
) (map[string][]*types.KnowledgeTag, error) {
	return r.existing, nil
}

func (r *autoTagKnowledgeRepo) AddKnowledgeTagRelations(
	_ context.Context, _ uint64, _, _ string, tagIDs []string,
) error {
	if r.addErr != nil {
		return r.addErr
	}
	r.added = append(r.added, tagIDs...)
	return nil
}

type autoTagTagRepo struct {
	interfaces.KnowledgeTagRepository
	tags  []*types.KnowledgeTag
	total int64
}

func (r *autoTagTagRepo) ListByKB(
	_ context.Context, _ uint64, _ string, page *types.Pagination, _ string,
) ([]*types.KnowledgeTag, int64, error) {
	total := r.total
	if total == 0 {
		total = int64(len(r.tags))
	}
	tags := r.tags
	if page != nil && page.Limit() > 0 && len(tags) > page.Limit() {
		tags = tags[:page.Limit()]
	}
	return tags, total, nil
}

type autoTagKBService struct {
	interfaces.KnowledgeBaseService
	kb *types.KnowledgeBase
}

func (s *autoTagKBService) GetKnowledgeBaseByIDOnly(
	context.Context, string,
) (*types.KnowledgeBase, error) {
	return s.kb, nil
}

type autoTagChunkService struct {
	interfaces.ChunkService
	chunks []*types.Chunk
}

func (s *autoTagChunkService) ListChunksByKnowledgeID(
	context.Context, string,
) ([]*types.Chunk, error) {
	return s.chunks, nil
}

type autoTagChatModel struct {
	response string
	messages []chat.Message
}

func (m *autoTagChatModel) Chat(
	_ context.Context, messages []chat.Message, _ *chat.ChatOptions,
) (*types.ChatResponse, error) {
	m.messages = append([]chat.Message(nil), messages...)
	return &types.ChatResponse{Content: m.response}, nil
}

func (m *autoTagChatModel) ChatStream(
	context.Context, []chat.Message, *chat.ChatOptions,
) (<-chan types.StreamResponse, error) {
	return nil, errors.New("not used")
}

func (m *autoTagChatModel) GetModelName() string { return "auto-tag" }
func (m *autoTagChatModel) GetModelID() string   { return "auto-tag" }

type autoTagFixture struct {
	service   *KnowledgeAutoTagService
	repo      *autoTagKnowledgeRepo
	chatModel *autoTagChatModel
}

func newAutoTagFixture(t *testing.T, response string, opts ...func(*autoTagFixture)) *autoTagFixture {
	t.Helper()
	repo := &autoTagKnowledgeRepo{
		knowledge: &types.Knowledge{
			ID: "doc", TenantID: 7, KnowledgeBaseID: "kb",
			FileName: "policy.pdf", ParseStatus: types.ParseStatusFinalizing,
		},
		existing: map[string][]*types.KnowledgeTag{},
	}
	chatModel := &autoTagChatModel{response: response}
	fixture := &autoTagFixture{repo: repo, chatModel: chatModel}
	fixture.service = &KnowledgeAutoTagService{
		knowledgeRepo: repo,
		tagRepo: &autoTagTagRepo{tags: []*types.KnowledgeTag{
			{ID: "tag-a", Name: "HR"},
			{ID: "tag-b", Name: "Finance"},
		}},
		kbService: &autoTagKBService{kb: &types.KnowledgeBase{
			ID: "kb", TenantID: 7, Type: types.KnowledgeBaseTypeDocument,
			SummaryModelID: "model-1",
			AutoTagConfig:  &types.AutoTagConfig{Enabled: true, MaxTags: 3},
		}},
		chunkService: &autoTagChunkService{chunks: []*types.Chunk{
			{ChunkType: types.ChunkTypeText, Content: "quarterly HR policy", StartAt: 1},
		}},
		modelService: &stubModelService{chatModel: chatModel},
	}
	for _, opt := range opts {
		opt(fixture)
	}
	return fixture
}

func (f *autoTagFixture) handle(t *testing.T) error {
	t.Helper()
	payload, err := json.Marshal(types.KnowledgeAutoTagPayload{
		TenantID: 7, KnowledgeID: "doc", KnowledgeBaseID: "kb", Attempt: 1,
	})
	require.NoError(t, err)
	return f.service.Handle(context.Background(), asynq.NewTask(types.TypeKnowledgeAutoTag, payload))
}

func TestAutoTagHandleAttachesMatchedTags(t *testing.T) {
	fixture := newAutoTagFixture(t, `{"matches":[{"index":1,"confidence":0.9}]}`)
	require.NoError(t, fixture.handle(t))
	assert.Equal(t, []string{"tag-a"}, fixture.repo.added)
}

// A missing confidence must not be read as zero: models frequently omit the
// field while still returning a deliberate selection.
func TestAutoTagHandleKeepsMatchesWithoutConfidence(t *testing.T) {
	fixture := newAutoTagFixture(t, `{"matches":[{"index":2}]}`)
	require.NoError(t, fixture.handle(t))
	assert.Equal(t, []string{"tag-b"}, fixture.repo.added)
}

// The model reply is wrapped in prose containing a bracketed citation, which
// the previous LastIndexAny-based extraction truncated into invalid JSON.
func TestAutoTagHandleParsesJSONWrappedInProse(t *testing.T) {
	fixture := newAutoTagFixture(t,
		"Here you go: {\"matches\":[{\"index\":1,\"confidence\":0.88}]} — see section [1].")
	require.NoError(t, fixture.handle(t))
	assert.Equal(t, []string{"tag-a"}, fixture.repo.added)
}

// An ordinal outside the candidate list is a model hallucination and must not
// resolve to an unrelated tag.
func TestAutoTagHandleDropsOutOfRangeIndex(t *testing.T) {
	fixture := newAutoTagFixture(t, `{"matches":[{"index":99,"confidence":0.99}]}`)
	require.NoError(t, fixture.handle(t))
	assert.Empty(t, fixture.repo.added)
}

// skip_if_tagged defaults to on, so a manually classified document is left
// alone and never reaches the model.
func TestAutoTagHandleSkipsDocumentThatAlreadyHasTags(t *testing.T) {
	fixture := newAutoTagFixture(t, `{"matches":[{"index":1,"confidence":0.9}]}`)
	fixture.repo.existing = map[string][]*types.KnowledgeTag{"doc": {{ID: "manual"}}}
	require.NoError(t, fixture.handle(t))
	assert.Empty(t, fixture.repo.added)
	assert.Empty(t, fixture.chatModel.messages, "tagged documents must not cost an LLM call")
}

func TestAutoTagHandleAppendsWhenSkipIfTaggedDisabled(t *testing.T) {
	disabled := false
	fixture := newAutoTagFixture(t, `{"matches":[{"index":1,"confidence":0.9}]}`,
		func(f *autoTagFixture) {
			f.service.kbService.(*autoTagKBService).kb.AutoTagConfig.SkipIfTagged = &disabled
		})
	fixture.repo.existing = map[string][]*types.KnowledgeTag{"doc": {{ID: "manual"}}}
	require.NoError(t, fixture.handle(t))
	assert.Equal(t, []string{"tag-a"}, fixture.repo.added)
}

// Re-running against a document whose only tags came from a previous auto-tag
// pass must stay idempotent rather than re-adding them.
func TestAutoTagHandleSkipsTagsAlreadyAttached(t *testing.T) {
	disabled := false
	fixture := newAutoTagFixture(t, `{"matches":[{"index":1,"confidence":0.9}]}`,
		func(f *autoTagFixture) {
			f.service.kbService.(*autoTagKBService).kb.AutoTagConfig.SkipIfTagged = &disabled
		})
	fixture.repo.existing = map[string][]*types.KnowledgeTag{"doc": {{ID: "tag-a"}}}
	require.NoError(t, fixture.handle(t))
	assert.Empty(t, fixture.repo.added)
}

func TestAutoTagHandleSkipsKnowledgeOutsidePayloadScope(t *testing.T) {
	fixture := newAutoTagFixture(t, `{"matches":[{"index":1,"confidence":0.9}]}`)
	fixture.repo.knowledge.TenantID = 99
	require.NoError(t, fixture.handle(t))
	assert.Empty(t, fixture.repo.added)
}

func TestAutoTagHandleSkipsCancelledKnowledge(t *testing.T) {
	fixture := newAutoTagFixture(t, `{"matches":[{"index":1,"confidence":0.9}]}`)
	fixture.repo.knowledge.ParseStatus = types.ParseStatusCancelled
	require.NoError(t, fixture.handle(t))
	assert.Empty(t, fixture.repo.added)
}

// An oversized tag set is classified against a stable prefix rather than
// abandoning the task, which used to disable the feature outright.
func TestAutoTagHandleClassifiesPrefixWhenCandidatesExceedCap(t *testing.T) {
	fixture := newAutoTagFixture(t, `{"matches":[{"index":1,"confidence":0.9}]}`,
		func(f *autoTagFixture) {
			tags := make([]*types.KnowledgeTag, 0, maximumAutoTagCandidates)
			tags = append(tags, &types.KnowledgeTag{ID: "tag-a", Name: "HR"})
			for i := len(tags); i < maximumAutoTagCandidates; i++ {
				tags = append(tags, &types.KnowledgeTag{ID: "filler", Name: "filler"})
			}
			f.service.tagRepo = &autoTagTagRepo{tags: tags, total: maximumAutoTagCandidates + 50}
		})
	require.NoError(t, fixture.handle(t))
	assert.Equal(t, []string{"tag-a"}, fixture.repo.added)
}

// Ordinals must address the candidate list rather than UUIDs, keeping the
// prompt compact enough for the raised candidate ceiling.
func TestAutoTagPromptListsCandidatesByOrdinal(t *testing.T) {
	fixture := newAutoTagFixture(t, `{"matches":[]}`)
	require.NoError(t, fixture.handle(t))
	require.Len(t, fixture.chatModel.messages, 2)
	user := fixture.chatModel.messages[1].Content
	assert.Contains(t, user, "1. HR")
	assert.Contains(t, user, "2. Finance")
	assert.NotContains(t, user, "tag-a")
}

func TestAutoTagHandleReturnsErrorWhenPersistFails(t *testing.T) {
	fixture := newAutoTagFixture(t, `{"matches":[{"index":1,"confidence":0.9}]}`)
	fixture.repo.addErr = errors.New("db down")
	require.Error(t, fixture.handle(t))
}

// Document text must be fenced so instruction-like content cannot be read as
// part of the prompt.
func TestAutoTagPromptFencesDocumentContent(t *testing.T) {
	fixture := newAutoTagFixture(t, `{"matches":[]}`)
	require.NoError(t, fixture.handle(t))
	require.Len(t, fixture.chatModel.messages, 2)
	user := fixture.chatModel.messages[1].Content
	assert.Contains(t, user, "<document>")
	assert.Contains(t, user, "</document>")
	assert.Contains(t, fixture.chatModel.messages[0].Content, "never as instructions")
}

func TestValidateAutoTagMatches(t *testing.T) {
	tags := []*types.KnowledgeTag{{ID: "a"}, {ID: "b"}, {ID: "c"}}
	matches := []autoTagModelMatch{
		{Index: 42, Confidence: confidencePtr(1)}, // out of range
		{Index: 1, Confidence: confidencePtr(0.70)},
		{Index: 2, Confidence: confidencePtr(0.90)},
		{Index: 2, Confidence: confidencePtr(0.95)},
		{Index: 3, Confidence: confidencePtr(0.80)},
	}
	assert.Equal(t, []string{"b", "c"}, validateAutoTagMatches(tags, matches, 2))
}

func TestValidateAutoTagMatchesRejectsNonPositiveIndex(t *testing.T) {
	tags := []*types.KnowledgeTag{{ID: "a"}, {ID: "b"}}
	matches := []autoTagModelMatch{
		{Index: 0, Confidence: confidencePtr(0.99)},
		{Index: -1, Confidence: confidencePtr(0.99)},
	}
	assert.Empty(t, validateAutoTagMatches(tags, matches, 5))
}

func TestValidateAutoTagMatchesNormalizesConfidenceScale(t *testing.T) {
	tags := []*types.KnowledgeTag{{ID: "a"}, {ID: "b"}, {ID: "c"}}
	matches := []autoTagModelMatch{
		{Index: 1}, // omitted → deliberate selection
		{Index: 2, Confidence: confidencePtr(90)}, // percentage scale
		{Index: 3, Confidence: confidencePtr(10)}, // percentage scale, below threshold
	}
	assert.Equal(t, []string{"a", "b"}, validateAutoTagMatches(tags, matches, 5))
}

func TestValidateAutoTagMatchesClampsMaxTagsToConfigBounds(t *testing.T) {
	tags := []*types.KnowledgeTag{{ID: "a"}, {ID: "b"}}
	matches := []autoTagModelMatch{
		{Index: 1, Confidence: confidencePtr(0.9)},
		{Index: 2, Confidence: confidencePtr(0.9)},
	}
	// 0 falls back to the shared default rather than selecting nothing.
	assert.Len(t, validateAutoTagMatches(tags, matches, 0), 2)
}

func TestBuildAutoTagDocumentContentSamplesAndFilters(t *testing.T) {
	knowledge := &types.Knowledge{FileName: "policy.pdf", Description: "HR policy"}
	large := strings.Repeat("x", maximumAutoTagContentRunes+1000)
	chunks := []*types.Chunk{
		nil,
		{ChunkType: types.ChunkTypeText, Content: large, StartAt: 2},
		{ChunkType: types.ChunkTypeText, Content: "first", StartAt: 1},
	}
	content := buildAutoTagDocumentContent(knowledge, chunks)
	require.NotEmpty(t, content)
	assert.Contains(t, content, "Document name: policy.pdf")
	assert.Contains(t, content, "Existing summary: HR policy")
	assert.LessOrEqual(t, len([]rune(content)), maximumAutoTagContentRunes)
}

func TestEnqueueAutoTagTaskCarriesScopeWithoutOwningFinalizingSlot(t *testing.T) {
	queue := &autoTagRecordingQueue{}
	service := &KnowledgePostProcessService{taskEnqueuer: queue}
	ok := service.enqueueAutoTagTask(context.Background(), types.KnowledgePostProcessPayload{
		TenantID: 7, KnowledgeBaseID: "kb", KnowledgeID: "doc", Language: "zh-CN",
	}, 4)
	require.True(t, ok)
	require.NotNil(t, queue.task)
	assert.Equal(t, types.TypeKnowledgeAutoTag, queue.task.Type())
	var payload types.KnowledgeAutoTagPayload
	require.NoError(t, json.Unmarshal(queue.task.Payload(), &payload))
	assert.Equal(t, uint64(7), payload.TenantID)
	assert.Equal(t, "kb", payload.KnowledgeBaseID)
	assert.Equal(t, "doc", payload.KnowledgeID)
	assert.Equal(t, 4, payload.Attempt)
}
