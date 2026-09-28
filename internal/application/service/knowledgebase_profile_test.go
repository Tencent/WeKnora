package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/hibiken/asynq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ── stubs: only what the profile service touches. ──

type kbProfileKBRepo struct {
	interfaces.KnowledgeBaseRepository
	kb        *types.KnowledgeBase
	saved     []*types.KnowledgeBaseProfile
	updateErr error
}

func (r *kbProfileKBRepo) GetKnowledgeBaseByIDAndTenant(
	_ context.Context, id string, tenantID uint64,
) (*types.KnowledgeBase, error) {
	if r.kb == nil || r.kb.ID != id || r.kb.TenantID != tenantID {
		return nil, errors.New("knowledge base not found")
	}
	return r.kb, nil
}

func (r *kbProfileKBRepo) UpdateKnowledgeBaseGeneratedProfile(
	_ context.Context, _ string, profile *types.KnowledgeBaseProfile,
) error {
	if r.updateErr != nil {
		return r.updateErr
	}
	r.saved = append(r.saved, profile)
	return nil
}

type kbProfileKnowledgeRepo struct {
	interfaces.KnowledgeRepository
	rows    []*types.KnowledgeProfileRow
	tags    map[string][]*types.KnowledgeTag
	listErr error
	calls   int
}

func (r *kbProfileKnowledgeRepo) ListKnowledgeProfileRows(
	context.Context, uint64, string,
) ([]*types.KnowledgeProfileRow, error) {
	r.calls++
	if r.listErr != nil {
		return nil, r.listErr
	}
	return r.rows, nil
}

func (r *kbProfileKnowledgeRepo) GetKnowledgeTags(
	context.Context, []string,
) (map[string][]*types.KnowledgeTag, error) {
	return r.tags, nil
}

type kbProfileChatModel struct {
	response string
	err      error
	calls    int
	messages []chat.Message
}

func (m *kbProfileChatModel) Chat(
	_ context.Context, messages []chat.Message, _ *chat.ChatOptions,
) (*types.ChatResponse, error) {
	m.calls++
	m.messages = append([]chat.Message(nil), messages...)
	if m.err != nil {
		return nil, m.err
	}
	return &types.ChatResponse{Content: m.response}, nil
}

func (m *kbProfileChatModel) ChatStream(
	context.Context, []chat.Message, *chat.ChatOptions,
) (<-chan types.StreamResponse, error) {
	return nil, errors.New("not used")
}

func (m *kbProfileChatModel) GetModelName() string { return "kb-profile" }
func (m *kbProfileChatModel) GetModelID() string   { return "kb-profile" }

type kbProfileQueue struct {
	interfaces.TaskEnqueuer
	tasks []*asynq.Task
	opts  [][]asynq.Option
	err   error
}

func (q *kbProfileQueue) Enqueue(task *asynq.Task, opts ...asynq.Option) (*asynq.TaskInfo, error) {
	if q.err != nil {
		return nil, q.err
	}
	q.tasks = append(q.tasks, task)
	q.opts = append(q.opts, opts)
	return &asynq.TaskInfo{ID: "t", Type: task.Type()}, nil
}

type kbProfileFixture struct {
	service   *KnowledgeBaseProfileService
	kbRepo    *kbProfileKBRepo
	docRepo   *kbProfileKnowledgeRepo
	chatModel *kbProfileChatModel
	queue     *kbProfileQueue
	kb        *types.KnowledgeBase
}

func newKBProfileFixture(response string) *kbProfileFixture {
	kb := &types.KnowledgeBase{
		ID: "kb", TenantID: 7, Name: "Ops handbook", Type: types.KnowledgeBaseTypeDocument,
		SummaryModelID: "model-1",
		ProfileConfig:  &types.KnowledgeBaseProfileConfig{Enabled: true},
	}
	rows := []*types.KnowledgeProfileRow{
		{ID: "d1", Title: "Cluster setup", FileType: "pdf", CreatedAt: time.Now(), Profile: &types.KnowledgeProfile{
			Gist: "Setting up a cluster", Topics: []string{"Kubernetes"}, DocType: "user manual",
			TypicalQuestion: "How do I set up a cluster?",
		}},
		{ID: "d2", Title: "Ingress guide", FileType: "md", CreatedAt: time.Now(), Profile: &types.KnowledgeProfile{
			Gist: "Ingress routing", Topics: []string{"K8s", "Ingress"}, DocType: "user manual",
			TypicalQuestion: "How does ingress routing work?",
		}},
	}
	chatModel := &kbProfileChatModel{response: response}
	f := &kbProfileFixture{
		kbRepo: &kbProfileKBRepo{kb: kb},
		docRepo: &kbProfileKnowledgeRepo{
			rows: rows,
			tags: map[string][]*types.KnowledgeTag{"d1": {{ID: "t", Name: "Ops"}}},
		},
		chatModel: chatModel,
		queue:     &kbProfileQueue{},
		kb:        kb,
	}
	f.service = &KnowledgeBaseProfileService{
		kbRepo:        f.kbRepo,
		knowledgeRepo: f.docRepo,
		modelService:  &stubModelService{chatModel: chatModel},
		taskEnqueuer:  f.queue,
	}
	return f
}

const kbProfileResponse = `{"gist":"Operations guides for running Kubernetes clusters.",` +
	`"topics":["Kubernetes","Ingress"],` +
	`"typical_questions":["How do I set up a cluster?","How does ingress routing work?"]}`

func TestKnowledgeBaseProfileGenerateProducesDescriptionFromAggregate(t *testing.T) {
	f := newKBProfileFixture("```json\n" + kbProfileResponse + "\n```")
	profile, err := f.service.GenerateKnowledgeBaseProfile(context.Background(), f.kb, false)
	require.NoError(t, err)
	require.NotNil(t, profile)
	assert.Equal(t, types.KnowledgeBaseProfileStatusReady, profile.Status)
	assert.Equal(t, "Operations guides for running Kubernetes clusters.", profile.Gist)
	assert.Equal(t, []string{"Kubernetes", "Ingress"}, profile.Topics)
	assert.Len(t, profile.TypicalQuestions, 2)
	assert.Equal(t, 2, profile.Stats.DocumentCount)
	assert.Equal(t, 2, profile.Stats.ProfiledCount)
	assert.Equal(t, []types.NamedCount{{Name: "Ops", Count: 1}}, profile.Stats.Tags)
	assert.Equal(t, "model-1", profile.ModelID)
	assert.NotEmpty(t, profile.AggregateHash)
	assert.Same(t, profile, f.kb.GeneratedProfile, "the in-memory KB mirrors what was persisted")
	require.Len(t, f.kbRepo.saved, 1)

	require.Len(t, f.chatModel.messages, 2)
	user := f.chatModel.messages[1].Content
	assert.Contains(t, user, "Knowledge base name: Ops handbook")
	assert.Contains(t, user, "Documents: 2 (with document profile: 2)")
	assert.Contains(t, user, "Kubernetes (1)")
	assert.Contains(t, user, "K8s (1)")
	assert.Contains(t, user, "Tags (documents per tag): Ops (1)")
	assert.Contains(t, user, "- How do I set up a cluster?")
	assert.Contains(t, f.chatModel.messages[0].Content, "strict JSON")
}

func TestKnowledgeBaseProfileGenerateSkipsModelWhenAggregateUnchanged(t *testing.T) {
	f := newKBProfileFixture(kbProfileResponse)
	first, err := f.service.GenerateKnowledgeBaseProfile(context.Background(), f.kb, false)
	require.NoError(t, err)
	again, err := f.service.GenerateKnowledgeBaseProfile(context.Background(), f.kb, false)
	require.NoError(t, err)
	assert.Same(t, first, again)
	assert.Equal(t, 1, f.chatModel.calls, "an unchanged aggregate must not cost a model call")
	assert.Equal(t, 2, f.docRepo.calls, "the aggregation itself still runs")

	forced, err := f.service.GenerateKnowledgeBaseProfile(context.Background(), f.kb, true)
	require.NoError(t, err)
	assert.NotSame(t, first, forced)
	assert.Equal(t, 2, f.chatModel.calls, "force bypasses the hash short circuit")
}

func TestKnowledgeBaseProfileGenerateRegeneratesAfterDeletion(t *testing.T) {
	f := newKBProfileFixture(kbProfileResponse)
	_, err := f.service.GenerateKnowledgeBaseProfile(context.Background(), f.kb, false)
	require.NoError(t, err)
	f.docRepo.rows = f.docRepo.rows[:1]
	profile, err := f.service.GenerateKnowledgeBaseProfile(context.Background(), f.kb, false)
	require.NoError(t, err)
	assert.Equal(t, 2, f.chatModel.calls, "removing a document changes the aggregate hash")
	assert.Equal(t, 1, profile.Stats.DocumentCount)
	assert.Equal(t, []types.NamedCount{{Name: "Kubernetes", Count: 1}}, profile.Stats.RawTopics,
		"the deleted document's topics are gone without any model judgement")
}

func TestKnowledgeBaseProfileGenerateClearsEmptyKnowledgeBase(t *testing.T) {
	f := newKBProfileFixture(kbProfileResponse)
	f.kb.GeneratedProfile = &types.KnowledgeBaseProfile{Gist: "old", Status: types.KnowledgeBaseProfileStatusReady}
	f.docRepo.rows = nil
	profile, err := f.service.GenerateKnowledgeBaseProfile(context.Background(), f.kb, false)
	require.NoError(t, err)
	assert.Equal(t, types.KnowledgeBaseProfileStatusEmpty, profile.Status)
	assert.Empty(t, profile.Gist)
	assert.Equal(t, 0, f.chatModel.calls)
}

func TestKnowledgeBaseProfileGenerateKeepsTextOnModelFailure(t *testing.T) {
	f := newKBProfileFixture(kbProfileResponse)
	f.kb.GeneratedProfile = &types.KnowledgeBaseProfile{
		Gist: "previous gist", Status: types.KnowledgeBaseProfileStatusReady, AggregateHash: "stale",
	}
	f.chatModel.err = errors.New("upstream 503")
	_, err := f.service.GenerateKnowledgeBaseProfile(context.Background(), f.kb, false)
	require.Error(t, err)
	require.NotNil(t, f.kb.GeneratedProfile)
	assert.Equal(t, types.KnowledgeBaseProfileStatusFailed, f.kb.GeneratedProfile.Status)
	assert.Equal(t, "previous gist", f.kb.GeneratedProfile.Gist)
	assert.Contains(t, f.kb.GeneratedProfile.Error, "upstream 503")
	assert.Equal(t, 2, f.kb.GeneratedProfile.Stats.DocumentCount)
}

func TestKnowledgeBaseProfileGenerateRejectsUnparseableOutput(t *testing.T) {
	f := newKBProfileFixture("Sure! This knowledge base is about clusters.")
	_, err := f.service.GenerateKnowledgeBaseProfile(context.Background(), f.kb, false)
	require.Error(t, err)
	assert.Equal(t, types.KnowledgeBaseProfileStatusFailed, f.kb.GeneratedProfile.Status)
}

func TestKnowledgeBaseProfileGenerateModelResolution(t *testing.T) {
	f := newKBProfileFixture(kbProfileResponse)
	f.kb.SummaryModelID = ""
	_, err := f.service.GenerateKnowledgeBaseProfile(context.Background(), f.kb, false)
	require.ErrorIs(t, err, ErrKnowledgeBaseProfileModelNotConfigured)
	assert.Equal(t, types.KnowledgeBaseProfileStatusFailed, f.kb.GeneratedProfile.Status)

	f.kb.ProfileConfig.ModelID = "override"
	profile, err := f.service.GenerateKnowledgeBaseProfile(context.Background(), f.kb, false)
	require.NoError(t, err)
	assert.Equal(t, "override", profile.ModelID)
}

func TestKnowledgeBaseProfileGenerateRejectsUnsupportedKB(t *testing.T) {
	f := newKBProfileFixture(kbProfileResponse)
	f.kb.Type = types.KnowledgeBaseTypeFAQ
	_, err := f.service.GenerateKnowledgeBaseProfile(context.Background(), f.kb, true)
	require.ErrorIs(t, err, ErrKnowledgeBaseProfileUnsupported)
	f.kb.Type = types.KnowledgeBaseTypeDocument
	f.kb.IsTemporary = true
	_, err = f.service.GenerateKnowledgeBaseProfile(context.Background(), f.kb, true)
	require.ErrorIs(t, err, ErrKnowledgeBaseProfileUnsupported)
}

func TestKnowledgeBaseProfileCustomInstructionsReachThePrompt(t *testing.T) {
	f := newKBProfileFixture(kbProfileResponse)
	f.kb.ProfileConfig.CustomInstructions = "Address platform engineers."
	_, err := f.service.GenerateKnowledgeBaseProfile(context.Background(), f.kb, false)
	require.NoError(t, err)
	assert.Contains(t, f.chatModel.messages[0].Content, "Address platform engineers.")
	assert.NotContains(t, f.chatModel.messages[0].Content, "{{custom_instructions}}")
	assert.NotContains(t, f.chatModel.messages[0].Content, "{{language}}")
}

func TestRequestKnowledgeBaseProfileRefreshHonoursConfigAndDebounces(t *testing.T) {
	f := newKBProfileFixture(kbProfileResponse)
	ctx := context.Background()

	f.kb.ProfileConfig.Enabled = false
	require.NoError(t, requestKnowledgeBaseProfileRefresh(ctx, f.queue, f.kb, false))
	assert.Empty(t, f.queue.tasks, "disabled knowledge bases never enqueue")

	require.NoError(t, requestKnowledgeBaseProfileRefresh(ctx, f.queue, f.kb, true))
	require.Len(t, f.queue.tasks, 1, "force bypasses the config")
	var payload types.KnowledgeBaseProfilePayload
	require.NoError(t, json.Unmarshal(f.queue.tasks[0].Payload(), &payload))
	assert.Equal(t, types.TypeKnowledgeBaseProfile, f.queue.tasks[0].Type())
	assert.Equal(t, uint64(7), payload.TenantID)
	assert.Equal(t, "kb", payload.KnowledgeBaseID)
	assert.True(t, payload.Force)

	f.kb.ProfileConfig.Enabled = true
	require.NoError(t, requestKnowledgeBaseProfileRefresh(ctx, f.queue, f.kb, false))
	require.Len(t, f.queue.tasks, 2)
	var sawDelay, sawTaskID bool
	for _, opt := range f.queue.opts[1] {
		switch opt.Type() {
		case asynq.ProcessInOpt:
			sawDelay = true
		case asynq.TaskIDOpt:
			sawTaskID = true
			assert.True(t, strings.HasPrefix(opt.Value().(string), "kb-profile:kb:"))
		}
	}
	assert.True(t, sawDelay && sawTaskID, "automatic refreshes are delayed and deduplicated per window")

	f.queue.err = asynq.ErrTaskIDConflict
	assert.NoError(t, requestKnowledgeBaseProfileRefresh(ctx, f.queue, f.kb, false),
		"a duplicate within the window is not an error")

	require.NoError(t, requestKnowledgeBaseProfileRefresh(ctx, f.queue, nil, true))
	faq := &types.KnowledgeBase{ID: "faq", TenantID: 7, Type: types.KnowledgeBaseTypeFAQ}
	f.queue.err = nil
	require.NoError(t, requestKnowledgeBaseProfileRefresh(ctx, f.queue, faq, true))
	assert.Len(t, f.queue.tasks, 2, "FAQ knowledge bases are skipped")
}

func TestKnowledgeBaseProfileHandle(t *testing.T) {
	f := newKBProfileFixture(kbProfileResponse)
	run := func(force bool) error {
		body, err := json.Marshal(types.KnowledgeBaseProfilePayload{TenantID: 7, KnowledgeBaseID: "kb", Force: force})
		require.NoError(t, err)
		return f.service.Handle(context.Background(), asynq.NewTask(types.TypeKnowledgeBaseProfile, body))
	}
	require.NoError(t, run(false))
	assert.Equal(t, 1, f.chatModel.calls)

	f.kb.ProfileConfig.Enabled = false
	f.kb.GeneratedProfile = nil
	require.NoError(t, run(false))
	assert.Equal(t, 1, f.chatModel.calls, "a disabled KB is skipped without touching the model")
	require.NoError(t, run(true))
	assert.Equal(t, 2, f.chatModel.calls, "a forced task still runs")

	f.kb.SummaryModelID = ""
	f.kb.GeneratedProfile = nil
	assert.NoError(t, run(true), "configuration errors are terminal, not retried")

	f.kb.SummaryModelID = "model-1"
	f.kb.GeneratedProfile = nil
	f.chatModel.err = errors.New("timeout")
	assert.Error(t, run(true), "transient model errors propagate so asynq retries")

	f.kbRepo.kb = nil
	assert.NoError(t, run(true), "a deleted knowledge base is skipped")
}

func TestParseDocumentSummaryOutput(t *testing.T) {
	fullJSON := `{"summary":"Two sentences.","gist":"A gist","topics":["a","b"],` +
		`"doc_type":"policy","typical_question":"Why?"}`
	fullProfile := &types.KnowledgeProfile{
		Gist:            "A gist",
		Topics:          []string{"a", "b"},
		DocType:         "policy",
		TypicalQuestion: "Why?",
	}
	// The reported failure shape: a 209-rune reply cut off at MaxTokens in the
	// middle of a string. It used to be stored raw as knowledge.Description,
	// marked completed (so Asynq never retried) and embedded as the summary chunk.
	truncatedJSON := `{"summary": "` + strings.Repeat("截", 130) + `", "gist": "` + strings.Repeat("断", 54)

	tests := []struct {
		name    string
		content string
		want    string
		profile *types.KnowledgeProfile
		wantErr error
	}{
		{
			name:    "structured JSON keeps summary and profile",
			content: fullJSON,
			want:    "Two sentences.",
			profile: fullProfile,
		},
		{
			name:    "fenced JSON keeps summary and profile",
			content: "```json\n" + fullJSON + "\n```",
			want:    "Two sentences.",
			profile: fullProfile,
		},
		{
			name:    "gist fills a missing summary",
			content: `{"gist":"Only a gist","topics":["x"]}`,
			want:    "Only a gist",
			profile: &types.KnowledgeProfile{Gist: "Only a gist", Topics: []string{"x"}},
		},
		{
			// The template's refusal reply is valid JSON with usable text, so
			// it keeps working (it carries no profile fields).
			name: "JSON refusal with an empty profile is kept",
			content: `{"summary": "No textual content was extractable from this document.",` +
				` "gist": "", "topics": [], "doc_type": "", "typical_question": ""}`,
			want: "No textual content was extractable from this document.",
		},
		{
			name:    "truncated JSON is rejected, never stored raw",
			content: truncatedJSON,
			wantErr: errSummaryOutputNotParsable,
		},
		{
			// A UTF-8 BOM is not whitespace: before the shape check stripped
			// it, the leading "{" stayed hidden and this half object was
			// handed back as the summary.
			name:    "BOM-prefixed truncated JSON is rejected",
			content: "\uFEFF" + truncatedJSON,
			wantErr: errSummaryOutputNotParsable,
		},
		{
			name:    "BOM-prefixed structured JSON still parses",
			content: "\uFEFF" + fullJSON,
			want:    "Two sentences.",
			profile: fullProfile,
		},
		{
			// Prose that opens with a bracketed citation is not broken JSON.
			name:    "bracketed prose keeps the legacy fallback",
			content: "[文档摘要] 本文件说明请假制度与年度额度。",
			want:    "[文档摘要] 本文件说明请假制度与年度额度。",
		},
		{
			name:    "numbered prose keeps the legacy fallback",
			content: "[1] 本文件说明请假制度与年度额度。",
			want:    "[1] 本文件说明请假制度与年度额度。",
		},
		{
			// A real JSON array is structured output that does not satisfy the
			// object contract, so it is rejected rather than stored raw.
			name:    "JSON array without the object contract is rejected",
			content: `["a","b"]`,
			wantErr: errSummaryOutputNotParsable,
		},
		{
			name:    "truncated JSON array is rejected",
			content: `[{"summary": "half a doc`,
			wantErr: errSummaryOutputNotParsable,
		},
		{
			name:    "JSON with an unescaped newline is rejected",
			content: "{\"summary\": \"First line\nSecond line\", \"gist\": \"g\"}",
			wantErr: errSummaryOutputNotParsable,
		},
		{
			name:    "JSON without summary or gist is rejected",
			content: `{"topics":["a"],"doc_type":"policy"}`,
			wantErr: errSummaryOutputNotParsable,
		},
		{
			name:    "whitespace-only reply is rejected",
			content: " \n\t ",
			wantErr: errEmptySummaryOutput,
		},
		{
			// Custom templates and older models answer in prose; that fallback
			// is deliberate and must survive.
			name:    "plain text keeps the legacy fallback",
			content: "  This document explains leave policy.  ",
			want:    "This document explains leave policy.",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, err := parseDocumentSummaryOutput(tt.content)
			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
				require.Nil(t, out, "a rejected reply must never be handed back as a summary")
				return
			}
			require.NoError(t, err)
			require.NotNil(t, out)
			assert.Equal(t, tt.want, out.Summary)
			assert.Equal(t, tt.profile, out.Profile)
		})
	}
}

func TestBuildSummaryChunkContent(t *testing.T) {
	assert.Equal(t, "# Summary\nBody.", buildSummaryChunkContent("Body.", nil))
	assert.Equal(t, "# Summary\nGist\n\nBody.",
		buildSummaryChunkContent("Body.", &types.KnowledgeProfile{Gist: "Gist"}))
	assert.Equal(t, "# Summary\nBody.", buildSummaryChunkContent("Body.", &types.KnowledgeProfile{Gist: "body."}),
		"a gist identical to the summary is not repeated")
}
