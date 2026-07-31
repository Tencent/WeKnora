package service

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"gorm.io/gorm"
)

type attributionSuggestionRepository struct {
	interfaces.MessageSuggestionRepository
	set *types.MessageSuggestionSet
	err error
}

func (r *attributionSuggestionRepository) GetByID(
	context.Context,
	uint64,
	string,
	string,
) (*types.MessageSuggestionSet, error) {
	return r.set, r.err
}

type suggestionScopeMessageService struct {
	interfaces.MessageService
	message *types.Message
}

func (s *suggestionScopeMessageService) GetMessage(
	context.Context,
	string,
	string,
) (*types.Message, error) {
	return s.message, nil
}

type suggestionScopeLifecycleRepository struct {
	interfaces.MessageSuggestionRepository
	saved     *types.MessageSuggestionSet
	saveCalls int
}

func (r *suggestionScopeLifecycleRepository) AcquireGeneration(
	_ context.Context,
	set *types.MessageSuggestionSet,
	_ bool,
) (*types.MessageSuggestionSet, bool, error) {
	return set, true, nil
}

func (r *suggestionScopeLifecycleRepository) Save(
	_ context.Context,
	set *types.MessageSuggestionSet,
) error {
	r.saved = set
	r.saveCalls++
	return nil
}

func TestParseGeneratedSuggestionsFiltersAndDeduplicates(t *testing.T) {
	content := "```json\n{\"questions\":[" +
		"{\"text\":\"如何继续实施？\",\"category\":\"action\"}," +
		"{\"text\":\"如何继续实施?\",\"category\":\"action\"}," +
		"{\"text\":\"有哪些风险？\",\"category\":\"unknown\"}" +
		"]}\n```"
	items, err := parseGeneratedSuggestions(content, []string{"clarify", "action"}, 3)
	if err != nil {
		t.Fatalf("parseGeneratedSuggestions() error = %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("len(items) = %d, want 2", len(items))
	}
	if items[0].Category != "action" {
		t.Fatalf("first category = %q, want action", items[0].Category)
	}
	if items[1].Category != "" {
		t.Fatalf("disallowed category = %q, want empty", items[1].Category)
	}
	for _, item := range items {
		if item.ID == "" || item.Source != "model" {
			t.Fatalf("item attribution fields are incomplete: %#v", item)
		}
	}
}

func TestMergeSuggestionItemsPreservesPriorityAndLimit(t *testing.T) {
	primary := types.SuggestionItems{{ID: "1", Text: "A?", Source: "model"}}
	fallback := types.SuggestionItems{
		{ID: "2", Text: "A？", Source: "faq"},
		{ID: "3", Text: "B?", Source: "faq"},
		{ID: "4", Text: "C?", Source: "faq"},
	}
	got := mergeSuggestionItems(primary, fallback, 2)
	if len(got) != 2 || got[0].ID != "1" || got[1].ID != "3" {
		t.Fatalf("mergeSuggestionItems() = %#v", got)
	}
}

func TestAnswerEndsWithQuestion(t *testing.T) {
	if !answerEndsWithQuestion("请补充具体时间？  ") {
		t.Fatal("Chinese question ending was not detected")
	}
	if answerEndsWithQuestion("结论已经给出。") {
		t.Fatal("statement was incorrectly detected as question")
	}
	if !answerEndsWithQuestion("需要我继续展开吗？\n<kb>1</kb>") {
		t.Fatal("question before a trailing citation was not detected")
	}
}

func TestBuildSuggestionGenerationContextUsesCompleteTurnsWithoutRawRAGContent(t *testing.T) {
	messages := []*types.Message{
		{ID: "u-old", RequestID: "old", Role: "user", Content: "old question"},
		{ID: "a-old", RequestID: "old", Role: "assistant", Content: "old answer", IsCompleted: true},
		{
			ID: "u-prev", RequestID: "prev", Role: "user",
			Content: "previous question", RenderedContent: "SECRET RAW RAG CONTEXT",
		},
		{
			ID: "a-prev", RequestID: "prev", Role: "assistant",
			Content: "<think>hidden</think>previous answer", IsCompleted: true,
		},
		{ID: "u-incomplete", RequestID: "incomplete", Role: "user", Content: "incomplete question"},
		{
			ID: "u-current", RequestID: "current", Role: "user",
			Content: "current question", RenderedContent: "CURRENT RAW RAG CONTEXT",
		},
		{ID: "a-current", RequestID: "current", Role: "assistant", Content: "current answer", IsCompleted: true},
	}
	current := messages[len(messages)-1]

	context := buildSuggestionGenerationContext(messages, current, 2)

	if context.CurrentQuery != "current question" {
		t.Fatalf("CurrentQuery = %q, want current question", context.CurrentQuery)
	}
	if !strings.Contains(context.History, "previous question") || !strings.Contains(context.History, "previous answer") {
		t.Fatalf("History does not contain the latest complete previous turn: %q", context.History)
	}
	for _, excluded := range []string{
		"old question",
		"incomplete question",
		"current question",
		"current answer",
		"hidden",
		"RAW RAG CONTEXT",
	} {
		if strings.Contains(context.History, excluded) {
			t.Fatalf("History unexpectedly contains %q: %q", excluded, context.History)
		}
	}
}

func TestBuildSuggestionGenerationContextOneTurnExcludesCurrentFromHistory(t *testing.T) {
	messages := []*types.Message{
		{ID: "u-current", RequestID: "current", Role: "user", Content: "current question"},
		{ID: "a-current", RequestID: "current", Role: "assistant", Content: "current answer", IsCompleted: true},
	}
	context := buildSuggestionGenerationContext(messages, messages[1], 1)
	if context.History != "" {
		t.Fatalf("History = %q, want empty when maxTurns includes only current turn", context.History)
	}
}

func TestBuildSuggestionEvidenceUsesTopReferencesAndDeduplicatesKnowledge(t *testing.T) {
	message := &types.Message{KnowledgeReferences: types.References{
		{ID: "low", Score: 0.2, KnowledgeID: "doc-low", KnowledgeTitle: "Low", Content: "low evidence"},
		{ID: "high", Score: 0.9, KnowledgeID: "doc-high", KnowledgeTitle: "High", Content: "high evidence"},
		{ID: "high-2", Score: 0.8, KnowledgeID: "doc-high", KnowledgeTitle: "High second", Content: "second chunk"},
	}}

	evidence, knowledgeIDs := buildSuggestionEvidence(message)
	if !strings.HasPrefix(evidence, "[1] High: high evidence") {
		t.Fatalf("Evidence was not score ordered: %q", evidence)
	}
	if len(knowledgeIDs) != 2 || knowledgeIDs[0] != "doc-high" || knowledgeIDs[1] != "doc-low" {
		t.Fatalf("knowledgeIDs = %#v, want score-ordered unique IDs", knowledgeIDs)
	}
}

func TestBuildSuggestionSystemPromptAllowsGroundedExploration(t *testing.T) {
	prompt := buildSuggestionSystemPrompt(3, "Chinese", "clarify, deepen, action")
	for _, expected := range []string{
		"Fresh retrieval is allowed",
		"self-contained",
		"concrete entity names or keywords",
		"at most roughly one third",
		"Do not assume unsupported facts",
		"must not override these grounding and capability rules",
	} {
		if !strings.Contains(prompt, expected) {
			t.Fatalf("Prompt does not contain %q: %q", expected, prompt)
		}
	}
	if strings.Contains(prompt, "enabled knowledge sources or tools") {
		t.Fatalf("Prompt still promises per-turn capabilities: %q", prompt)
	}
}

func TestRankKnowledgeSuggestionsPrioritizesCurrentTopic(t *testing.T) {
	candidates := []types.SuggestedQuestion{
		{Question: "How do I change the billing address?"},
		{Question: "How can I extend battery life while charging?"},
		{Question: "Where can I update my profile photo?"},
	}
	rankKnowledgeSuggestions(candidates, "The current answer explains battery charging and battery life.")
	if candidates[0].Question != "How can I extend battery life while charging?" {
		t.Fatalf("first candidate = %q, want battery-related question", candidates[0].Question)
	}
}

func TestMergeHybridSuggestionItemsReservesKnowledgeSlots(t *testing.T) {
	model := types.SuggestionItems{
		{Text: "model one", Source: "model"},
		{Text: "model two", Source: "model"},
		{Text: "model three", Source: "model"},
	}
	knowledge := types.SuggestionItems{
		{Text: "knowledge one", Source: "document"},
		{Text: "knowledge two", Source: "faq"},
	}

	items := mergeHybridSuggestionItems(model, knowledge, 3)
	if len(items) != 3 {
		t.Fatalf("len(items) = %d, want 3", len(items))
	}
	if items[0].Source != "model" || items[1].Source != "model" || items[2].Source != "document" {
		t.Fatalf("sources = [%s %s %s], want [model model document]", items[0].Source, items[1].Source, items[2].Source)
	}
}

func TestMergeHybridSuggestionItemsFillsMissingKnowledgeSlotsFromModel(t *testing.T) {
	model := types.SuggestionItems{
		{Text: "model one", Source: "model"},
		{Text: "model two", Source: "model"},
		{Text: "model three", Source: "model"},
	}

	items := mergeHybridSuggestionItems(model, nil, 3)
	if len(items) != 3 {
		t.Fatalf("len(items) = %d, want 3", len(items))
	}
	for _, item := range items {
		if item.Source != "model" {
			t.Fatalf("source = %q, want model", item.Source)
		}
	}
}

func TestAttachSuggestionKnowledgeScopeCopiesEachItem(t *testing.T) {
	includeDescendants := false
	folderScopes := []types.FolderScopeRequest{{
		KnowledgeBaseID:    "kb-1",
		FolderIDs:          []string{"folder-1"},
		IncludeDescendants: &includeDescendants,
	}}
	requestScope := &types.KnowledgeScopeRequest{
		KnowledgeBaseIDs: []string{"kb-1"},
		KnowledgeIDs:     []string{"knowledge-1"},
		FolderScopes:     &folderScopes,
	}
	items := types.SuggestionItems{
		{ID: "question-1", Text: "first", Source: "model"},
		{ID: "question-2", Text: "second", Source: "document"},
	}

	attachSuggestionKnowledgeScope(items, requestScope)

	if items[0].KnowledgeScope == nil || items[1].KnowledgeScope == nil {
		t.Fatal("suggestion scopes were not attached")
	}
	requestScope.KnowledgeBaseIDs[0] = "mutated-source-kb"
	(*requestScope.FolderScopes)[0].FolderIDs[0] = "mutated-source-folder"
	*(*requestScope.FolderScopes)[0].IncludeDescendants = true
	if items[0].KnowledgeScope.KnowledgeBaseIDs[0] != "kb-1" {
		t.Fatal("first suggestion scope aliases the request scope")
	}
	if (*items[0].KnowledgeScope.FolderScopes)[0].FolderIDs[0] != "folder-1" {
		t.Fatal("first suggestion folder scope aliases the request scope")
	}
	if *(*items[0].KnowledgeScope.FolderScopes)[0].IncludeDescendants {
		t.Fatal("first suggestion include_descendants aliases the request scope")
	}

	items[0].KnowledgeScope.KnowledgeIDs[0] = "mutated-first-knowledge"
	(*items[0].KnowledgeScope.FolderScopes)[0].FolderIDs[0] =
		"mutated-first-folder"
	if items[1].KnowledgeScope.KnowledgeIDs[0] != "knowledge-1" {
		t.Fatal("suggestion scopes share knowledge ID storage")
	}
	if (*items[1].KnowledgeScope.FolderScopes)[0].FolderIDs[0] != "folder-1" {
		t.Fatal("suggestion scopes share folder storage")
	}
}

func TestSuggestionItemsPersistencePreservesExplicitEmptyFolderScope(
	t *testing.T,
) {
	folderScopes := []types.FolderScopeRequest{}
	items := types.SuggestionItems{{
		ID:     "question-1",
		Text:   "follow up?",
		Source: "model",
		KnowledgeScope: &types.KnowledgeScopeRequest{
			KnowledgeBaseIDs: []string{"kb-1"},
			FolderScopes:     &folderScopes,
		},
	}}

	value, err := items.Value()
	if err != nil {
		t.Fatalf("SuggestionItems.Value() error = %v", err)
	}
	var restored types.SuggestionItems
	if err := restored.Scan(value); err != nil {
		t.Fatalf("SuggestionItems.Scan() error = %v", err)
	}
	if len(restored) != 1 || restored[0].KnowledgeScope == nil {
		t.Fatalf("restored items = %#v", restored)
	}
	if restored[0].KnowledgeScope.FolderScopes == nil {
		t.Fatal("explicit empty folder scopes became disabled")
	}
	if len(*restored[0].KnowledgeScope.FolderScopes) != 0 {
		t.Fatalf(
			"folder scopes = %#v, want explicit empty",
			*restored[0].KnowledgeScope.FolderScopes,
		)
	}
}

func TestValidateAttributionReturnsStoredScopeCopy(t *testing.T) {
	includeDescendants := false
	folderScopes := []types.FolderScopeRequest{{
		KnowledgeBaseID:    "kb-1",
		FolderIDs:          []string{"folder-1"},
		IncludeDescendants: &includeDescendants,
	}}
	storedScope := &types.KnowledgeScopeRequest{
		KnowledgeBaseIDs: []string{"kb-1"},
		KnowledgeIDs:     []string{"knowledge-1"},
		FolderScopes:     &folderScopes,
	}
	service := &messageSuggestionService{
		repo: &attributionSuggestionRepository{
			set: &types.MessageSuggestionSet{
				Status: types.SuggestionStatusReady,
				Questions: types.SuggestionItems{{
					ID:             "question-1",
					Text:           "follow up?",
					Source:         "model",
					KnowledgeScope: storedScope,
				}},
			},
		},
	}
	ctx := context.WithValue(
		context.Background(),
		types.TenantIDContextKey,
		uint64(77),
	)

	scope, err := service.ValidateAttribution(
		ctx,
		"session-1",
		" follow up? ",
		&types.SuggestionAttribution{
			SuggestionSetID: "set-1",
			QuestionID:      "question-1",
		},
	)
	if err != nil {
		t.Fatalf("ValidateAttribution() error = %v", err)
	}
	if scope == nil {
		t.Fatal("ValidateAttribution() scope is nil")
	}

	scope.KnowledgeBaseIDs[0] = "mutated-returned-kb"
	scope.KnowledgeIDs[0] = "mutated-returned-knowledge"
	(*scope.FolderScopes)[0].FolderIDs[0] = "mutated-returned-folder"
	*(*scope.FolderScopes)[0].IncludeDescendants = true
	if storedScope.KnowledgeBaseIDs[0] != "kb-1" {
		t.Fatal("returned scope aliases stored knowledge base IDs")
	}
	if storedScope.KnowledgeIDs[0] != "knowledge-1" {
		t.Fatal("returned scope aliases stored knowledge IDs")
	}
	if (*storedScope.FolderScopes)[0].FolderIDs[0] != "folder-1" {
		t.Fatal("returned scope aliases stored folder IDs")
	}
	if *(*storedScope.FolderScopes)[0].IncludeDescendants {
		t.Fatal("returned scope aliases stored include_descendants")
	}
}

func TestValidateAttributionAcceptsLegacyItemWithoutScope(t *testing.T) {
	service := &messageSuggestionService{
		repo: &attributionSuggestionRepository{
			set: &types.MessageSuggestionSet{
				Status: types.SuggestionStatusReady,
				Questions: types.SuggestionItems{{
					ID:     "question-1",
					Text:   "follow up?",
					Source: "model",
				}},
			},
		},
	}
	ctx := context.WithValue(
		context.Background(),
		types.TenantIDContextKey,
		uint64(77),
	)

	scope, err := service.ValidateAttribution(
		ctx,
		"session-1",
		"follow up?",
		&types.SuggestionAttribution{
			SuggestionSetID: "set-1",
			QuestionID:      "question-1",
		},
	)
	if scope != nil {
		t.Fatalf("ValidateAttribution() scope = %#v, want nil", scope)
	}
	if err != nil {
		t.Fatalf("ValidateAttribution() error = %v, want nil", err)
	}
}

func TestValidateAttributionMapsInvalidInputsToBadRequest(t *testing.T) {
	testCases := []struct {
		name        string
		repository  *attributionSuggestionRepository
		attribution *types.SuggestionAttribution
		query       string
	}{
		{
			name:       "missing suggestion set ID",
			repository: &attributionSuggestionRepository{},
			attribution: &types.SuggestionAttribution{
				QuestionID: "question-1",
			},
			query: "follow up?",
		},
		{
			name:       "missing question ID",
			repository: &attributionSuggestionRepository{},
			attribution: &types.SuggestionAttribution{
				SuggestionSetID: "set-1",
			},
			query: "follow up?",
		},
		{
			name: "repository not found",
			repository: &attributionSuggestionRepository{
				err: gorm.ErrRecordNotFound,
			},
			attribution: &types.SuggestionAttribution{
				SuggestionSetID: "set-1",
				QuestionID:      "question-1",
			},
			query: "follow up?",
		},
		{
			name: "set is not ready",
			repository: &attributionSuggestionRepository{
				set: &types.MessageSuggestionSet{
					Status: types.SuggestionStatusSuppressed,
				},
			},
			attribution: &types.SuggestionAttribution{
				SuggestionSetID: "set-1",
				QuestionID:      "question-1",
			},
			query: "follow up?",
		},
		{
			name: "question does not match",
			repository: &attributionSuggestionRepository{
				set: &types.MessageSuggestionSet{
					Status: types.SuggestionStatusReady,
					Questions: types.SuggestionItems{{
						ID:   "question-1",
						Text: "stored question",
					}},
				},
			},
			attribution: &types.SuggestionAttribution{
				SuggestionSetID: "set-1",
				QuestionID:      "question-1",
			},
			query: "forged question",
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			service := &messageSuggestionService{
				repo: testCase.repository,
			}
			scope, err := service.ValidateAttribution(
				suggestionScopeTestContext(),
				"session-1",
				testCase.query,
				testCase.attribution,
			)

			if scope != nil {
				t.Fatalf("ValidateAttribution() scope = %#v, want nil", scope)
			}
			appError := requireSuggestionAppError(
				t,
				err,
				http.StatusBadRequest,
			)
			if appError.Message != "invalid suggestion attribution" {
				t.Fatalf("ValidateAttribution() message = %q", appError.Message)
			}
		})
	}
}

func TestValidateAttributionPreservesContextErrors(t *testing.T) {
	testCases := []struct {
		name string
		err  error
		want error
	}{
		{
			name: "canceled",
			err:  fmt.Errorf("repository canceled: %w", context.Canceled),
			want: context.Canceled,
		},
		{
			name: "deadline exceeded",
			err:  fmt.Errorf("repository deadline: %w", context.DeadlineExceeded),
			want: context.DeadlineExceeded,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			service := &messageSuggestionService{
				repo: &attributionSuggestionRepository{err: testCase.err},
			}
			scope, err := service.ValidateAttribution(
				suggestionScopeTestContext(),
				"session-1",
				"follow up?",
				&types.SuggestionAttribution{
					SuggestionSetID: "set-1",
					QuestionID:      "question-1",
				},
			)

			if scope != nil {
				t.Fatalf("ValidateAttribution() scope = %#v, want nil", scope)
			}
			if !errors.Is(err, testCase.want) {
				t.Fatalf("ValidateAttribution() error = %v", err)
			}
			if err != testCase.err {
				t.Fatalf("ValidateAttribution() replaced repository error chain")
			}
		})
	}
}

func TestValidateAttributionMapsRepositoryFailureToInternalError(t *testing.T) {
	privateErr := errors.New("private repository failure")
	service := &messageSuggestionService{
		repo: &attributionSuggestionRepository{err: privateErr},
	}

	scope, err := service.ValidateAttribution(
		suggestionScopeTestContext(),
		"session-1",
		"follow up?",
		&types.SuggestionAttribution{
			SuggestionSetID: "set-1",
			QuestionID:      "question-1",
		},
	)

	if scope != nil {
		t.Fatalf("ValidateAttribution() scope = %#v, want nil", scope)
	}
	appError := requireSuggestionAppError(
		t,
		err,
		http.StatusInternalServerError,
	)
	if appError.Message != "message suggestion operation failed" {
		t.Fatalf("ValidateAttribution() message = %q", appError.Message)
	}
	if strings.Contains(appError.Message, privateErr.Error()) {
		t.Fatalf("ValidateAttribution() leaked repository error")
	}
}

func TestValidateAttributionNilRepositoryResultIsInternalError(t *testing.T) {
	service := &messageSuggestionService{
		repo: &attributionSuggestionRepository{},
	}

	scope, err := service.ValidateAttribution(
		suggestionScopeTestContext(),
		"session-1",
		"follow up?",
		&types.SuggestionAttribution{
			SuggestionSetID: "set-1",
			QuestionID:      "question-1",
		},
	)

	if scope != nil {
		t.Fatalf("ValidateAttribution() scope = %#v, want nil", scope)
	}
	requireSuggestionAppError(t, err, http.StatusInternalServerError)
}

func TestEnsureFollowUpsSuppressesMissingRequestScope(t *testing.T) {
	repository := &suggestionScopeLifecycleRepository{}
	messageService := &suggestionScopeMessageService{
		message: &types.Message{
			Role:        "assistant",
			IsCompleted: true,
			Content:     "completed answer",
			ExecutionContext: types.MessageExecutionContext{
				AgentConfigHash: "agent-config-hash",
				QuestionSuggestions: &types.QuestionSuggestionConfig{
					FollowUps: types.FollowUpSuggestionConfig{
						Enabled: true,
						Mode:    types.SuggestionModeGenerated,
						Count:   3,
					},
				},
			},
		},
	}
	service := &messageSuggestionService{
		repo:           repository,
		messageService: messageService,
	}

	set, err := service.EnsureFollowUps(
		suggestionScopeTestContext(),
		"session-1",
		"assistant-1",
		false,
	)

	if err != nil {
		t.Fatalf("EnsureFollowUps() error = %v", err)
	}
	if set == nil {
		t.Fatal("EnsureFollowUps() set is nil")
	}
	if set.Status != types.SuggestionStatusSuppressed {
		t.Fatalf("EnsureFollowUps() status = %q", set.Status)
	}
	if set.SuppressionReason != "missing_knowledge_scope" {
		t.Fatalf(
			"EnsureFollowUps() suppression reason = %q",
			set.SuppressionReason,
		)
	}
	if len(set.Questions) != 0 {
		t.Fatalf("EnsureFollowUps() questions = %#v", set.Questions)
	}
	if repository.saveCalls != 1 || repository.saved != set {
		t.Fatalf(
			"EnsureFollowUps() save calls = %d, saved = %#v",
			repository.saveCalls,
			repository.saved,
		)
	}
}

func suggestionScopeTestContext() context.Context {
	return context.WithValue(
		context.Background(),
		types.TenantIDContextKey,
		uint64(77),
	)
}

func requireSuggestionAppError(
	t *testing.T,
	err error,
	httpCode int,
) *apperrors.AppError {
	t.Helper()
	if err == nil {
		t.Fatal("expected application error")
	}
	var appError *apperrors.AppError
	if !errors.As(err, &appError) {
		t.Fatalf("error = %T %v, want AppError", err, err)
	}
	if appError.HTTPCode != httpCode {
		t.Fatalf(
			"HTTP code = %d, want %d",
			appError.HTTPCode,
			httpCode,
		)
	}
	return appError
}
