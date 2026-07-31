package session

import (
	"bytes"
	"context"
	"encoding/json"
	stderrors "errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/event"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/stream"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	phase4B1RequestFolderID  = "00000000-0000-0000-0000-000000000041"
	phase4B1ResolvedFolderID = "00000000-0000-0000-0000-000000000042"
)

type phase4B1ContextKey struct{}

type phase4B1SessionServiceStub struct {
	interfaces.SessionService

	session             *types.Session
	preparation         *types.KnowledgeScopePreparation
	prepareErr          error
	prepareCalls        int
	prepareContext      context.Context
	prepareInput        types.KnowledgeScopePrepareInput
	searchCalls         int
	searchPreparation   *types.KnowledgeScopePreparation
	searchResults       []*types.SearchResult
	searchErr           error
	knowledgeQACalls    int
	agentQACalls        int
	knowledgeQARequest  *types.QARequest
	lastRequestState    *types.SessionLastRequestState
	lastRequestStateCtx context.Context
}

type phase4B1CustomAgentServiceStub struct {
	interfaces.CustomAgentService

	agent *types.CustomAgent
}

type phase4B1AgentShareServiceStub struct {
	interfaces.AgentShareService

	agent *types.CustomAgent
	err   error
}

type phase4B1SuggestionServiceStub struct {
	interfaces.MessageSuggestionService

	scope             *types.KnowledgeScopeRequest
	err               error
	calls             int
	validationContext context.Context
}

type phase4B1MessageServiceStub struct {
	interfaces.MessageService

	createCalls int
}

func (s *phase4B1CustomAgentServiceStub) GetAgentByID(
	_ context.Context,
	_ string,
) (*types.CustomAgent, error) {
	return s.agent, nil
}

func (s *phase4B1AgentShareServiceStub) GetSharedAgentForTenant(
	_ context.Context,
	_ uint64,
	_ types.TenantRole,
	_ string,
) (*types.CustomAgent, error) {
	return s.agent, s.err
}

func (s *phase4B1SuggestionServiceStub) ValidateAttribution(
	ctx context.Context,
	_ string,
	_ string,
	_ *types.SuggestionAttribution,
) (*types.KnowledgeScopeRequest, error) {
	s.calls++
	s.validationContext = ctx
	return s.scope, s.err
}

func (s *phase4B1SessionServiceStub) GetSession(
	_ context.Context,
	_ string,
) (*types.Session, error) {
	return s.session, nil
}

func (s *phase4B1SessionServiceStub) PrepareKnowledgeScope(
	ctx context.Context,
	input types.KnowledgeScopePrepareInput,
) (*types.KnowledgeScopePreparation, error) {
	s.prepareCalls++
	s.prepareContext = ctx
	s.prepareInput = input
	return s.preparation, s.prepareErr
}

func (s *phase4B1SessionServiceStub) SearchKnowledgeWithScope(
	_ context.Context,
	_ string,
	preparation *types.KnowledgeScopePreparation,
) ([]*types.SearchResult, error) {
	s.searchCalls++
	s.searchPreparation = preparation
	return s.searchResults, s.searchErr
}

func (s *phase4B1SessionServiceStub) KnowledgeQA(
	ctx context.Context,
	request *types.QARequest,
	eventBus *event.EventBus,
) error {
	s.knowledgeQACalls++
	s.knowledgeQARequest = request
	return eventBus.Emit(ctx, event.Event{
		Type:      event.EventAgentComplete,
		SessionID: request.Session.ID,
		Data: event.AgentCompleteData{
			SessionID: request.Session.ID,
			MessageID: request.AssistantMessageID,
		},
	})
}

func (s *phase4B1SessionServiceStub) AgentQA(
	ctx context.Context,
	request *types.QARequest,
	eventBus *event.EventBus,
) error {
	s.agentQACalls++
	return eventBus.Emit(ctx, event.Event{
		Type:      event.EventAgentComplete,
		SessionID: request.Session.ID,
		Data: event.AgentCompleteData{
			SessionID: request.Session.ID,
			MessageID: request.AssistantMessageID,
		},
	})
}

func (s *phase4B1SessionServiceStub) UpdateSessionLastRequestState(
	ctx context.Context,
	_ string,
	state *types.SessionLastRequestState,
) error {
	s.lastRequestStateCtx = ctx
	s.lastRequestState = state
	return nil
}

func (s *phase4B1MessageServiceStub) CreateMessage(
	_ context.Context,
	message *types.Message,
) (*types.Message, error) {
	s.createCalls++
	if message.ID == "" {
		message.ID = fmt.Sprintf("message-%d", s.createCalls)
	}
	return message, nil
}

func (s *phase4B1MessageServiceStub) UpdateMessage(
	context.Context,
	*types.Message,
) error {
	return nil
}

func (s *phase4B1MessageServiceStub) IndexMessageToKB(
	context.Context,
	string,
	string,
	string,
	string,
) {
}

func phase4B1Bool(value bool) *bool {
	return &value
}

func newPhase4B1Preparation(
	t *testing.T,
	request *types.KnowledgeScopeRequest,
	folderFilterEnabled bool,
	resolvedFolderIDs []string,
) *types.KnowledgeScopePreparation {
	t.Helper()

	filter, err := types.NewResolvedFolderFilter(
		folderFilterEnabled,
		resolvedFolderIDs,
	)
	require.NoError(t, err)
	target, err := types.NewKnowledgeScopeTarget(
		"kb-1",
		77,
		[]string{"knowledge-1"},
		[]string{"tag-physical"},
		[]string{"tag-request"},
		filter,
	)
	require.NoError(t, err)
	scope, err := types.NewKnowledgeScope([]types.KnowledgeScopeTarget{target})
	require.NoError(t, err)
	preparation, err := types.NewKnowledgeScopePreparation(
		request,
		scope,
		"0123456789abcdef",
	)
	require.NoError(t, err)
	return preparation
}

func newPhase4B1EmptyPreparation(
	t *testing.T,
	request *types.KnowledgeScopeRequest,
) *types.KnowledgeScopePreparation {
	t.Helper()

	scope, err := types.NewKnowledgeScope(nil)
	require.NoError(t, err)
	preparation, err := types.NewKnowledgeScopePreparation(
		request,
		scope,
		"0123456789abcdef",
	)
	require.NoError(t, err)
	return preparation
}

func assertFolderScopeJSONState(
	t *testing.T,
	payload string,
	decode func([]byte) *types.KnowledgeScopeRequest,
	wantEnabled bool,
) {
	t.Helper()

	scope := decode([]byte(payload))
	require.NotNil(t, scope)
	if !wantEnabled {
		assert.Nil(t, scope.FolderScopes)
		return
	}
	require.NotNil(t, scope.FolderScopes)
	assert.Empty(t, *scope.FolderScopes)
}

func TestCreateKnowledgeQARequestPreservesFolderScopeJSONStates(t *testing.T) {
	decode := func(payload []byte) *types.KnowledgeScopeRequest {
		var request CreateKnowledgeQARequest
		require.NoError(t, json.Unmarshal(payload, &request))
		return request.KnowledgeScope
	}

	assertFolderScopeJSONState(
		t,
		`{"query":"q","knowledge_scope":{"knowledge_base_ids":["kb-1"]}}`,
		decode,
		false,
	)
	assertFolderScopeJSONState(
		t,
		`{"query":"q","knowledge_scope":{"knowledge_base_ids":["kb-1"],"folder_scopes":null}}`,
		decode,
		false,
	)
	assertFolderScopeJSONState(
		t,
		`{"query":"q","knowledge_scope":{"knowledge_base_ids":["kb-1"],"folder_scopes":[]}}`,
		decode,
		true,
	)
}

func TestSearchKnowledgeRequestPreservesFolderScopeJSONStates(t *testing.T) {
	decode := func(payload []byte) *types.KnowledgeScopeRequest {
		var request SearchKnowledgeRequest
		require.NoError(t, json.Unmarshal(payload, &request))
		return request.KnowledgeScope
	}

	assertFolderScopeJSONState(
		t,
		`{"query":"q","knowledge_scope":{"knowledge_base_ids":["kb-1"]}}`,
		decode,
		false,
	)
	assertFolderScopeJSONState(
		t,
		`{"query":"q","knowledge_scope":{"knowledge_base_ids":["kb-1"],"folder_scopes":null}}`,
		decode,
		false,
	)
	assertFolderScopeJSONState(
		t,
		`{"query":"q","knowledge_scope":{"knowledge_base_ids":["kb-1"],"folder_scopes":[]}}`,
		decode,
		true,
	)
}

func TestPrepareNormalKnowledgeScopeUsesRequestContextAndStoresIndependentSnapshots(t *testing.T) {
	includeDescendants := false
	folderScopes := []types.FolderScopeRequest{{
		KnowledgeBaseID:    "kb-1",
		FolderIDs:          []string{},
		IncludeDescendants: &includeDescendants,
	}}
	canonical := &types.KnowledgeScopeRequest{
		KnowledgeBaseIDs: []string{"kb-1"},
		KnowledgeIDs:     []string{"knowledge-1"},
		TagScopes: []types.TagScope{{
			KnowledgeBaseID: "kb-1",
			TagIDs:          []string{"tag-request"},
		}},
		FolderScopes: &folderScopes,
	}
	preparation := newPhase4B1Preparation(t, canonical, true, nil)
	stub := &phase4B1SessionServiceStub{preparation: preparation}
	h := &Handler{sessionService: stub}
	requestContext := context.WithValue(
		context.Background(),
		phase4B1ContextKey{},
		"original-request",
	)
	reqCtx := &qaRequestContext{
		requestContext:   requestContext,
		session:          &types.Session{ID: "session-1"},
		assistantMessage: &types.Message{},
		knowledgeBaseIDs: []string{"kb-1"},
		knowledgeIDs:     []string{"knowledge-1"},
		tagScopes: []types.TagScope{{
			KnowledgeBaseID: "kb-1",
			TagIDs:          []string{"tag-request"},
		}},
	}

	require.NoError(t, h.prepareNormalKnowledgeScope(reqCtx, canonical, false))
	require.Equal(t, 1, stub.prepareCalls)
	assert.Equal(
		t,
		"original-request",
		stub.prepareContext.Value(phase4B1ContextKey{}),
	)
	require.NotNil(t, stub.prepareInput.CanonicalRequest)
	require.NotNil(t, stub.prepareInput.LegacyRequest)
	assert.Equal(t, "0123456789abcdef", reqCtx.executionScopeHash)
	assert.Equal(
		t,
		"0123456789abcdef",
		reqCtx.assistantMessage.ExecutionContext.ExecutionScopeHash,
	)
	require.NotNil(t, reqCtx.requestScope)
	require.NotNil(t, reqCtx.assistantMessage.ExecutionContext.RequestScope)

	canonical.KnowledgeBaseIDs[0] = "mutated-canonical"
	(*canonical.FolderScopes)[0].KnowledgeBaseID = "mutated-folder-scope"
	*(*canonical.FolderScopes)[0].IncludeDescendants = true
	assert.Equal(
		t,
		"kb-1",
		stub.prepareInput.CanonicalRequest.KnowledgeBaseIDs[0],
	)
	assert.Equal(
		t,
		"kb-1",
		(*stub.prepareInput.CanonicalRequest.FolderScopes)[0].KnowledgeBaseID,
	)
	assert.False(
		t,
		*(*stub.prepareInput.CanonicalRequest.FolderScopes)[0].IncludeDescendants,
	)

	reqCtx.requestScope.KnowledgeBaseIDs[0] = "mutated-prepared"
	assert.Equal(
		t,
		"kb-1",
		reqCtx.assistantMessage.ExecutionContext.RequestScope.KnowledgeBaseIDs[0],
	)
}

func TestPrepareNormalKnowledgeScopeRejectsEnabledNonEmptyBeforeExecution(t *testing.T) {
	folderScopes := []types.FolderScopeRequest{{
		KnowledgeBaseID:    "kb-1",
		FolderIDs:          []string{phase4B1RequestFolderID},
		IncludeDescendants: phase4B1Bool(true),
	}}
	request := &types.KnowledgeScopeRequest{
		KnowledgeBaseIDs: []string{"kb-1"},
		FolderScopes:     &folderScopes,
	}
	stub := &phase4B1SessionServiceStub{
		preparation: newPhase4B1Preparation(
			t,
			request,
			true,
			[]string{phase4B1ResolvedFolderID},
		),
	}
	h := &Handler{sessionService: stub}
	reqCtx := &qaRequestContext{
		requestContext:   context.Background(),
		assistantMessage: &types.Message{},
		knowledgeBaseIDs: []string{"kb-1"},
	}

	err := h.prepareNormalKnowledgeScope(reqCtx, request, false)
	var appErr *apperrors.AppError
	require.ErrorAs(t, err, &appErr)
	assert.Equal(t, http.StatusServiceUnavailable, appErr.HTTPCode)
	assert.Equal(
		t,
		"folder-scoped retrieval is temporarily unavailable",
		appErr.Message,
	)
	assert.Equal(t, 1, stub.prepareCalls)
	assert.Nil(t, reqCtx.requestScope)
	assert.Nil(t, reqCtx.executionScope)
	assert.Empty(t, reqCtx.executionScopeHash)
}

func TestPrepareNormalKnowledgeScopeAllowsEnabledNonEmptyForWebQuickAnswer(
	t *testing.T,
) {
	folderScopes := []types.FolderScopeRequest{{
		KnowledgeBaseID:    "kb-1",
		FolderIDs:          []string{phase4B1RequestFolderID},
		IncludeDescendants: phase4B1Bool(true),
	}}
	request := &types.KnowledgeScopeRequest{
		KnowledgeBaseIDs: []string{"kb-1"},
		FolderScopes:     &folderScopes,
	}
	preparation := newPhase4B1Preparation(
		t,
		request,
		true,
		[]string{phase4B1ResolvedFolderID},
	)
	stub := &phase4B1SessionServiceStub{preparation: preparation}
	h := &Handler{sessionService: stub}
	reqCtx := &qaRequestContext{
		requestContext:   context.Background(),
		assistantMessage: &types.Message{},
		knowledgeBaseIDs: []string{"kb-1"},
	}

	err := h.prepareNormalKnowledgeScope(reqCtx, request, true)

	require.NoError(t, err)
	assert.Equal(t, 1, stub.prepareCalls)
	assert.Equal(t, preparation.Request(), reqCtx.requestScope)
	assert.Equal(t, preparation.Execution(), reqCtx.executionScope)
	assert.Equal(
		t,
		preparation.ExecutionScopeHash(),
		reqCtx.executionScopeHash,
	)
	assert.Equal(
		t,
		preparation.ExecutionScopeHash(),
		reqCtx.assistantMessage.ExecutionContext.ExecutionScopeHash,
	)
}

func TestAllowsFolderScopeForWebQuickAnswer(t *testing.T) {
	testCases := []struct {
		name            string
		principalType   string
		agentEnabled    bool
		customAgentMode string
		sharedAgent     bool
		apiKeyScope     bool
		want            bool
	}{
		{
			name:          "ordinary web quick answer",
			principalType: types.PrincipalWebUser,
			want:          true,
		},
		{
			name:          "agent enabled",
			principalType: types.PrincipalWebUser,
			agentEnabled:  true,
		},
		{
			name:            "resolved quick answer agent",
			principalType:   types.PrincipalWebUser,
			customAgentMode: types.AgentModeQuickAnswer,
			want:            true,
		},
		{
			name:            "resolved smart reasoning agent",
			principalType:   types.PrincipalWebUser,
			customAgentMode: types.AgentModeSmartReasoning,
		},
		{
			name:          "trusted shared agent",
			principalType: types.PrincipalWebUser,
			sharedAgent:   true,
		},
		{
			name:          "tenant api principal",
			principalType: types.PrincipalAPITenant,
		},
		{
			name:          "external api principal",
			principalType: types.PrincipalAPIExternalUser,
		},
		{
			name:          "embed principal",
			principalType: types.PrincipalEmbedSession,
		},
		{
			name:          "web principal with api key scope",
			principalType: types.PrincipalWebUser,
			apiKeyScope:   true,
		},
		{
			name: "missing principal",
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			requestContext := context.Background()
			if testCase.principalType != "" {
				requestContext = types.WithPrincipal(
					requestContext,
					types.Principal{
						Type: testCase.principalType,
						ID:   "caller-1",
					},
				)
			}
			if testCase.apiKeyScope {
				requestContext = types.WithTenantAPIKeyScope(
					requestContext,
					types.TenantAPIKeyScope{KeyID: 1},
				)
			}
			reqCtx := &qaRequestContext{
				requestContext: requestContext,
				sharedAgent:    testCase.sharedAgent,
			}
			if testCase.customAgentMode != "" {
				reqCtx.customAgent = &types.CustomAgent{
					ID: "agent-1",
					Config: types.CustomAgentConfig{
						AgentMode: testCase.customAgentMode,
					},
				}
			}

			got := allowsFolderScopeForWebQuickAnswer(
				reqCtx,
				&CreateKnowledgeQARequest{
					AgentEnabled: testCase.agentEnabled,
				},
			)

			assert.Equal(t, testCase.want, got)
		})
	}
}

func TestKnowledgeQAAllowsPreparedFolderScopeForWebQuickAnswer(
	t *testing.T,
) {
	gin.SetMode(gin.TestMode)
	folderScopes := []types.FolderScopeRequest{{
		KnowledgeBaseID:    "kb-1",
		FolderIDs:          []string{phase4B1RequestFolderID},
		IncludeDescendants: phase4B1Bool(true),
	}}
	requestScope := &types.KnowledgeScopeRequest{
		KnowledgeBaseIDs: []string{"kb-1"},
		FolderScopes:     &folderScopes,
	}
	sessionStub := &phase4B1SessionServiceStub{
		session: &types.Session{
			ID:       "session-1",
			TenantID: 77,
			Title:    "existing title",
		},
		preparation: newPhase4B1Preparation(
			t,
			requestScope,
			true,
			[]string{phase4B1ResolvedFolderID},
		),
	}
	messageStub := &phase4B1MessageServiceStub{}
	h := &Handler{
		sessionService: sessionStub,
		messageService: messageStub,
		customAgentService: &phase4B1CustomAgentServiceStub{
			agent: &types.CustomAgent{
				ID:       types.BuiltinQuickAnswerID,
				TenantID: 77,
				Config: types.CustomAgentConfig{
					AgentMode: types.AgentModeQuickAnswer,
				},
			},
		},
		streamManager: stream.NewMemoryStreamManager(),
	}
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Params = gin.Params{{Key: "session_id", Value: "session-1"}}
	requestContext := types.WithPrincipal(
		context.Background(),
		types.Principal{
			Type: types.PrincipalWebUser,
			ID:   "user-1",
		},
	)
	ctx.Request = httptest.NewRequest(
		http.MethodPost,
		"/api/v1/sessions/session-1/knowledge-qa",
		bytes.NewBufferString(
			`{"query":"q","agent_id":"`+types.BuiltinQuickAnswerID+`",`+
				`"agent_enabled":false,"disable_title":true,`+
				`"knowledge_scope":{"knowledge_base_ids":["kb-1"],`+
				`"folder_scopes":[{"knowledge_base_id":"kb-1",`+
				`"folder_ids":["`+phase4B1RequestFolderID+`"],`+
				`"include_descendants":true}]}}`,
		),
	).WithContext(requestContext)
	ctx.Request.Header.Set("Content-Type", "application/json")

	h.KnowledgeQA(ctx)

	assert.Empty(t, ctx.Errors)
	assert.Equal(t, 1, sessionStub.prepareCalls)
	require.NotNil(t, sessionStub.prepareInput.CustomAgent)
	assert.Equal(
		t,
		types.BuiltinQuickAnswerID,
		sessionStub.prepareInput.CustomAgent.ID,
	)
	assert.False(t, sessionStub.prepareInput.SharedAgent)
	principal, ok := types.PrincipalFromContext(sessionStub.prepareContext)
	require.True(t, ok)
	assert.Equal(t, types.PrincipalWebUser, principal.Type)
	assert.Equal(t, "user-1", principal.ID)
	_, hasTenantAPIKeyScope := types.TenantAPIKeyScopeFromContext(
		sessionStub.prepareContext,
	)
	assert.False(t, hasTenantAPIKeyScope)
	assert.Equal(t, 1, sessionStub.knowledgeQACalls)
	assert.Equal(t, 0, sessionStub.agentQACalls)
	require.NotNil(t, sessionStub.knowledgeQARequest)
	require.NotNil(t, sessionStub.knowledgeQARequest.RequestScope)
	require.NotNil(t, sessionStub.knowledgeQARequest.RequestScope.FolderScopes)
	require.Len(
		t,
		*sessionStub.knowledgeQARequest.RequestScope.FolderScopes,
		1,
	)
	assert.Equal(
		t,
		phase4B1RequestFolderID,
		(*sessionStub.knowledgeQARequest.RequestScope.FolderScopes)[0].
			FolderIDs[0],
	)
	require.NotNil(t, sessionStub.knowledgeQARequest.ExecutionScope)
	assert.Equal(
		t,
		"0123456789abcdef",
		sessionStub.knowledgeQARequest.ExecutionScopeHash,
	)
	targets := sessionStub.knowledgeQARequest.ExecutionScope.Targets()
	require.Len(t, targets, 1)
	assert.Equal(t, "kb-1", targets[0].KnowledgeBaseID())
	assert.Equal(t, uint64(77), targets[0].SourceTenantID())
	assert.Equal(t, []string{"knowledge-1"}, targets[0].KnowledgeIDs())
	assert.True(t, targets[0].FolderFilter().Enabled())
	assert.Equal(
		t,
		[]string{phase4B1ResolvedFolderID},
		targets[0].FolderFilter().FolderIDs(),
	)
	assert.Equal(t, 2, messageStub.createCalls)
}

func TestSearchKnowledgePreservesScopeAppError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	conflict := apperrors.NewBadRequestError(
		"canonical and legacy knowledge scope differ",
	)
	stub := &phase4B1SessionServiceStub{prepareErr: conflict}
	h := &Handler{sessionService: stub}
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	requestContext := context.WithValue(
		context.Background(),
		phase4B1ContextKey{},
		"search-request",
	)
	ctx.Request = httptest.NewRequest(
		http.MethodPost,
		"/api/v1/sessions/search",
		bytes.NewBufferString(
			`{"query":"q","knowledge_base_ids":["kb-legacy"],`+
				`"knowledge_scope":{"knowledge_base_ids":["kb-canonical"]}}`,
		),
	).WithContext(requestContext)
	ctx.Request.Header.Set("Content-Type", "application/json")

	h.SearchKnowledge(ctx)

	require.Equal(t, 1, stub.prepareCalls)
	assert.Equal(
		t,
		"search-request",
		stub.prepareContext.Value(phase4B1ContextKey{}),
	)
	assert.Equal(t, 0, stub.searchCalls)
	require.Len(t, ctx.Errors, 1)
	assert.Same(t, conflict, ctx.Errors[0].Err)
	assert.Equal(t, http.StatusBadRequest, conflict.HTTPCode)
}

func TestSearchKnowledgeEnabledEmptyDelegatesWithoutFallback(t *testing.T) {
	gin.SetMode(gin.TestMode)
	empty := []types.FolderScopeRequest{}
	requestScope := &types.KnowledgeScopeRequest{
		KnowledgeBaseIDs: []string{"kb-1"},
		FolderScopes:     &empty,
	}
	preparation := newPhase4B1Preparation(t, requestScope, true, nil)
	stub := &phase4B1SessionServiceStub{
		preparation:   preparation,
		searchResults: []*types.SearchResult{},
	}
	h := &Handler{sessionService: stub}
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(
		http.MethodPost,
		"/api/v1/sessions/search",
		bytes.NewBufferString(
			`{"query":"q","knowledge_base_ids":["kb-1"],`+
				`"knowledge_scope":{"knowledge_base_ids":["kb-1"],"folder_scopes":[]}}`,
		),
	)
	ctx.Request.Header.Set("Content-Type", "application/json")

	h.SearchKnowledge(ctx)

	assert.Empty(t, ctx.Errors)
	assert.Equal(t, http.StatusOK, recorder.Code)
	assert.Equal(t, 1, stub.searchCalls)
	assert.Same(t, preparation, stub.searchPreparation)
}

func TestSearchKnowledgeExplicitEmptyScopeWithoutTargetsReturnsEmpty(
	t *testing.T,
) {
	gin.SetMode(gin.TestMode)
	empty := []types.FolderScopeRequest{}
	requestScope := &types.KnowledgeScopeRequest{FolderScopes: &empty}
	preparation := newPhase4B1EmptyPreparation(t, requestScope)
	stub := &phase4B1SessionServiceStub{
		preparation:   preparation,
		searchResults: []*types.SearchResult{},
	}
	h := &Handler{sessionService: stub}
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(
		http.MethodPost,
		"/api/v1/sessions/search",
		bytes.NewBufferString(
			`{"query":"q","knowledge_scope":{"folder_scopes":[]}}`,
		),
	)
	ctx.Request.Header.Set("Content-Type", "application/json")

	h.SearchKnowledge(ctx)

	assert.Empty(t, ctx.Errors)
	assert.Equal(t, http.StatusOK, recorder.Code)
	assert.Equal(t, 1, stub.searchCalls)
	assert.Same(t, preparation, stub.searchPreparation)
}

func TestSearchKnowledgeLegacyRequestKeepsDisabledFolderBehavior(t *testing.T) {
	gin.SetMode(gin.TestMode)
	requestScope := &types.KnowledgeScopeRequest{
		KnowledgeBaseIDs: []string{"kb-1"},
	}
	preparation := newPhase4B1Preparation(
		t,
		requestScope,
		false,
		nil,
	)
	stub := &phase4B1SessionServiceStub{
		preparation: preparation,
		searchResults: []*types.SearchResult{{
			ID: "result-1",
		}},
	}
	h := &Handler{sessionService: stub}
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(
		http.MethodPost,
		"/api/v1/sessions/search",
		bytes.NewBufferString(
			`{"query":"q","knowledge_base_ids":["kb-1"]}`,
		),
	)
	ctx.Request.Header.Set("Content-Type", "application/json")

	h.SearchKnowledge(ctx)

	assert.Empty(t, ctx.Errors)
	assert.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, 1, stub.prepareCalls)
	assert.Nil(t, stub.prepareInput.CanonicalRequest)
	require.NotNil(t, stub.prepareInput.LegacyRequest)
	assert.Equal(
		t,
		[]string{"kb-1"},
		stub.prepareInput.LegacyRequest.KnowledgeBaseIDs,
	)
	assert.Nil(t, stub.prepareInput.LegacyRequest.FolderScopes)
	assert.Equal(t, 1, stub.searchCalls)
	assert.False(
		t,
		stub.searchPreparation.Execution().Targets()[0].
			FolderFilter().Enabled(),
	)
}

func TestSearchKnowledgeEnabledNonEmptyReturnsFixed503WithoutSearch(t *testing.T) {
	gin.SetMode(gin.TestMode)
	folderScopes := []types.FolderScopeRequest{{
		KnowledgeBaseID: "kb-1",
		FolderIDs:       []string{phase4B1RequestFolderID},
	}}
	requestScope := &types.KnowledgeScopeRequest{
		KnowledgeBaseIDs: []string{"kb-1"},
		FolderScopes:     &folderScopes,
	}
	stub := &phase4B1SessionServiceStub{
		preparation: newPhase4B1Preparation(
			t,
			requestScope,
			true,
			[]string{phase4B1ResolvedFolderID},
		),
	}
	h := &Handler{sessionService: stub}
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(
		http.MethodPost,
		"/api/v1/sessions/search",
		bytes.NewBufferString(
			`{"query":"q","knowledge_base_ids":["kb-1"],`+
				`"knowledge_scope":{"knowledge_base_ids":["kb-1"],`+
				`"folder_scopes":[{"knowledge_base_id":"kb-1",`+
				`"folder_ids":["`+phase4B1RequestFolderID+`"]}]}}`,
		),
	)
	ctx.Request.Header.Set("Content-Type", "application/json")

	h.SearchKnowledge(ctx)

	assert.Equal(t, 0, stub.searchCalls)
	require.Len(t, ctx.Errors, 1)
	var appErr *apperrors.AppError
	require.ErrorAs(t, ctx.Errors[0].Err, &appErr)
	assert.Equal(t, http.StatusServiceUnavailable, appErr.HTTPCode)
	assert.Equal(
		t,
		"folder-scoped retrieval is temporarily unavailable",
		appErr.Message,
	)
}

func TestAgentQARejectsMissingAgentBeforeScopePreparation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	stub := &phase4B1SessionServiceStub{
		session: &types.Session{ID: "session-1", TenantID: 77},
	}
	h := &Handler{sessionService: stub}
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Params = gin.Params{{Key: "session_id", Value: "session-1"}}
	ctx.Request = httptest.NewRequest(
		http.MethodPost,
		"/api/v1/sessions/session-1/agent-qa",
		bytes.NewBufferString(
			`{"query":"q","agent_enabled":true,`+
				`"knowledge_scope":{"knowledge_base_ids":["kb-1"]}}`,
		),
	)
	ctx.Request.Header.Set("Content-Type", "application/json")

	h.AgentQA(ctx)

	assert.Equal(t, 0, stub.prepareCalls)
	require.Len(t, ctx.Errors, 1)
	var appErr *apperrors.AppError
	require.ErrorAs(t, ctx.Errors[0].Err, &appErr)
	assert.Equal(t, http.StatusBadRequest, appErr.HTTPCode)
}

func TestAgentQAPreparesKnowledgeScopeBeforeExecution(t *testing.T) {
	gin.SetMode(gin.TestMode)
	folderScopes := []types.FolderScopeRequest{{
		KnowledgeBaseID: "kb-1",
		FolderIDs:       []string{phase4B1RequestFolderID},
	}}
	requestScope := &types.KnowledgeScopeRequest{
		KnowledgeBaseIDs: []string{"kb-1"},
		FolderScopes:     &folderScopes,
	}
	sessionStub := &phase4B1SessionServiceStub{
		session: &types.Session{ID: "session-1", TenantID: 77},
		preparation: newPhase4B1Preparation(
			t,
			requestScope,
			true,
			[]string{phase4B1ResolvedFolderID},
		),
	}
	agent := &types.CustomAgent{
		ID:       "agent-1",
		TenantID: 77,
		Config: types.CustomAgentConfig{
			AgentMode: types.AgentModeSmartReasoning,
		},
	}
	h := &Handler{
		sessionService: sessionStub,
		customAgentService: &phase4B1CustomAgentServiceStub{
			agent: agent,
		},
	}
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Params = gin.Params{{Key: "session_id", Value: "session-1"}}
	ctx.Request = httptest.NewRequest(
		http.MethodPost,
		"/api/v1/sessions/session-1/agent-qa",
		bytes.NewBufferString(
			`{"query":"q","agent_id":"agent-1","agent_enabled":true,`+
				`"knowledge_scope":{"knowledge_base_ids":["kb-1"],`+
				`"folder_scopes":[{"knowledge_base_id":"kb-1",`+
				`"folder_ids":["`+phase4B1RequestFolderID+`"]}]}}`,
		),
	)
	ctx.Request.Header.Set("Content-Type", "application/json")

	h.AgentQA(ctx)

	require.Equal(t, 1, sessionStub.prepareCalls)
	assert.Same(t, agent, sessionStub.prepareInput.CustomAgent)
	assert.False(t, sessionStub.prepareInput.SharedAgent)
	require.NotNil(t, sessionStub.prepareInput.CanonicalRequest)
	assert.Equal(
		t,
		[]string{"kb-1"},
		sessionStub.prepareInput.CanonicalRequest.KnowledgeBaseIDs,
	)
	require.NotNil(t, sessionStub.prepareInput.CanonicalRequest.FolderScopes)
	require.Len(t, ctx.Errors, 1)
	var appErr *apperrors.AppError
	require.ErrorAs(t, ctx.Errors[0].Err, &appErr)
	assert.Equal(t, http.StatusServiceUnavailable, appErr.HTTPCode)
}

func TestAgentQADisabledModeStillRejectsEnabledNonEmptyFolderScope(
	t *testing.T,
) {
	gin.SetMode(gin.TestMode)
	folderScopes := []types.FolderScopeRequest{{
		KnowledgeBaseID: "kb-1",
		FolderIDs:       []string{phase4B1RequestFolderID},
	}}
	requestScope := &types.KnowledgeScopeRequest{
		KnowledgeBaseIDs: []string{"kb-1"},
		FolderScopes:     &folderScopes,
	}
	sessionStub := &phase4B1SessionServiceStub{
		session: &types.Session{ID: "session-1", TenantID: 77},
		preparation: newPhase4B1Preparation(
			t,
			requestScope,
			true,
			[]string{phase4B1ResolvedFolderID},
		),
	}
	h := &Handler{sessionService: sessionStub}
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Params = gin.Params{{Key: "session_id", Value: "session-1"}}
	requestContext := types.WithPrincipal(
		context.Background(),
		types.Principal{Type: types.PrincipalWebUser, ID: "user-1"},
	)
	ctx.Request = httptest.NewRequest(
		http.MethodPost,
		"/api/v1/sessions/session-1/agent-qa",
		bytes.NewBufferString(
			`{"query":"q","agent_enabled":false,`+
				`"knowledge_scope":{"knowledge_base_ids":["kb-1"],`+
				`"folder_scopes":[{"knowledge_base_id":"kb-1",`+
				`"folder_ids":["`+phase4B1RequestFolderID+`"]}]}}`,
		),
	).WithContext(requestContext)
	ctx.Request.Header.Set("Content-Type", "application/json")

	h.AgentQA(ctx)

	require.Equal(t, 1, sessionStub.prepareCalls)
	require.Len(t, ctx.Errors, 1)
	var appErr *apperrors.AppError
	require.ErrorAs(t, ctx.Errors[0].Err, &appErr)
	assert.Equal(t, http.StatusServiceUnavailable, appErr.HTTPCode)
	assert.Equal(
		t,
		"folder-scoped retrieval is temporarily unavailable",
		appErr.Message,
	)
}

func TestAgentQAPassesTrustedSharedAgentStateToScopePreparation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	prepareErr := apperrors.NewServiceUnavailableError(
		"stop after scope capture",
	)
	sessionStub := &phase4B1SessionServiceStub{
		session:    &types.Session{ID: "session-1", TenantID: 77},
		prepareErr: prepareErr,
	}
	agent := &types.CustomAgent{
		ID:       "shared-agent-1",
		TenantID: 88,
		Config: types.CustomAgentConfig{
			AgentMode: types.AgentModeSmartReasoning,
		},
	}
	h := &Handler{
		sessionService: sessionStub,
		agentShareService: &phase4B1AgentShareServiceStub{
			agent: agent,
		},
	}
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Params = gin.Params{{Key: "session_id", Value: "session-1"}}
	ctx.Set(types.UserIDContextKey.String(), "user-1")
	ctx.Set(types.TenantIDContextKey.String(), uint64(77))
	ctx.Request = httptest.NewRequest(
		http.MethodPost,
		"/api/v1/sessions/session-1/agent-qa",
		bytes.NewBufferString(
			`{"query":"q","agent_id":"shared-agent-1","agent_enabled":true,`+
				`"knowledge_scope":{"knowledge_base_ids":["kb-owner"]}}`,
		),
	)
	ctx.Request.Header.Set("Content-Type", "application/json")

	h.AgentQA(ctx)

	require.Equal(t, 1, sessionStub.prepareCalls)
	assert.Same(t, agent, sessionStub.prepareInput.CustomAgent)
	assert.True(t, sessionStub.prepareInput.SharedAgent)
	require.Len(t, ctx.Errors, 1)
	assert.Same(t, prepareErr, ctx.Errors[0].Err)
}

func TestSuggestionAttributionRestoresExplicitEmptyScopeBeforePreparation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	emptyFolderScopes := []types.FolderScopeRequest{}
	storedScope := &types.KnowledgeScopeRequest{
		KnowledgeBaseIDs: []string{"kb-1"},
		FolderScopes:     &emptyFolderScopes,
	}
	sessionStub := &phase4B1SessionServiceStub{
		session: &types.Session{ID: "session-1", TenantID: 77},
		prepareErr: apperrors.NewServiceUnavailableError(
			"folder-scoped retrieval is temporarily unavailable",
		),
	}
	suggestionStub := &phase4B1SuggestionServiceStub{scope: storedScope}
	h := &Handler{
		sessionService:    sessionStub,
		suggestionService: suggestionStub,
	}
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Params = gin.Params{{Key: "session_id", Value: "session-1"}}
	ctx.Request = httptest.NewRequest(
		http.MethodPost,
		"/api/v1/sessions/session-1/knowledge-qa",
		bytes.NewBufferString(
			`{"query":"suggested question","suggestion_attribution":{`+
				`"suggestion_set_id":"set-1","question_id":"question-1"}}`,
		),
	)
	ctx.Request.Header.Set("Content-Type", "application/json")

	h.KnowledgeQA(ctx)

	assert.Equal(t, 1, suggestionStub.calls)
	require.Equal(t, 1, sessionStub.prepareCalls)
	require.NotNil(t, sessionStub.prepareInput.CanonicalRequest)
	require.NotNil(t, sessionStub.prepareInput.CanonicalRequest.FolderScopes)
	assert.Empty(t, *sessionStub.prepareInput.CanonicalRequest.FolderScopes)
	assert.NotSame(t, storedScope, sessionStub.prepareInput.CanonicalRequest)
	require.Len(t, ctx.Errors, 1)
	var appErr *apperrors.AppError
	require.ErrorAs(t, ctx.Errors[0].Err, &appErr)
	assert.Equal(t, http.StatusServiceUnavailable, appErr.HTTPCode)
}

func TestSuggestionAttributionRejectsClientScopeMismatchBeforePreparation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	suggestionStub := &phase4B1SuggestionServiceStub{
		scope: &types.KnowledgeScopeRequest{
			KnowledgeBaseIDs: []string{"kb-1"},
		},
	}
	sessionStub := &phase4B1SessionServiceStub{}
	h := &Handler{
		sessionService:    sessionStub,
		suggestionService: suggestionStub,
	}
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Params = gin.Params{{Key: "session_id", Value: "session-1"}}
	ctx.Request = httptest.NewRequest(
		http.MethodPost,
		"/api/v1/sessions/session-1/knowledge-qa",
		bytes.NewBufferString(
			`{"query":"suggested question","knowledge_scope":{`+
				`"knowledge_base_ids":["kb-2"]},"suggestion_attribution":{`+
				`"suggestion_set_id":"set-1","question_id":"question-1"}}`,
		),
	)
	ctx.Request.Header.Set("Content-Type", "application/json")

	h.KnowledgeQA(ctx)

	assert.Equal(t, 1, suggestionStub.calls)
	assert.Equal(t, 0, sessionStub.prepareCalls)
	require.Len(t, ctx.Errors, 1)
	var appErr *apperrors.AppError
	require.ErrorAs(t, ctx.Errors[0].Err, &appErr)
	assert.Equal(t, http.StatusBadRequest, appErr.HTTPCode)
	assert.Equal(t, "invalid suggestion attribution", appErr.Message)
}

func TestSuggestionAttributionRejectsMissingServiceBeforePreparation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	sessionStub := &phase4B1SessionServiceStub{}
	h := &Handler{sessionService: sessionStub}
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Params = gin.Params{{Key: "session_id", Value: "session-1"}}
	ctx.Request = httptest.NewRequest(
		http.MethodPost,
		"/api/v1/sessions/session-1/knowledge-qa",
		bytes.NewBufferString(
			`{"query":"suggested question","suggestion_attribution":{`+
				`"suggestion_set_id":"set-1","question_id":"question-1"}}`,
		),
	)
	ctx.Request.Header.Set("Content-Type", "application/json")

	h.KnowledgeQA(ctx)

	assert.Equal(t, 0, sessionStub.prepareCalls)
	require.Len(t, ctx.Errors, 1)
	var appErr *apperrors.AppError
	require.ErrorAs(t, ctx.Errors[0].Err, &appErr)
	assert.Equal(t, http.StatusInternalServerError, appErr.HTTPCode)
	assert.Equal(t, "message suggestion operation failed", appErr.Message)
}

func TestSuggestionAttributionStoredKnowledgeFileScopeIgnoresLegacySelectors(
	t *testing.T,
) {
	gin.SetMode(gin.TestMode)
	storedScope := &types.KnowledgeScopeRequest{
		KnowledgeBaseIDs: []string{"kb-1"},
		KnowledgeIDs:     []string{"knowledge-1"},
	}
	sessionStub := &phase4B1SessionServiceStub{
		session: &types.Session{ID: "session-1", TenantID: 77},
		prepareErr: apperrors.NewServiceUnavailableError(
			"stop after scope capture",
		),
	}
	suggestionStub := &phase4B1SuggestionServiceStub{scope: storedScope}
	h := &Handler{
		sessionService:    sessionStub,
		suggestionService: suggestionStub,
	}
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Params = gin.Params{{Key: "session_id", Value: "session-1"}}
	ctx.Request = httptest.NewRequest(
		http.MethodPost,
		"/api/v1/sessions/session-1/knowledge-qa",
		bytes.NewBufferString(
			`{"query":"suggested question","knowledge_base_ids":["kb-1"],`+
				`"mentioned_items":[{"id":"kb-1","type":"kb"}],`+
				`"suggestion_attribution":{"suggestion_set_id":"set-1",`+
				`"question_id":"question-1"}}`,
		),
	)
	ctx.Request.Header.Set("Content-Type", "application/json")

	h.KnowledgeQA(ctx)

	require.Equal(t, 1, sessionStub.prepareCalls)
	require.NotNil(t, sessionStub.prepareInput.CanonicalRequest)
	assert.Equal(
		t,
		[]string{"knowledge-1"},
		sessionStub.prepareInput.CanonicalRequest.KnowledgeIDs,
	)
	assert.Nil(t, sessionStub.prepareInput.LegacyRequest)
}

func TestSuggestionAttributionStoredTagScopeIgnoresLegacySelectors(
	t *testing.T,
) {
	gin.SetMode(gin.TestMode)
	storedScope := &types.KnowledgeScopeRequest{
		TagScopes: []types.TagScope{{
			KnowledgeBaseID: "kb-1",
			TagIDs:          []string{"tag-1"},
		}},
	}
	sessionStub := &phase4B1SessionServiceStub{
		session: &types.Session{ID: "session-1", TenantID: 77},
		prepareErr: apperrors.NewServiceUnavailableError(
			"stop after scope capture",
		),
	}
	suggestionStub := &phase4B1SuggestionServiceStub{scope: storedScope}
	h := &Handler{
		sessionService:    sessionStub,
		suggestionService: suggestionStub,
	}
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Params = gin.Params{{Key: "session_id", Value: "session-1"}}
	ctx.Request = httptest.NewRequest(
		http.MethodPost,
		"/api/v1/sessions/session-1/knowledge-qa",
		bytes.NewBufferString(
			`{"query":"suggested question","knowledge_base_ids":["kb-1"],`+
				`"suggestion_attribution":{"suggestion_set_id":"set-1",`+
				`"question_id":"question-1"}}`,
		),
	)
	ctx.Request.Header.Set("Content-Type", "application/json")

	h.KnowledgeQA(ctx)

	require.Equal(t, 1, sessionStub.prepareCalls)
	require.NotNil(t, sessionStub.prepareInput.CanonicalRequest)
	require.Len(t, sessionStub.prepareInput.CanonicalRequest.TagScopes, 1)
	assert.Equal(
		t,
		[]string{"tag-1"},
		sessionStub.prepareInput.CanonicalRequest.TagScopes[0].TagIDs,
	)
	assert.Nil(t, sessionStub.prepareInput.LegacyRequest)
}

func TestSuggestionAttributionLegacyItemUsesOrdinaryRequestScope(
	t *testing.T,
) {
	gin.SetMode(gin.TestMode)
	sessionStub := &phase4B1SessionServiceStub{
		session: &types.Session{ID: "session-1", TenantID: 77},
		prepareErr: apperrors.NewServiceUnavailableError(
			"stop after scope capture",
		),
	}
	suggestionStub := &phase4B1SuggestionServiceStub{}
	h := &Handler{
		sessionService:    sessionStub,
		suggestionService: suggestionStub,
	}
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Params = gin.Params{{Key: "session_id", Value: "session-1"}}
	ctx.Request = httptest.NewRequest(
		http.MethodPost,
		"/api/v1/sessions/session-1/knowledge-qa",
		bytes.NewBufferString(
			`{"query":"legacy suggested question","knowledge_base_ids":["kb-current"],`+
				`"suggestion_attribution":{"suggestion_set_id":"set-1",`+
				`"question_id":"question-1"}}`,
		),
	)
	ctx.Request.Header.Set("Content-Type", "application/json")

	h.KnowledgeQA(ctx)

	require.Equal(t, 1, sessionStub.prepareCalls)
	assert.Nil(t, sessionStub.prepareInput.CanonicalRequest)
	require.NotNil(t, sessionStub.prepareInput.LegacyRequest)
	assert.Equal(
		t,
		[]string{"kb-current"},
		sessionStub.prepareInput.LegacyRequest.KnowledgeBaseIDs,
	)
}

func TestSuggestionAttributionInternalFailureIsNotBadRequest(t *testing.T) {
	gin.SetMode(gin.TestMode)
	sessionStub := &phase4B1SessionServiceStub{}
	suggestionStub := &phase4B1SuggestionServiceStub{
		err: stderrors.New("repository unavailable"),
	}
	h := &Handler{
		sessionService:    sessionStub,
		suggestionService: suggestionStub,
	}
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Params = gin.Params{{Key: "session_id", Value: "session-1"}}
	ctx.Request = httptest.NewRequest(
		http.MethodPost,
		"/api/v1/sessions/session-1/knowledge-qa",
		bytes.NewBufferString(
			`{"query":"suggested question","suggestion_attribution":{`+
				`"suggestion_set_id":"set-1","question_id":"question-1"}}`,
		),
	)
	ctx.Request.Header.Set("Content-Type", "application/json")

	h.KnowledgeQA(ctx)

	require.Len(t, ctx.Errors, 1)
	var appErr *apperrors.AppError
	require.ErrorAs(t, ctx.Errors[0].Err, &appErr)
	assert.Equal(t, http.StatusInternalServerError, appErr.HTTPCode)
	assert.Equal(t, "message suggestion operation failed", appErr.Message)
	assert.Equal(t, 0, sessionStub.prepareCalls)
}

func TestSuggestionAttributionCancellationPreservesErrorChain(t *testing.T) {
	gin.SetMode(gin.TestMode)
	sessionStub := &phase4B1SessionServiceStub{}
	suggestionStub := &phase4B1SuggestionServiceStub{
		err: fmt.Errorf("validate attribution: %w", context.Canceled),
	}
	h := &Handler{
		sessionService:    sessionStub,
		suggestionService: suggestionStub,
	}
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Params = gin.Params{{Key: "session_id", Value: "session-1"}}
	requestContext, cancel := context.WithCancel(context.Background())
	cancel()
	ctx.Request = httptest.NewRequest(
		http.MethodPost,
		"/api/v1/sessions/session-1/knowledge-qa",
		bytes.NewBufferString(
			`{"query":"suggested question","suggestion_attribution":{`+
				`"suggestion_set_id":"set-1","question_id":"question-1"}}`,
		),
	).WithContext(requestContext)
	ctx.Request.Header.Set("Content-Type", "application/json")

	h.KnowledgeQA(ctx)

	require.Len(t, ctx.Errors, 1)
	assert.ErrorIs(t, ctx.Errors[0].Err, context.Canceled)
	require.NotNil(t, suggestionStub.validationContext)
	assert.ErrorIs(t, suggestionStub.validationContext.Err(), context.Canceled)
	assert.Equal(t, 0, sessionStub.prepareCalls)
}

func TestPersistLastRequestStateStoresOnlyIndependentRequestScope(t *testing.T) {
	rawFolderScopes := []types.FolderScopeRequest{{
		KnowledgeBaseID:    "kb-1",
		FolderIDs:          []string{phase4B1RequestFolderID},
		IncludeDescendants: phase4B1Bool(false),
	}}
	requestScope := &types.KnowledgeScopeRequest{
		KnowledgeBaseIDs: []string{"kb-1"},
		FolderScopes:     &rawFolderScopes,
	}
	executionPreparation := newPhase4B1Preparation(
		t,
		requestScope,
		true,
		[]string{phase4B1ResolvedFolderID},
	)
	stub := &phase4B1SessionServiceStub{}
	h := &Handler{sessionService: stub}
	reqCtx := &qaRequestContext{
		sessionID:          "session-1",
		requestScope:       requestScope,
		executionScope:     executionPreparation.Execution(),
		executionScopeHash: executionPreparation.ExecutionScopeHash(),
	}

	h.persistLastRequestState(context.Background(), reqCtx, qaModeNormal)

	require.NotNil(t, stub.lastRequestState)
	require.NotNil(t, stub.lastRequestState.RequestScope)
	assert.Equal(
		t,
		phase4B1RequestFolderID,
		(*stub.lastRequestState.RequestScope.FolderScopes)[0].FolderIDs[0],
	)
	requestScope.KnowledgeBaseIDs[0] = "mutated-request"
	requestScopeFolder := &(*requestScope.FolderScopes)[0]
	requestScopeFolder.FolderIDs[0] = "mutated-folder"
	*requestScopeFolder.IncludeDescendants = true
	assert.Equal(
		t,
		"kb-1",
		stub.lastRequestState.RequestScope.KnowledgeBaseIDs[0],
	)
	assert.Equal(
		t,
		phase4B1RequestFolderID,
		(*stub.lastRequestState.RequestScope.FolderScopes)[0].FolderIDs[0],
	)
	assert.False(
		t,
		*(*stub.lastRequestState.RequestScope.FolderScopes)[0].IncludeDescendants,
	)

	serialized, err := json.Marshal(stub.lastRequestState)
	require.NoError(t, err)
	assert.NotContains(t, string(serialized), "execution_scope_hash")
	assert.NotContains(t, string(serialized), "source_tenant")
	assert.NotContains(t, string(serialized), phase4B1ResolvedFolderID)
}

func TestPreparedTitleGenerationContextContainsOnlySafeScopeSummary(
	t *testing.T,
) {
	preparation := newPhase4B1Preparation(
		t,
		&types.KnowledgeScopeRequest{
			KnowledgeBaseIDs: []string{"kb-1"},
		},
		true,
		[]string{phase4B1ResolvedFolderID},
	)

	ctx := preparedTitleGenerationContext(
		context.Background(),
		preparation.Execution(),
		preparation.ExecutionScopeHash(),
	)
	fields := logger.GetLogger(ctx).Data

	assert.Equal(t, true, fields["knowledge_scope_prepared"])
	assert.Equal(t, true, fields["folder_filter_enabled"])
	assert.Equal(t, "0123456789ab", fields["scope_hash_prefix"])
	encoded, err := json.Marshal(fields)
	require.NoError(t, err)
	assert.NotContains(t, string(encoded), phase4B1ResolvedFolderID)
	assert.NotContains(t, string(encoded), `"source_tenant_id"`)
}
