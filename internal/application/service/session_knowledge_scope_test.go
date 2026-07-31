package service

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"testing"
	"time"

	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	prepareScopeCallerTenant = uint64(41)
	prepareScopeSourceTenant = uint64(73)
	prepareScopeKBOne        = "prepare-kb-1"
	prepareScopeKBTwo        = "prepare-kb-2"
	prepareScopeKnowledgeOne = "prepare-knowledge-1"
	prepareScopeKnowledgeTwo = "prepare-knowledge-2"
	prepareScopeTagOne       = "prepare-tag-1"
	prepareScopeFolderOne    = "00000000-0000-0000-0000-000000000041"
	prepareScopeFolderTwo    = "00000000-0000-0000-0000-000000000042"
)

type prepareScopeAuthorizationRepositoryStub struct {
	knowledges        map[string]*types.Knowledge
	tags              map[string]*types.KnowledgeTag
	folders           map[string]*types.KnowledgeFolder
	knowledgeErr      error
	tagErr            error
	folderErr         error
	knowledgeCalls    int
	tagCalls          int
	folderCalls       int
	knowledgeContexts []context.Context
	tagContexts       []context.Context
	folderContexts    []context.Context
}

func (s *prepareScopeAuthorizationRepositoryStub) ListKnowledgeScopeReferencesByIDs(
	ctx context.Context,
	knowledgeIDs []string,
) ([]*types.Knowledge, error) {
	s.knowledgeCalls++
	s.knowledgeContexts = append(s.knowledgeContexts, ctx)
	if s.knowledgeErr != nil {
		return nil, s.knowledgeErr
	}
	result := make([]*types.Knowledge, 0, len(knowledgeIDs))
	for _, knowledgeID := range knowledgeIDs {
		if knowledge := s.knowledges[knowledgeID]; knowledge != nil {
			result = append(result, knowledge)
		}
	}
	return result, nil
}

func (s *prepareScopeAuthorizationRepositoryStub) ListKnowledgeTagScopeReferencesByIDs(
	ctx context.Context,
	tagIDs []string,
) ([]*types.KnowledgeTag, error) {
	s.tagCalls++
	s.tagContexts = append(s.tagContexts, ctx)
	if s.tagErr != nil {
		return nil, s.tagErr
	}
	result := make([]*types.KnowledgeTag, 0, len(tagIDs))
	for _, tagID := range tagIDs {
		if tag := s.tags[tagID]; tag != nil {
			result = append(result, tag)
		}
	}
	return result, nil
}

func (s *prepareScopeAuthorizationRepositoryStub) ListKnowledgeFolderScopeReferencesByIDs(
	ctx context.Context,
	folderIDs []string,
) ([]*types.KnowledgeFolder, error) {
	s.folderCalls++
	s.folderContexts = append(s.folderContexts, ctx)
	if s.folderErr != nil {
		return nil, s.folderErr
	}
	result := make([]*types.KnowledgeFolder, 0, len(folderIDs))
	for _, folderID := range folderIDs {
		if folder := s.folders[folderID]; folder != nil {
			result = append(result, folder)
		}
	}
	return result, nil
}

type prepareScopeKnowledgeBaseServiceStub struct {
	interfaces.KnowledgeBaseService
	knowledgeBases map[string]*types.KnowledgeBase
	err            error
	calls          int
	contexts       []context.Context
	requestedIDs   [][]string
	listErr        error
	listCalls      int
	listContexts   []context.Context
}

func (s *prepareScopeKnowledgeBaseServiceStub) GetKnowledgeBasesByIDsOnly(
	ctx context.Context,
	ids []string,
) ([]*types.KnowledgeBase, error) {
	s.calls++
	s.contexts = append(s.contexts, ctx)
	s.requestedIDs = append(s.requestedIDs, append([]string(nil), ids...))
	if s.err != nil {
		return nil, s.err
	}
	result := make([]*types.KnowledgeBase, 0, len(ids))
	for _, id := range ids {
		if knowledgeBase := s.knowledgeBases[id]; knowledgeBase != nil {
			result = append(result, knowledgeBase)
		}
	}
	return result, nil
}

func (s *prepareScopeKnowledgeBaseServiceStub) ListKnowledgeBases(
	ctx context.Context,
) ([]*types.KnowledgeBase, error) {
	s.listCalls++
	s.listContexts = append(s.listContexts, ctx)
	if s.listErr != nil {
		return nil, s.listErr
	}
	result := make([]*types.KnowledgeBase, 0, len(s.knowledgeBases))
	for _, knowledgeBase := range s.knowledgeBases {
		if knowledgeBase != nil {
			result = append(result, knowledgeBase)
		}
	}
	return result, nil
}

type prepareScopeMaterializationKey struct {
	tenantID uint64
	kbID     string
	afterID  string
}

type prepareScopeMaterializationPage struct {
	ids          []string
	hasMore      bool
	err          error
	beforeReturn func()
}

type prepareScopeMaterializationCall struct {
	ctx                context.Context
	tenantID           uint64
	kbID               string
	folderIDs          []string
	knowledgeIDsWasNil bool
	knowledgeIDs       []string
	afterID            string
	limit              int
}

type prepareScopeKnowledgeServiceStub struct {
	interfaces.KnowledgeService
	mu                   sync.Mutex
	tagKnowledgeIDs      map[string][]string
	tagCalls             int
	materializationPages map[prepareScopeMaterializationKey]prepareScopeMaterializationPage
	materializationCalls []prepareScopeMaterializationCall
}

func (s *prepareScopeKnowledgeServiceStub) ListKnowledgeIDsByTagIDs(
	_ context.Context,
	_ uint64,
	knowledgeBaseID string,
	_ []string,
) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tagCalls++
	return append([]string(nil), s.tagKnowledgeIDs[knowledgeBaseID]...), nil
}

func (s *prepareScopeKnowledgeServiceStub) ListActiveKnowledgeIDsByFolderIDs(
	ctx context.Context,
	tenantID uint64,
	kbID string,
	folderIDs []string,
	knowledgeIDs []string,
	afterID string,
	limit int,
) ([]string, bool, error) {
	call := prepareScopeMaterializationCall{
		ctx:                ctx,
		tenantID:           tenantID,
		kbID:               kbID,
		folderIDs:          append([]string(nil), folderIDs...),
		knowledgeIDsWasNil: knowledgeIDs == nil,
		knowledgeIDs:       append([]string(nil), knowledgeIDs...),
		afterID:            afterID,
		limit:              limit,
	}
	key := prepareScopeMaterializationKey{
		tenantID: tenantID,
		kbID:     kbID,
		afterID:  afterID,
	}

	s.mu.Lock()
	s.materializationCalls = append(s.materializationCalls, call)
	page := s.materializationPages[key]
	page.ids = append([]string(nil), page.ids...)
	s.mu.Unlock()

	if page.beforeReturn != nil {
		page.beforeReturn()
	}
	return page.ids, page.hasMore, page.err
}

func (s *prepareScopeKnowledgeServiceStub) setMaterializationPage(
	tenantID uint64,
	kbID string,
	afterID string,
	page prepareScopeMaterializationPage,
) {
	page.ids = append([]string(nil), page.ids...)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.materializationPages[prepareScopeMaterializationKey{
		tenantID: tenantID,
		kbID:     kbID,
		afterID:  afterID,
	}] = page
}

func (s *prepareScopeKnowledgeServiceStub) materializationCallSnapshot() []prepareScopeMaterializationCall {
	s.mu.Lock()
	defer s.mu.Unlock()
	calls := make(
		[]prepareScopeMaterializationCall,
		len(s.materializationCalls),
	)
	for index, call := range s.materializationCalls {
		call.folderIDs = append([]string(nil), call.folderIDs...)
		call.knowledgeIDs = append([]string(nil), call.knowledgeIDs...)
		calls[index] = call
	}
	return calls
}

func (s *prepareScopeKnowledgeServiceStub) materializationCallCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.materializationCalls)
}

func (s *prepareScopeKnowledgeServiceStub) tagCallCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.tagCalls
}

type prepareScopeShareCall struct {
	ctx             context.Context
	knowledgeBaseID string
	callerTenantID  uint64
	callerRole      types.TenantRole
	requiredRole    types.OrgMemberRole
}

type prepareScopeKBShareServiceStub struct {
	interfaces.KBShareService
	allowed map[string]bool
	errs    map[string]error
	calls   []prepareScopeShareCall
}

func (s *prepareScopeKBShareServiceStub) HasTenantKBPermission(
	ctx context.Context,
	knowledgeBaseID string,
	callerTenantID uint64,
	callerRole types.TenantRole,
	requiredRole types.OrgMemberRole,
) (bool, error) {
	s.calls = append(s.calls, prepareScopeShareCall{
		ctx:             ctx,
		knowledgeBaseID: knowledgeBaseID,
		callerTenantID:  callerTenantID,
		callerRole:      callerRole,
		requiredRole:    requiredRole,
	})
	if err := s.errs[knowledgeBaseID]; err != nil {
		return false, err
	}
	return s.allowed[knowledgeBaseID], nil
}

type prepareScopeResolverStub struct {
	err            error
	budgetErr      error
	budgetCalls    int
	budgetRequests []*types.KnowledgeScopeRequest
	calls          int
	contexts       []context.Context
	inputs         []types.KnowledgeScopeResolveInput
}

func (s *prepareScopeResolverStub) ValidateFolderSelectorBudget(
	request *types.KnowledgeScopeRequest,
) error {
	s.budgetCalls++
	s.budgetRequests = append(s.budgetRequests, request.Clone())
	return s.budgetErr
}

func (s *prepareScopeResolverStub) Resolve(
	ctx context.Context,
	input types.KnowledgeScopeResolveInput,
) (*types.KnowledgeScope, error) {
	s.calls++
	s.contexts = append(s.contexts, ctx)
	s.inputs = append(s.inputs, clonePrepareScopeResolveInput(input))
	if s.err != nil {
		return nil, s.err
	}

	targets := make([]types.KnowledgeScopeTarget, 0, len(input.AuthorizedTargets))
	for _, authorized := range input.AuthorizedTargets {
		folderIDs := []string(nil)
		folderEnabled := input.Request != nil && input.Request.FolderScopes != nil
		if folderEnabled {
			folderIDs = []string{}
			for _, folderScope := range *input.Request.FolderScopes {
				if folderScope.KnowledgeBaseID == authorized.KnowledgeBaseID {
					folderIDs = append(folderIDs, folderScope.FolderIDs...)
				}
			}
		}
		folderFilter, err := types.NewResolvedFolderFilter(folderEnabled, folderIDs)
		if err != nil {
			return nil, err
		}
		target, err := types.NewKnowledgeScopeTarget(
			authorized.KnowledgeBaseID,
			authorized.SourceTenantID,
			authorized.KnowledgeIDs,
			authorized.TagIDs,
			authorized.ScopeTagIDs,
			folderFilter,
		)
		if err != nil {
			return nil, err
		}
		targets = append(targets, target)
	}
	return types.NewKnowledgeScope(targets)
}

func clonePrepareScopeResolveInput(
	input types.KnowledgeScopeResolveInput,
) types.KnowledgeScopeResolveInput {
	cloned := types.KnowledgeScopeResolveInput{
		Request: input.Request.Clone(),
		AuthorizedTargets: make(
			[]types.AuthorizedKnowledgeScopeTarget,
			len(input.AuthorizedTargets),
		),
	}
	for index, target := range input.AuthorizedTargets {
		cloned.AuthorizedTargets[index] = types.AuthorizedKnowledgeScopeTarget{
			KnowledgeBaseID: target.KnowledgeBaseID,
			SourceTenantID:  target.SourceTenantID,
			KnowledgeIDs:    append([]string(nil), target.KnowledgeIDs...),
			TagIDs:          append([]string(nil), target.TagIDs...),
			ScopeTagIDs:     append([]string(nil), target.ScopeTagIDs...),
		}
	}
	return cloned
}

type prepareScopeFolderRepositoryStub struct {
	calls int
}

func (s *prepareScopeFolderRepositoryStub) RunKnowledgeFolderScopeReadSnapshot(
	_ context.Context,
	_ uint64,
	_ string,
	_ interfaces.KnowledgeFolderScopeReadSnapshotFunc,
) error {
	s.calls++
	return errors.New("unexpected folder snapshot")
}

type prepareScopeHarness struct {
	service        *sessionService
	authorization  *prepareScopeAuthorizationRepositoryStub
	knowledgeBases *prepareScopeKnowledgeBaseServiceStub
	knowledge      *prepareScopeKnowledgeServiceStub
	shares         *prepareScopeKBShareServiceStub
	resolver       *prepareScopeResolverStub
}

func newPrepareScopeHarness() *prepareScopeHarness {
	authorization := &prepareScopeAuthorizationRepositoryStub{
		knowledges: map[string]*types.Knowledge{},
		tags:       map[string]*types.KnowledgeTag{},
		folders:    map[string]*types.KnowledgeFolder{},
	}
	knowledgeBases := &prepareScopeKnowledgeBaseServiceStub{
		knowledgeBases: map[string]*types.KnowledgeBase{},
	}
	knowledge := &prepareScopeKnowledgeServiceStub{
		tagKnowledgeIDs:      map[string][]string{},
		materializationPages: map[prepareScopeMaterializationKey]prepareScopeMaterializationPage{},
	}
	shares := &prepareScopeKBShareServiceStub{
		allowed: map[string]bool{},
		errs:    map[string]error{},
	}
	resolver := &prepareScopeResolverStub{}
	return &prepareScopeHarness{
		service: &sessionService{
			knowledgeBaseService:   knowledgeBases,
			knowledgeService:       knowledge,
			kbShareService:         shares,
			knowledgeScopeAuthRepo: authorization,
			knowledgeScopeResolver: resolver,
		},
		authorization:  authorization,
		knowledgeBases: knowledgeBases,
		knowledge:      knowledge,
		shares:         shares,
		resolver:       resolver,
	}
}

func (h *prepareScopeHarness) addKnowledgeBase(
	id string,
	tenantID uint64,
) *types.KnowledgeBase {
	knowledgeBase := &types.KnowledgeBase{
		ID:       id,
		TenantID: tenantID,
		Type:     types.KnowledgeBaseTypeFAQ,
	}
	h.knowledgeBases.knowledgeBases[id] = knowledgeBase
	return knowledgeBase
}

func (h *prepareScopeHarness) addFolder(
	id string,
	knowledgeBaseID string,
	tenantID uint64,
) {
	h.authorization.folders[id] = &types.KnowledgeFolder{
		ID:              id,
		TenantID:        tenantID,
		KnowledgeBaseID: knowledgeBaseID,
	}
}

func (h *prepareScopeHarness) addKnowledge(
	id string,
	knowledgeBaseID string,
	tenantID uint64,
) {
	h.authorization.knowledges[id] = &types.Knowledge{
		ID:              id,
		TenantID:        tenantID,
		KnowledgeBaseID: knowledgeBaseID,
	}
}

func (h *prepareScopeHarness) addTag(
	id string,
	knowledgeBaseID string,
	tenantID uint64,
) {
	h.authorization.tags[id] = &types.KnowledgeTag{
		ID:              id,
		TenantID:        tenantID,
		KnowledgeBaseID: knowledgeBaseID,
	}
}

func prepareScopeExecutionTarget(
	t *testing.T,
	knowledgeBaseID string,
	sourceTenantID uint64,
	knowledgeIDs []string,
	tagIDs []string,
	scopeTagIDs []string,
	folderEnabled bool,
	folderIDs []string,
) types.KnowledgeScopeTarget {
	t.Helper()
	filter, err := types.NewResolvedFolderFilter(folderEnabled, folderIDs)
	require.NoError(t, err)
	target, err := types.NewKnowledgeScopeTarget(
		knowledgeBaseID,
		sourceTenantID,
		knowledgeIDs,
		tagIDs,
		scopeTagIDs,
		filter,
	)
	require.NoError(t, err)
	return target
}

func prepareScopeExecution(
	t *testing.T,
	targets ...types.KnowledgeScopeTarget,
) *types.KnowledgeScope {
	t.Helper()
	execution, err := types.NewKnowledgeScope(targets)
	require.NoError(t, err)
	return execution
}

func prepareScopeTargetByKB(
	t *testing.T,
	scope *types.KnowledgeScope,
	knowledgeBaseID string,
) types.KnowledgeScopeTarget {
	t.Helper()
	for _, target := range scope.Targets() {
		if target.KnowledgeBaseID() == knowledgeBaseID {
			return target
		}
	}
	require.FailNow(t, "knowledge scope target not found", knowledgeBaseID)
	return types.KnowledgeScopeTarget{}
}

func prepareScopeContext(tenantID uint64) context.Context {
	ctx := context.WithValue(
		context.Background(),
		types.TenantIDContextKey,
		tenantID,
	)
	ctx = context.WithValue(
		ctx,
		types.TenantRoleContextKey,
		types.TenantRoleOwner,
	)
	return types.WithPrincipal(ctx, types.Principal{
		Type: types.PrincipalWebUser,
		ID:   "prepare-scope-user",
	})
}

func requirePrepareScopeHTTPCode(
	t *testing.T,
	err error,
	expected int,
) *apperrors.AppError {
	t.Helper()
	require.Error(t, err)
	var appError *apperrors.AppError
	require.ErrorAs(t, err, &appError)
	require.Equal(t, expected, appError.HTTPCode)
	return appError
}

func prepareScopeFolders(scopes ...types.FolderScopeRequest) *[]types.FolderScopeRequest {
	result := append([]types.FolderScopeRequest(nil), scopes...)
	return &result
}

func prepareScopeBool(value bool) *bool {
	return &value
}

func TestPrepareKnowledgeScopeMaterializationFastPaths(t *testing.T) {
	testCases := []struct {
		name  string
		input types.KnowledgeScopePrepareInput
	}{
		{
			name: "legacy no folder",
			input: types.KnowledgeScopePrepareInput{
				LegacyRequest: &types.KnowledgeScopeRequest{
					KnowledgeBaseIDs: []string{prepareScopeKBOne},
				},
			},
		},
		{
			name: "folder null",
			input: types.KnowledgeScopePrepareInput{
				CanonicalRequest: &types.KnowledgeScopeRequest{
					KnowledgeBaseIDs: []string{prepareScopeKBOne},
					FolderScopes:     nil,
				},
			},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			harness := newPrepareScopeHarness()
			harness.addKnowledgeBase(
				prepareScopeKBOne,
				prepareScopeCallerTenant,
			)

			preparation, err := harness.service.PrepareKnowledgeScope(
				prepareScopeContext(prepareScopeCallerTenant),
				testCase.input,
			)

			require.NoError(t, err)
			require.NotNil(t, preparation)
			assert.Equal(t, 0, harness.knowledge.materializationCallCount())
			target := prepareScopeTargetByKB(
				t,
				preparation.Execution(),
				prepareScopeKBOne,
			)
			assert.False(t, target.FolderFilter().Enabled())
		})
	}

	t.Run("oversized candidates on nonquery targets", func(t *testing.T) {
		testCases := []struct {
			name          string
			folderEnabled bool
			folderIDs     []string
			wantIDs       []string
		}{
			{
				name:          "disabled is ignored and preserved",
				folderEnabled: false,
				wantIDs: []string{
					"knowledge-a",
					"knowledge-b",
					"knowledge-c",
					"knowledge-d",
				},
			},
			{
				name:          "enabled empty is ignored and cleared",
				folderEnabled: true,
				folderIDs:     []string{},
				wantIDs:       []string{},
			},
		}
		for _, testCase := range testCases {
			t.Run(testCase.name, func(t *testing.T) {
				harness := newPrepareScopeHarness()
				input := prepareScopeExecution(
					t,
					prepareScopeExecutionTarget(
						t,
						prepareScopeKBOne,
						prepareScopeCallerTenant,
						[]string{
							"knowledge-a",
							"knowledge-b",
							"knowledge-c",
							"knowledge-d",
						},
						nil,
						nil,
						testCase.folderEnabled,
						testCase.folderIDs,
					),
				)

				materialized, err :=
					harness.service.materializeKnowledgeScope(
						prepareScopeContext(prepareScopeCallerTenant),
						input,
						2,
						3,
					)

				require.NoError(t, err)
				require.NotNil(t, materialized)
				assert.Equal(
					t,
					testCase.wantIDs,
					prepareScopeTargetByKB(
						t,
						materialized,
						prepareScopeKBOne,
					).KnowledgeIDs(),
				)
				assert.Equal(
					t,
					0,
					harness.knowledge.materializationCallCount(),
				)
			})
		}
	})

	t.Run("mixed disabled and enabled empty", func(t *testing.T) {
		harness := newPrepareScopeHarness()
		input := prepareScopeExecution(
			t,
			prepareScopeExecutionTarget(
				t,
				prepareScopeKBOne,
				prepareScopeCallerTenant,
				[]string{prepareScopeKnowledgeOne},
				nil,
				nil,
				false,
				nil,
			),
			prepareScopeExecutionTarget(
				t,
				prepareScopeKBTwo,
				prepareScopeCallerTenant,
				[]string{prepareScopeKnowledgeTwo},
				[]string{prepareScopeTagOne},
				[]string{prepareScopeTagOne},
				true,
				[]string{},
			),
		)

		materialized, err := harness.service.materializeKnowledgeScope(
			prepareScopeContext(prepareScopeCallerTenant),
			input,
			2,
			3,
		)

		require.NoError(t, err)
		require.NotNil(t, materialized)
		assert.Equal(t, 0, harness.knowledge.materializationCallCount())
		assert.Equal(
			t,
			[]string{prepareScopeKnowledgeOne},
			prepareScopeTargetByKB(
				t,
				materialized,
				prepareScopeKBOne,
			).KnowledgeIDs(),
		)
		enabledEmpty := prepareScopeTargetByKB(
			t,
			materialized,
			prepareScopeKBTwo,
		)
		assert.True(t, enabledEmpty.FolderFilter().Empty())
		assert.Empty(t, enabledEmpty.KnowledgeIDs())
		assert.Equal(t, []string{prepareScopeTagOne}, enabledEmpty.TagIDs())
		assert.Equal(
			t,
			[]string{prepareScopeKnowledgeTwo},
			prepareScopeTargetByKB(
				t,
				input,
				prepareScopeKBTwo,
			).KnowledgeIDs(),
		)
	})
}

func TestPrepareKnowledgeScopeMaterializationCandidateTruthTable(t *testing.T) {
	type candidateTestCase struct {
		name                 string
		knowledgeBaseType    string
		explicitIDs          []string
		scopeTagIDs          []string
		resolvedTagIDs       []string
		materializedIDs      []string
		wantCall             bool
		wantCandidateWasNil  bool
		wantCandidate        []string
		wantKnowledgeIDs     []string
		wantPhysicalTagIDs   []string
		wantScopeTagIDs      []string
		wantTagResolutionRun bool
	}
	testCases := []candidateTestCase{
		{
			name:                "folder only is unrestricted",
			knowledgeBaseType:   types.KnowledgeBaseTypeDocument,
			materializedIDs:     []string{prepareScopeKnowledgeOne},
			wantCall:            true,
			wantCandidateWasNil: true,
			wantKnowledgeIDs:    []string{prepareScopeKnowledgeOne},
		},
		{
			name:              "folder and explicit is restricted",
			knowledgeBaseType: types.KnowledgeBaseTypeDocument,
			explicitIDs:       []string{prepareScopeKnowledgeTwo, prepareScopeKnowledgeOne},
			materializedIDs:   []string{prepareScopeKnowledgeOne},
			wantCall:          true,
			wantCandidate:     []string{prepareScopeKnowledgeOne, prepareScopeKnowledgeTwo},
			wantKnowledgeIDs:  []string{prepareScopeKnowledgeOne},
		},
		{
			name:                 "folder and document tag uses resolved candidate",
			knowledgeBaseType:    types.KnowledgeBaseTypeDocument,
			scopeTagIDs:          []string{prepareScopeTagOne},
			resolvedTagIDs:       []string{prepareScopeKnowledgeTwo, prepareScopeKnowledgeOne},
			materializedIDs:      []string{prepareScopeKnowledgeTwo},
			wantCall:             true,
			wantCandidate:        []string{prepareScopeKnowledgeOne, prepareScopeKnowledgeTwo},
			wantKnowledgeIDs:     []string{prepareScopeKnowledgeTwo},
			wantScopeTagIDs:      []string{prepareScopeTagOne},
			wantTagResolutionRun: true,
		},
		{
			name:                 "empty document tag is known empty",
			knowledgeBaseType:    types.KnowledgeBaseTypeDocument,
			scopeTagIDs:          []string{prepareScopeTagOne},
			wantScopeTagIDs:      []string{prepareScopeTagOne},
			wantTagResolutionRun: true,
		},
		{
			name:                 "empty document tag explicit intersection is known empty",
			knowledgeBaseType:    types.KnowledgeBaseTypeDocument,
			explicitIDs:          []string{prepareScopeKnowledgeOne},
			scopeTagIDs:          []string{prepareScopeTagOne},
			resolvedTagIDs:       []string{prepareScopeKnowledgeTwo},
			wantScopeTagIDs:      []string{prepareScopeTagOne},
			wantTagResolutionRun: true,
		},
		{
			name:                "faq tag only remains physical and unrestricted",
			knowledgeBaseType:   types.KnowledgeBaseTypeFAQ,
			scopeTagIDs:         []string{prepareScopeTagOne},
			materializedIDs:     []string{prepareScopeKnowledgeOne},
			wantCall:            true,
			wantCandidateWasNil: true,
			wantKnowledgeIDs:    []string{prepareScopeKnowledgeOne},
			wantPhysicalTagIDs:  []string{prepareScopeTagOne},
			wantScopeTagIDs:     []string{prepareScopeTagOne},
		},
		{
			name:               "faq tag and explicit uses explicit candidate",
			knowledgeBaseType:  types.KnowledgeBaseTypeFAQ,
			explicitIDs:        []string{prepareScopeKnowledgeOne},
			scopeTagIDs:        []string{prepareScopeTagOne},
			materializedIDs:    []string{prepareScopeKnowledgeOne},
			wantCall:           true,
			wantCandidate:      []string{prepareScopeKnowledgeOne},
			wantKnowledgeIDs:   []string{prepareScopeKnowledgeOne},
			wantPhysicalTagIDs: []string{prepareScopeTagOne},
			wantScopeTagIDs:    []string{prepareScopeTagOne},
		},
		{
			name:                "folder query zero is materialized zero",
			knowledgeBaseType:   types.KnowledgeBaseTypeDocument,
			wantCall:            true,
			wantCandidateWasNil: true,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			harness := newPrepareScopeHarness()
			knowledgeBase := harness.addKnowledgeBase(
				prepareScopeKBOne,
				prepareScopeSourceTenant,
			)
			knowledgeBase.Type = testCase.knowledgeBaseType
			harness.shares.allowed[prepareScopeKBOne] = true
			harness.addFolder(
				prepareScopeFolderOne,
				prepareScopeKBOne,
				prepareScopeSourceTenant,
			)
			for _, id := range testCase.explicitIDs {
				harness.addKnowledge(
					id,
					prepareScopeKBOne,
					prepareScopeSourceTenant,
				)
			}
			for _, id := range testCase.scopeTagIDs {
				harness.addTag(
					id,
					prepareScopeKBOne,
					prepareScopeSourceTenant,
				)
			}
			harness.knowledge.tagKnowledgeIDs[prepareScopeKBOne] = append(
				[]string(nil),
				testCase.resolvedTagIDs...,
			)
			if testCase.wantCall {
				harness.knowledge.setMaterializationPage(
					prepareScopeSourceTenant,
					prepareScopeKBOne,
					"",
					prepareScopeMaterializationPage{
						ids: append(
							[]string(nil),
							testCase.materializedIDs...,
						),
					},
				)
			}
			ctx := prepareScopeContext(prepareScopeCallerTenant)
			request := &types.KnowledgeScopeRequest{
				KnowledgeIDs: append(
					[]string(nil),
					testCase.explicitIDs...,
				),
				FolderScopes: prepareScopeFolders(
					types.FolderScopeRequest{
						KnowledgeBaseID: prepareScopeKBOne,
						FolderIDs: []string{
							prepareScopeFolderOne,
						},
						IncludeDescendants: prepareScopeBool(false),
					},
				),
			}
			if len(testCase.scopeTagIDs) > 0 {
				request.TagScopes = []types.TagScope{{
					KnowledgeBaseID: prepareScopeKBOne,
					TagIDs: append(
						[]string(nil),
						testCase.scopeTagIDs...,
					),
				}}
			}

			preparation, err := harness.service.PrepareKnowledgeScope(
				ctx,
				types.KnowledgeScopePrepareInput{
					CanonicalRequest: request,
				},
			)

			require.NoError(t, err)
			require.NotNil(t, preparation)
			if testCase.wantCall {
				calls := harness.knowledge.materializationCallSnapshot()
				require.Len(t, calls, 1)
				assert.Same(t, ctx, calls[0].ctx)
				assert.Equal(
					t,
					prepareScopeSourceTenant,
					calls[0].tenantID,
				)
				assert.Equal(t, prepareScopeKBOne, calls[0].kbID)
				assert.Equal(
					t,
					[]string{prepareScopeFolderOne},
					calls[0].folderIDs,
				)
				assert.Equal(
					t,
					testCase.wantCandidateWasNil,
					calls[0].knowledgeIDsWasNil,
				)
				assert.Equal(t, testCase.wantCandidate, calls[0].knowledgeIDs)
				assert.Empty(t, calls[0].afterID)
				assert.Equal(
					t,
					knowledgeScopeMaterializationPageSize,
					calls[0].limit,
				)
			} else {
				assert.Equal(
					t,
					0,
					harness.knowledge.materializationCallCount(),
				)
			}
			if testCase.wantTagResolutionRun {
				assert.Equal(t, 1, harness.knowledge.tagCallCount())
			} else {
				assert.Equal(t, 0, harness.knowledge.tagCallCount())
			}
			target := prepareScopeTargetByKB(
				t,
				preparation.Execution(),
				prepareScopeKBOne,
			)
			assert.True(t, target.FolderFilter().Enabled())
			assert.False(t, target.FolderFilter().Empty())
			assert.Equal(
				t,
				append([]string{}, testCase.wantKnowledgeIDs...),
				target.KnowledgeIDs(),
			)
			assert.Equal(
				t,
				append([]string{}, testCase.wantPhysicalTagIDs...),
				target.TagIDs(),
			)
			assert.Equal(
				t,
				append([]string{}, testCase.wantScopeTagIDs...),
				target.ScopeTagIDs(),
			)
		})
	}
}

func TestMaterializeKnowledgeScopePassesResolvedFolderFilters(t *testing.T) {
	testCases := []struct {
		name      string
		folderIDs []string
	}{
		{
			name: "descendant closure",
			folderIDs: []string{
				prepareScopeFolderOne,
				prepareScopeFolderTwo,
			},
		},
		{
			name: "multi folder union",
			folderIDs: []string{
				prepareScopeFolderTwo,
				prepareScopeFolderOne,
			},
		},
		{
			name:      "root direct preserves empty string",
			folderIDs: []string{types.KnowledgeFolderRootID},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			harness := newPrepareScopeHarness()
			execution := prepareScopeExecution(
				t,
				prepareScopeExecutionTarget(
					t,
					prepareScopeKBOne,
					prepareScopeSourceTenant,
					nil,
					nil,
					nil,
					true,
					testCase.folderIDs,
				),
			)

			materialized, err := harness.service.materializeKnowledgeScope(
				prepareScopeContext(prepareScopeCallerTenant),
				execution,
				2,
				3,
			)

			require.NoError(t, err)
			require.NotNil(t, materialized)
			calls := harness.knowledge.materializationCallSnapshot()
			require.Len(t, calls, 1)
			assert.Equal(
				t,
				prepareScopeTargetByKB(
					t,
					execution,
					prepareScopeKBOne,
				).FolderFilter().FolderIDs(),
				calls[0].folderIDs,
			)
			if testCase.name == "root direct preserves empty string" {
				assert.Equal(t, []string{""}, calls[0].folderIDs)
			}
		})
	}
}

func TestMaterializeKnowledgeScopeUsesStableTargetLocalCursors(t *testing.T) {
	harness := newPrepareScopeHarness()
	harness.knowledge.setMaterializationPage(
		prepareScopeCallerTenant,
		prepareScopeKBOne,
		"",
		prepareScopeMaterializationPage{
			ids:     []string{"knowledge-a", "knowledge-b"},
			hasMore: true,
		},
	)
	harness.knowledge.setMaterializationPage(
		prepareScopeCallerTenant,
		prepareScopeKBOne,
		"knowledge-b",
		prepareScopeMaterializationPage{
			ids: []string{"knowledge-c"},
		},
	)
	harness.knowledge.setMaterializationPage(
		prepareScopeCallerTenant,
		prepareScopeKBTwo,
		"",
		prepareScopeMaterializationPage{
			ids: []string{"knowledge-d"},
		},
	)
	execution := prepareScopeExecution(
		t,
		prepareScopeExecutionTarget(
			t,
			prepareScopeKBOne,
			prepareScopeCallerTenant,
			nil,
			nil,
			nil,
			true,
			[]string{prepareScopeFolderOne},
		),
		prepareScopeExecutionTarget(
			t,
			prepareScopeKBTwo,
			prepareScopeCallerTenant,
			nil,
			nil,
			nil,
			true,
			[]string{prepareScopeFolderTwo},
		),
	)

	materialized, err := harness.service.materializeKnowledgeScope(
		prepareScopeContext(prepareScopeCallerTenant),
		execution,
		2,
		10,
	)

	require.NoError(t, err)
	require.NotNil(t, materialized)
	assert.Equal(
		t,
		[]string{"knowledge-a", "knowledge-b", "knowledge-c"},
		prepareScopeTargetByKB(
			t,
			materialized,
			prepareScopeKBOne,
		).KnowledgeIDs(),
	)
	assert.Equal(
		t,
		[]string{"knowledge-d"},
		prepareScopeTargetByKB(
			t,
			materialized,
			prepareScopeKBTwo,
		).KnowledgeIDs(),
	)
	calls := harness.knowledge.materializationCallSnapshot()
	require.Len(t, calls, 3)
	assert.Equal(
		t,
		[]string{"", "knowledge-b", ""},
		[]string{calls[0].afterID, calls[1].afterID, calls[2].afterID},
	)
	assert.Equal(
		t,
		[]string{prepareScopeKBOne, prepareScopeKBOne, prepareScopeKBTwo},
		[]string{calls[0].kbID, calls[1].kbID, calls[2].kbID},
	)
	assert.Equal(t, []int{2, 2, 2}, []int{
		calls[0].limit,
		calls[1].limit,
		calls[2].limit,
	})
}

func TestMaterializeKnowledgeScopeRejectsPaginationContractViolations(
	t *testing.T,
) {
	testCases := []struct {
		name          string
		candidate     []string
		pages         map[string]prepareScopeMaterializationPage
		expectedCalls int
	}{
		{
			name: "has more with empty page",
			pages: map[string]prepareScopeMaterializationPage{
				"": {hasMore: true},
			},
			expectedCalls: 1,
		},
		{
			name: "has more with short page",
			pages: map[string]prepareScopeMaterializationPage{
				"": {
					ids:     []string{"knowledge-a"},
					hasMore: true,
				},
			},
			expectedCalls: 1,
		},
		{
			name: "page exceeds call limit",
			pages: map[string]prepareScopeMaterializationPage{
				"": {
					ids: []string{
						"knowledge-a",
						"knowledge-b",
						"knowledge-c",
					},
				},
			},
			expectedCalls: 1,
		},
		{
			name: "unordered page",
			pages: map[string]prepareScopeMaterializationPage{
				"": {
					ids: []string{"knowledge-b", "knowledge-a"},
				},
			},
			expectedCalls: 1,
		},
		{
			name: "duplicate page entry",
			pages: map[string]prepareScopeMaterializationPage{
				"": {
					ids: []string{"knowledge-a", "knowledge-a"},
				},
			},
			expectedCalls: 1,
		},
		{
			name: "empty knowledge id",
			pages: map[string]prepareScopeMaterializationPage{
				"": {
					ids: []string{""},
				},
			},
			expectedCalls: 1,
		},
		{
			name: "cursor does not advance",
			pages: map[string]prepareScopeMaterializationPage{
				"": {
					ids:     []string{"knowledge-a", "knowledge-b"},
					hasMore: true,
				},
				"knowledge-b": {
					ids: []string{"knowledge-a"},
				},
			},
			expectedCalls: 2,
		},
		{
			name:      "restricted result is outside candidate",
			candidate: []string{"knowledge-a", "knowledge-b"},
			pages: map[string]prepareScopeMaterializationPage{
				"": {
					ids: []string{"knowledge-c"},
				},
			},
			expectedCalls: 1,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			harness := newPrepareScopeHarness()
			for afterID, page := range testCase.pages {
				harness.knowledge.setMaterializationPage(
					prepareScopeCallerTenant,
					prepareScopeKBOne,
					afterID,
					page,
				)
			}
			execution := prepareScopeExecution(
				t,
				prepareScopeExecutionTarget(
					t,
					prepareScopeKBOne,
					prepareScopeCallerTenant,
					testCase.candidate,
					nil,
					nil,
					true,
					[]string{prepareScopeFolderOne},
				),
			)

			materialized, err := harness.service.materializeKnowledgeScope(
				prepareScopeContext(prepareScopeCallerTenant),
				execution,
				2,
				10,
			)

			require.Nil(t, materialized)
			appError := requirePrepareScopeHTTPCode(
				t,
				err,
				http.StatusInternalServerError,
			)
			assert.Equal(
				t,
				"knowledge scope preparation failed",
				appError.Message,
			)
			assert.False(
				t,
				errors.Is(err, types.ErrInvalidKnowledgeScopeRequest),
			)
			assert.Equal(
				t,
				testCase.expectedCalls,
				harness.knowledge.materializationCallCount(),
			)
		})
	}
}

func TestMaterializeKnowledgeScopeEnforcesCandidateAndMaterializedBudgets(
	t *testing.T,
) {
	assert.Equal(t, 10000, knowledgeScopeMaxMaterializedKnowledgeIDs)

	t.Run("production exact limit materializes completely", func(t *testing.T) {
		harness := newPrepareScopeHarness()
		knowledgeIDs := make(
			[]string,
			knowledgeScopeMaxMaterializedKnowledgeIDs,
		)
		for index := range knowledgeIDs {
			knowledgeIDs[index] = fmt.Sprintf("knowledge-%05d", index)
		}
		harness.knowledge.setMaterializationPage(
			prepareScopeCallerTenant,
			prepareScopeKBOne,
			"",
			prepareScopeMaterializationPage{ids: knowledgeIDs},
		)
		execution := prepareScopeExecution(
			t,
			prepareScopeExecutionTarget(
				t,
				prepareScopeKBOne,
				prepareScopeCallerTenant,
				knowledgeIDs,
				nil,
				nil,
				true,
				[]string{prepareScopeFolderOne},
			),
		)

		materialized, err := harness.service.materializeKnowledgeScope(
			prepareScopeContext(prepareScopeCallerTenant),
			execution,
			knowledgeScopeMaxMaterializedKnowledgeIDs,
			knowledgeScopeMaxMaterializedKnowledgeIDs,
		)

		require.NoError(t, err)
		require.NotNil(t, materialized)
		assert.Len(
			t,
			prepareScopeTargetByKB(
				t,
				materialized,
				prepareScopeKBOne,
			).KnowledgeIDs(),
			knowledgeScopeMaxMaterializedKnowledgeIDs,
		)
		assert.Equal(t, 1, harness.knowledge.materializationCallCount())
	})

	t.Run("candidate exact limit succeeds", func(t *testing.T) {
		harness := newPrepareScopeHarness()
		execution := prepareScopeExecution(
			t,
			prepareScopeExecutionTarget(
				t,
				prepareScopeKBOne,
				prepareScopeCallerTenant,
				[]string{"knowledge-a", "knowledge-b", "knowledge-c"},
				nil,
				nil,
				true,
				[]string{prepareScopeFolderOne},
			),
		)

		materialized, err := harness.service.materializeKnowledgeScope(
			prepareScopeContext(prepareScopeCallerTenant),
			execution,
			2,
			3,
		)

		require.NoError(t, err)
		require.NotNil(t, materialized)
		assert.Equal(t, 1, harness.knowledge.materializationCallCount())
	})

	t.Run("candidate limit plus one fails before every query", func(t *testing.T) {
		harness := newPrepareScopeHarness()
		execution := prepareScopeExecution(
			t,
			prepareScopeExecutionTarget(
				t,
				prepareScopeKBOne,
				prepareScopeCallerTenant,
				nil,
				nil,
				nil,
				true,
				[]string{prepareScopeFolderOne},
			),
			prepareScopeExecutionTarget(
				t,
				prepareScopeKBTwo,
				prepareScopeCallerTenant,
				[]string{
					"knowledge-a",
					"knowledge-b",
					"knowledge-c",
					"knowledge-d",
				},
				nil,
				nil,
				true,
				[]string{prepareScopeFolderTwo},
			),
		)

		materialized, err := harness.service.materializeKnowledgeScope(
			prepareScopeContext(prepareScopeCallerTenant),
			execution,
			2,
			3,
		)

		require.Nil(t, materialized)
		require.ErrorIs(t, err, types.ErrKnowledgeScopeTooLarge)
		appError := requirePrepareScopeHTTPCode(
			t,
			mapKnowledgeScopePreparationError(
				prepareScopeContext(prepareScopeCallerTenant),
				err,
			),
			http.StatusBadRequest,
		)
		assert.Equal(t, apperrors.ErrKnowledgeScopeTooLarge, appError.Code)
		assert.Equal(
			t,
			fmt.Sprintf(
				"knowledge scope exceeds the %d-file per-request limit; "+
					"select a smaller folder or reduce the selected scope",
				knowledgeScopeMaxMaterializedKnowledgeIDs,
			),
			appError.Message,
		)
		assert.Equal(t, 0, harness.knowledge.materializationCallCount())
	})

	t.Run("materialized exact limit succeeds", func(t *testing.T) {
		harness := newPrepareScopeHarness()
		harness.knowledge.setMaterializationPage(
			prepareScopeCallerTenant,
			prepareScopeKBOne,
			"",
			prepareScopeMaterializationPage{
				ids:     []string{"knowledge-a", "knowledge-b"},
				hasMore: true,
			},
		)
		harness.knowledge.setMaterializationPage(
			prepareScopeCallerTenant,
			prepareScopeKBOne,
			"knowledge-b",
			prepareScopeMaterializationPage{
				ids: []string{"knowledge-c"},
			},
		)
		execution := prepareScopeExecution(
			t,
			prepareScopeExecutionTarget(
				t,
				prepareScopeKBOne,
				prepareScopeCallerTenant,
				nil,
				nil,
				nil,
				true,
				[]string{prepareScopeFolderOne},
			),
		)

		materialized, err := harness.service.materializeKnowledgeScope(
			prepareScopeContext(prepareScopeCallerTenant),
			execution,
			2,
			3,
		)

		require.NoError(t, err)
		require.NotNil(t, materialized)
		assert.Equal(
			t,
			[]string{"knowledge-a", "knowledge-b", "knowledge-c"},
			prepareScopeTargetByKB(
				t,
				materialized,
				prepareScopeKBOne,
			).KnowledgeIDs(),
		)
	})

	t.Run("candidate count does not consume materialized budget", func(t *testing.T) {
		harness := newPrepareScopeHarness()
		harness.knowledge.setMaterializationPage(
			prepareScopeCallerTenant,
			prepareScopeKBOne,
			"",
			prepareScopeMaterializationPage{
				ids:     []string{"knowledge-a", "knowledge-b"},
				hasMore: true,
			},
		)
		harness.knowledge.setMaterializationPage(
			prepareScopeCallerTenant,
			prepareScopeKBOne,
			"knowledge-b",
			prepareScopeMaterializationPage{
				ids: []string{"knowledge-c"},
			},
		)
		execution := prepareScopeExecution(
			t,
			prepareScopeExecutionTarget(
				t,
				prepareScopeKBOne,
				prepareScopeCallerTenant,
				[]string{"knowledge-a", "knowledge-b", "knowledge-c"},
				nil,
				nil,
				true,
				[]string{prepareScopeFolderOne},
			),
		)

		materialized, err := harness.service.materializeKnowledgeScope(
			prepareScopeContext(prepareScopeCallerTenant),
			execution,
			2,
			3,
		)

		require.NoError(t, err)
		require.NotNil(t, materialized)
		assert.Len(
			t,
			prepareScopeTargetByKB(
				t,
				materialized,
				prepareScopeKBOne,
			).KnowledgeIDs(),
			3,
		)
		calls := harness.knowledge.materializationCallSnapshot()
		require.Len(t, calls, 2)
		assert.Equal(t, []int{2, 2}, []int{calls[0].limit, calls[1].limit})
	})

	t.Run("max plus one fails without partial scope", func(t *testing.T) {
		harness := newPrepareScopeHarness()
		harness.knowledge.setMaterializationPage(
			prepareScopeCallerTenant,
			prepareScopeKBOne,
			"",
			prepareScopeMaterializationPage{
				ids:     []string{"knowledge-a", "knowledge-b"},
				hasMore: true,
			},
		)
		harness.knowledge.setMaterializationPage(
			prepareScopeCallerTenant,
			prepareScopeKBOne,
			"knowledge-b",
			prepareScopeMaterializationPage{
				ids: []string{"knowledge-c", "knowledge-d"},
			},
		)
		execution := prepareScopeExecution(
			t,
			prepareScopeExecutionTarget(
				t,
				prepareScopeKBOne,
				prepareScopeCallerTenant,
				nil,
				nil,
				nil,
				true,
				[]string{prepareScopeFolderOne},
			),
		)

		materialized, err := harness.service.materializeKnowledgeScope(
			prepareScopeContext(prepareScopeCallerTenant),
			execution,
			2,
			3,
		)

		require.Nil(t, materialized)
		require.ErrorIs(t, err, types.ErrKnowledgeScopeTooLarge)
	})

	t.Run("exact remaining with more rows fails", func(t *testing.T) {
		harness := newPrepareScopeHarness()
		harness.knowledge.setMaterializationPage(
			prepareScopeCallerTenant,
			prepareScopeKBOne,
			"",
			prepareScopeMaterializationPage{
				ids:     []string{"knowledge-a", "knowledge-b"},
				hasMore: true,
			},
		)
		harness.knowledge.setMaterializationPage(
			prepareScopeCallerTenant,
			prepareScopeKBOne,
			"knowledge-b",
			prepareScopeMaterializationPage{
				ids:     []string{"knowledge-c", "knowledge-d"},
				hasMore: true,
			},
		)
		execution := prepareScopeExecution(
			t,
			prepareScopeExecutionTarget(
				t,
				prepareScopeKBOne,
				prepareScopeCallerTenant,
				nil,
				nil,
				nil,
				true,
				[]string{prepareScopeFolderOne},
			),
		)

		materialized, err := harness.service.materializeKnowledgeScope(
			prepareScopeContext(prepareScopeCallerTenant),
			execution,
			2,
			4,
		)

		require.Nil(t, materialized)
		require.ErrorIs(t, err, types.ErrKnowledgeScopeTooLarge)
	})

	t.Run("budget accumulates across targets", func(t *testing.T) {
		testCases := []struct {
			name       string
			secondPage []string
			wantError  bool
		}{
			{
				name:       "exact total",
				secondPage: []string{"knowledge-c"},
			},
			{
				name:       "limit plus one",
				secondPage: []string{"knowledge-c", "knowledge-d"},
				wantError:  true,
			},
		}
		for _, testCase := range testCases {
			t.Run(testCase.name, func(t *testing.T) {
				harness := newPrepareScopeHarness()
				harness.knowledge.setMaterializationPage(
					prepareScopeCallerTenant,
					prepareScopeKBOne,
					"",
					prepareScopeMaterializationPage{
						ids: []string{"knowledge-a", "knowledge-b"},
					},
				)
				harness.knowledge.setMaterializationPage(
					prepareScopeCallerTenant,
					prepareScopeKBTwo,
					"",
					prepareScopeMaterializationPage{
						ids: testCase.secondPage,
					},
				)
				execution := prepareScopeExecution(
					t,
					prepareScopeExecutionTarget(
						t,
						prepareScopeKBOne,
						prepareScopeCallerTenant,
						nil,
						nil,
						nil,
						true,
						[]string{prepareScopeFolderOne},
					),
					prepareScopeExecutionTarget(
						t,
						prepareScopeKBTwo,
						prepareScopeCallerTenant,
						nil,
						nil,
						nil,
						true,
						[]string{prepareScopeFolderTwo},
					),
				)

				materialized, err := harness.service.materializeKnowledgeScope(
					prepareScopeContext(prepareScopeCallerTenant),
					execution,
					2,
					3,
				)

				if testCase.wantError {
					require.Nil(t, materialized)
					require.ErrorIs(
						t,
						err,
						types.ErrKnowledgeScopeTooLarge,
					)
					return
				}
				require.NoError(t, err)
				require.NotNil(t, materialized)
				assert.Len(
					t,
					prepareScopeTargetByKB(
						t,
						materialized,
						prepareScopeKBOne,
					).KnowledgeIDs(),
					2,
				)
				assert.Len(
					t,
					prepareScopeTargetByKB(
						t,
						materialized,
						prepareScopeKBTwo,
					).KnowledgeIDs(),
					1,
				)
			})
		}
	})

	t.Run("nonpositive limits are internal failures", func(t *testing.T) {
		testCases := []struct {
			name         string
			pageSize     int
			maxKnowledge int
		}{
			{name: "zero page size", pageSize: 0, maxKnowledge: 3},
			{name: "zero maximum", pageSize: 2, maxKnowledge: 0},
		}
		for _, testCase := range testCases {
			t.Run(testCase.name, func(t *testing.T) {
				harness := newPrepareScopeHarness()
				execution := prepareScopeExecution(
					t,
					prepareScopeExecutionTarget(
						t,
						prepareScopeKBOne,
						prepareScopeCallerTenant,
						nil,
						nil,
						nil,
						true,
						[]string{prepareScopeFolderOne},
					),
				)

				materialized, err := harness.service.materializeKnowledgeScope(
					prepareScopeContext(prepareScopeCallerTenant),
					execution,
					testCase.pageSize,
					testCase.maxKnowledge,
				)

				require.Nil(t, materialized)
				requirePrepareScopeHTTPCode(
					t,
					err,
					http.StatusInternalServerError,
				)
				assert.Equal(
					t,
					0,
					harness.knowledge.materializationCallCount(),
				)
			})
		}
	})
}

func TestMaterializeKnowledgeScopeHandlesZeroRemainingAcrossTargets(
	t *testing.T,
) {
	testCases := []struct {
		name       string
		secondPage []string
		wantError  bool
	}{
		{
			name: "later target has zero rows",
		},
		{
			name:       "later target has one row",
			secondPage: []string{"knowledge-c"},
			wantError:  true,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			harness := newPrepareScopeHarness()
			harness.knowledge.setMaterializationPage(
				prepareScopeCallerTenant,
				prepareScopeKBOne,
				"",
				prepareScopeMaterializationPage{
					ids: []string{"knowledge-a", "knowledge-b"},
				},
			)
			harness.knowledge.setMaterializationPage(
				prepareScopeCallerTenant,
				prepareScopeKBTwo,
				"",
				prepareScopeMaterializationPage{
					ids: testCase.secondPage,
				},
			)
			execution := prepareScopeExecution(
				t,
				prepareScopeExecutionTarget(
					t,
					prepareScopeKBOne,
					prepareScopeCallerTenant,
					nil,
					nil,
					nil,
					true,
					[]string{prepareScopeFolderOne},
				),
				prepareScopeExecutionTarget(
					t,
					prepareScopeKBTwo,
					prepareScopeCallerTenant,
					nil,
					nil,
					nil,
					true,
					[]string{prepareScopeFolderTwo},
				),
			)

			materialized, err := harness.service.materializeKnowledgeScope(
				prepareScopeContext(prepareScopeCallerTenant),
				execution,
				2,
				2,
			)

			if testCase.wantError {
				require.Nil(t, materialized)
				require.ErrorIs(
					t,
					err,
					types.ErrKnowledgeScopeTooLarge,
				)
			} else {
				require.NoError(t, err)
				require.NotNil(t, materialized)
				assert.Empty(
					t,
					prepareScopeTargetByKB(
						t,
						materialized,
						prepareScopeKBTwo,
					).KnowledgeIDs(),
				)
			}
			calls := harness.knowledge.materializationCallSnapshot()
			require.Len(t, calls, 2)
			assert.Equal(t, 1, calls[1].limit)
			assert.Empty(t, calls[1].afterID)
		})
	}
}

func TestPrepareKnowledgeScopeMaterializationPreservesContextAndMapsErrors(
	t *testing.T,
) {
	newExecution := func(t *testing.T) *types.KnowledgeScope {
		t.Helper()
		return prepareScopeExecution(
			t,
			prepareScopeExecutionTarget(
				t,
				prepareScopeKBOne,
				prepareScopeCallerTenant,
				nil,
				nil,
				nil,
				true,
				[]string{prepareScopeFolderOne},
			),
		)
	}

	t.Run("page preflight sees canceled context", func(t *testing.T) {
		harness := newPrepareScopeHarness()
		ctx, cancel := context.WithCancel(
			prepareScopeContext(prepareScopeCallerTenant),
		)
		cancel()

		materialized, err := harness.service.materializeKnowledgeScope(
			ctx,
			newExecution(t),
			2,
			3,
		)

		require.Nil(t, materialized)
		require.ErrorIs(t, err, context.Canceled)
		assert.Equal(t, 0, harness.knowledge.materializationCallCount())
	})

	t.Run("service returns cancellation", func(t *testing.T) {
		harness := newPrepareScopeHarness()
		harness.knowledge.setMaterializationPage(
			prepareScopeCallerTenant,
			prepareScopeKBOne,
			"",
			prepareScopeMaterializationPage{
				err: fmt.Errorf("materialization stopped: %w", context.Canceled),
			},
		)

		materialized, err := harness.service.materializeKnowledgeScope(
			prepareScopeContext(prepareScopeCallerTenant),
			newExecution(t),
			2,
			3,
		)

		require.Nil(t, materialized)
		require.ErrorIs(t, err, context.Canceled)
		assert.Equal(t, 1, harness.knowledge.materializationCallCount())
	})

	t.Run("context cancels during service call", func(t *testing.T) {
		harness := newPrepareScopeHarness()
		ctx, cancel := context.WithCancel(
			prepareScopeContext(prepareScopeCallerTenant),
		)
		defer cancel()
		harness.knowledge.setMaterializationPage(
			prepareScopeCallerTenant,
			prepareScopeKBOne,
			"",
			prepareScopeMaterializationPage{
				err:          context.Canceled,
				beforeReturn: cancel,
			},
		)

		materialized, err := harness.service.materializeKnowledgeScope(
			ctx,
			newExecution(t),
			2,
			3,
		)

		require.Nil(t, materialized)
		require.ErrorIs(t, err, context.Canceled)
	})

	t.Run("context cancels after successful service result", func(t *testing.T) {
		harness := newPrepareScopeHarness()
		ctx, cancel := context.WithCancel(
			prepareScopeContext(prepareScopeCallerTenant),
		)
		defer cancel()
		harness.knowledge.setMaterializationPage(
			prepareScopeCallerTenant,
			prepareScopeKBOne,
			"",
			prepareScopeMaterializationPage{
				ids:          []string{"knowledge-a"},
				beforeReturn: cancel,
			},
		)

		materialized, err := harness.service.materializeKnowledgeScope(
			ctx,
			newExecution(t),
			2,
			3,
		)

		require.Nil(t, materialized)
		require.ErrorIs(t, err, context.Canceled)
	})

	t.Run("service deadline remains identifiable", func(t *testing.T) {
		harness := newPrepareScopeHarness()
		harness.knowledge.setMaterializationPage(
			prepareScopeCallerTenant,
			prepareScopeKBOne,
			"",
			prepareScopeMaterializationPage{
				err: fmt.Errorf(
					"materialization deadline: %w",
					context.DeadlineExceeded,
				),
			},
		)

		materialized, err := harness.service.materializeKnowledgeScope(
			prepareScopeContext(prepareScopeCallerTenant),
			newExecution(t),
			2,
			3,
		)

		require.Nil(t, materialized)
		require.ErrorIs(t, err, context.DeadlineExceeded)
	})

	t.Run("ordinary service error retains its helper chain", func(t *testing.T) {
		harness := newPrepareScopeHarness()
		repositoryErr := errors.New("private repository failure")
		harness.knowledge.setMaterializationPage(
			prepareScopeCallerTenant,
			prepareScopeKBOne,
			"",
			prepareScopeMaterializationPage{
				err: repositoryErr,
			},
		)

		materialized, err := harness.service.materializeKnowledgeScope(
			prepareScopeContext(prepareScopeCallerTenant),
			newExecution(t),
			2,
			3,
		)

		require.Nil(t, materialized)
		require.ErrorIs(t, err, repositoryErr)
		mapped := mapKnowledgeScopePreparationError(
			prepareScopeContext(prepareScopeCallerTenant),
			err,
		)
		appError := requirePrepareScopeHTTPCode(
			t,
			mapped,
			http.StatusInternalServerError,
		)
		assert.Equal(
			t,
			"knowledge scope preparation failed",
			appError.Message,
		)
	})

	t.Run("repository failures map to safe internal errors", func(t *testing.T) {
		testCases := []struct {
			name string
			err  error
		}{
			{
				name: "ordinary error",
				err: errors.New(
					"private SELECT knowledge_id FROM tenant_table",
				),
			},
			{
				name: "repository invalid request sentinel",
				err: fmt.Errorf(
					"private provider detail: %w",
					types.ErrInvalidKnowledgeScopeRequest,
				),
			},
		}
		for _, testCase := range testCases {
			t.Run(testCase.name, func(t *testing.T) {
				harness := newPrepareScopeHarness()
				harness.addKnowledgeBase(
					prepareScopeKBOne,
					prepareScopeCallerTenant,
				)
				harness.addFolder(
					prepareScopeFolderOne,
					prepareScopeKBOne,
					prepareScopeCallerTenant,
				)
				harness.knowledge.setMaterializationPage(
					prepareScopeCallerTenant,
					prepareScopeKBOne,
					"",
					prepareScopeMaterializationPage{
						err: testCase.err,
					},
				)

				preparation, err := harness.service.PrepareKnowledgeScope(
					prepareScopeContext(prepareScopeCallerTenant),
					types.KnowledgeScopePrepareInput{
						CanonicalRequest: &types.KnowledgeScopeRequest{
							FolderScopes: prepareScopeFolders(
								types.FolderScopeRequest{
									KnowledgeBaseID: prepareScopeKBOne,
									FolderIDs: []string{
										prepareScopeFolderOne,
									},
								},
							),
						},
					},
				)

				require.Nil(t, preparation)
				appError := requirePrepareScopeHTTPCode(
					t,
					err,
					http.StatusInternalServerError,
				)
				assert.Equal(
					t,
					"knowledge scope preparation failed",
					appError.Message,
				)
				assert.NotContains(t, appError.Message, "SELECT")
				assert.NotContains(t, appError.Message, "provider")
			})
		}
	})
}

func TestPrepareKnowledgeScopeMaterializationFailsAtomically(t *testing.T) {
	t.Run("second target failure publishes no preparation", func(t *testing.T) {
		harness := newPrepareScopeHarness()
		harness.addKnowledgeBase(
			prepareScopeKBOne,
			prepareScopeCallerTenant,
		)
		harness.addKnowledgeBase(
			prepareScopeKBTwo,
			prepareScopeCallerTenant,
		)
		harness.addFolder(
			prepareScopeFolderOne,
			prepareScopeKBOne,
			prepareScopeCallerTenant,
		)
		harness.addFolder(
			prepareScopeFolderTwo,
			prepareScopeKBTwo,
			prepareScopeCallerTenant,
		)
		harness.knowledge.setMaterializationPage(
			prepareScopeCallerTenant,
			prepareScopeKBOne,
			"",
			prepareScopeMaterializationPage{
				ids: []string{"knowledge-a"},
			},
		)
		harness.knowledge.setMaterializationPage(
			prepareScopeCallerTenant,
			prepareScopeKBTwo,
			"",
			prepareScopeMaterializationPage{
				err: errors.New("private second target failure"),
			},
		)
		request := &types.KnowledgeScopeRequest{
			FolderScopes: prepareScopeFolders(
				types.FolderScopeRequest{
					KnowledgeBaseID: prepareScopeKBOne,
					FolderIDs:       []string{prepareScopeFolderOne},
				},
				types.FolderScopeRequest{
					KnowledgeBaseID: prepareScopeKBTwo,
					FolderIDs:       []string{prepareScopeFolderTwo},
				},
			),
		}
		originalRequest := request.Clone()

		preparation, err := harness.service.PrepareKnowledgeScope(
			prepareScopeContext(prepareScopeCallerTenant),
			types.KnowledgeScopePrepareInput{
				CanonicalRequest: request,
			},
		)

		require.Nil(t, preparation)
		requirePrepareScopeHTTPCode(
			t,
			err,
			http.StatusInternalServerError,
		)
		assert.Equal(t, originalRequest, request)
		assert.Equal(t, 2, harness.knowledge.materializationCallCount())
	})

	t.Run("input execution remains unchanged", func(t *testing.T) {
		harness := newPrepareScopeHarness()
		harness.knowledge.setMaterializationPage(
			prepareScopeCallerTenant,
			prepareScopeKBOne,
			"",
			prepareScopeMaterializationPage{
				ids: []string{"knowledge-a"},
			},
		)
		harness.knowledge.setMaterializationPage(
			prepareScopeCallerTenant,
			prepareScopeKBTwo,
			"",
			prepareScopeMaterializationPage{
				err: errors.New("private second target failure"),
			},
		)
		execution := prepareScopeExecution(
			t,
			prepareScopeExecutionTarget(
				t,
				prepareScopeKBOne,
				prepareScopeCallerTenant,
				[]string{"knowledge-a", "knowledge-b"},
				nil,
				nil,
				true,
				[]string{prepareScopeFolderOne},
			),
			prepareScopeExecutionTarget(
				t,
				prepareScopeKBTwo,
				prepareScopeCallerTenant,
				nil,
				nil,
				nil,
				true,
				[]string{prepareScopeFolderTwo},
			),
		)

		materialized, err := harness.service.materializeKnowledgeScope(
			prepareScopeContext(prepareScopeCallerTenant),
			execution,
			2,
			3,
		)

		require.Nil(t, materialized)
		require.Error(t, err)
		assert.Equal(
			t,
			[]string{"knowledge-a", "knowledge-b"},
			prepareScopeTargetByKB(
				t,
				execution,
				prepareScopeKBOne,
			).KnowledgeIDs(),
		)
		assert.Empty(
			t,
			prepareScopeTargetByKB(
				t,
				execution,
				prepareScopeKBTwo,
			).KnowledgeIDs(),
		)
	})
}

func TestPrepareKnowledgeScopeMaterializationOwnsCopiesAndFinalHash(
	t *testing.T,
) {
	t.Run("authoritative tenant and stable materialized hash", func(t *testing.T) {
		harness := newPrepareScopeHarness()
		harness.addKnowledgeBase(
			prepareScopeKBOne,
			prepareScopeSourceTenant,
		)
		harness.shares.allowed[prepareScopeKBOne] = true
		harness.addFolder(
			prepareScopeFolderOne,
			prepareScopeKBOne,
			prepareScopeSourceTenant,
		)
		harness.knowledge.setMaterializationPage(
			prepareScopeSourceTenant,
			prepareScopeKBOne,
			"",
			prepareScopeMaterializationPage{
				ids: []string{"knowledge-a", "knowledge-b"},
			},
		)
		ctx := prepareScopeContext(prepareScopeCallerTenant)
		request := &types.KnowledgeScopeRequest{
			FolderScopes: prepareScopeFolders(
				types.FolderScopeRequest{
					KnowledgeBaseID: prepareScopeKBOne,
					FolderIDs:       []string{prepareScopeFolderOne},
				},
			),
		}

		first, err := harness.service.PrepareKnowledgeScope(
			ctx,
			types.KnowledgeScopePrepareInput{
				CanonicalRequest: request,
			},
		)
		require.NoError(t, err)
		second, err := harness.service.PrepareKnowledgeScope(
			ctx,
			types.KnowledgeScopePrepareInput{
				CanonicalRequest: request,
			},
		)

		require.NoError(t, err)
		require.NotNil(t, first)
		require.NotNil(t, second)
		assert.Equal(
			t,
			first.ExecutionScopeHash(),
			second.ExecutionScopeHash(),
		)
		assert.Empty(t, first.Request().KnowledgeIDs)
		assert.Equal(
			t,
			[]string{prepareScopeFolderOne},
			(*first.Request().FolderScopes)[0].FolderIDs,
		)
		calls := harness.knowledge.materializationCallSnapshot()
		require.Len(t, calls, 2)
		for _, call := range calls {
			assert.Same(t, ctx, call.ctx)
			assert.Equal(t, prepareScopeSourceTenant, call.tenantID)
			assert.NotEqual(t, prepareScopeCallerTenant, call.tenantID)
			assert.True(t, call.knowledgeIDsWasNil)
		}
		firstTarget := prepareScopeTargetByKB(
			t,
			first.Execution(),
			prepareScopeKBOne,
		)
		assert.Equal(t, prepareScopeSourceTenant, firstTarget.SourceTenantID())
		knowledgeIDs := firstTarget.KnowledgeIDs()
		folderIDs := firstTarget.FolderFilter().FolderIDs()
		require.Len(t, knowledgeIDs, 2)
		require.Len(t, folderIDs, 1)
		knowledgeIDs[0] = "mutated-knowledge"
		folderIDs[0] = "mutated-folder"
		stableTarget := prepareScopeTargetByKB(
			t,
			first.Execution(),
			prepareScopeKBOne,
		)
		assert.Equal(
			t,
			[]string{"knowledge-a", "knowledge-b"},
			stableTarget.KnowledgeIDs(),
		)
		assert.Equal(
			t,
			[]string{prepareScopeFolderOne},
			stableTarget.FolderFilter().FolderIDs(),
		)
		calls[0].folderIDs[0] = "mutated-call-folder"
		assert.Equal(
			t,
			prepareScopeFolderOne,
			harness.knowledge.materializationCallSnapshot()[0].folderIDs[0],
		)

		harness.knowledge.setMaterializationPage(
			prepareScopeSourceTenant,
			prepareScopeKBOne,
			"",
			prepareScopeMaterializationPage{
				ids: []string{"knowledge-c"},
			},
		)
		changed, err := harness.service.PrepareKnowledgeScope(
			ctx,
			types.KnowledgeScopePrepareInput{
				CanonicalRequest: request,
			},
		)
		require.NoError(t, err)
		require.NotNil(t, changed)
		assert.NotEqual(
			t,
			first.ExecutionScopeHash(),
			changed.ExecutionScopeHash(),
		)
		assert.Equal(
			t,
			[]string{"knowledge-c"},
			prepareScopeTargetByKB(
				t,
				changed.Execution(),
				prepareScopeKBOne,
			).KnowledgeIDs(),
		)
	})

	t.Run("enabled empty and materialized zero hashes differ", func(t *testing.T) {
		harness := newPrepareScopeHarness()
		harness.addKnowledgeBase(
			prepareScopeKBOne,
			prepareScopeCallerTenant,
		)
		harness.addFolder(
			prepareScopeFolderOne,
			prepareScopeKBOne,
			prepareScopeCallerTenant,
		)
		emptyFolders := []types.FolderScopeRequest{}
		enabledEmpty, err := harness.service.PrepareKnowledgeScope(
			prepareScopeContext(prepareScopeCallerTenant),
			types.KnowledgeScopePrepareInput{
				CanonicalRequest: &types.KnowledgeScopeRequest{
					KnowledgeBaseIDs: []string{prepareScopeKBOne},
					FolderScopes:     &emptyFolders,
				},
			},
		)
		require.NoError(t, err)
		materializedZero, err := harness.service.PrepareKnowledgeScope(
			prepareScopeContext(prepareScopeCallerTenant),
			types.KnowledgeScopePrepareInput{
				CanonicalRequest: &types.KnowledgeScopeRequest{
					FolderScopes: prepareScopeFolders(
						types.FolderScopeRequest{
							KnowledgeBaseID: prepareScopeKBOne,
							FolderIDs: []string{
								prepareScopeFolderOne,
							},
						},
					),
				},
			},
		)

		require.NoError(t, err)
		require.NotNil(t, enabledEmpty)
		require.NotNil(t, materializedZero)
		enabledEmptyTarget := prepareScopeTargetByKB(
			t,
			enabledEmpty.Execution(),
			prepareScopeKBOne,
		)
		materializedZeroTarget := prepareScopeTargetByKB(
			t,
			materializedZero.Execution(),
			prepareScopeKBOne,
		)
		assert.True(t, enabledEmptyTarget.FolderFilter().Empty())
		assert.Empty(t, enabledEmptyTarget.KnowledgeIDs())
		assert.True(t, materializedZeroTarget.FolderFilter().Enabled())
		assert.False(t, materializedZeroTarget.FolderFilter().Empty())
		assert.Empty(t, materializedZeroTarget.KnowledgeIDs())
		assert.NotEqual(
			t,
			enabledEmpty.ExecutionScopeHash(),
			materializedZero.ExecutionScopeHash(),
		)
	})
}

func TestPrepareKnowledgeScopeCanonicalLegacyEquivalentUsesCanonical(t *testing.T) {
	harness := newPrepareScopeHarness()
	harness.addKnowledgeBase(prepareScopeKBOne, prepareScopeCallerTenant)
	harness.addFolder(
		prepareScopeFolderOne,
		prepareScopeKBOne,
		prepareScopeCallerTenant,
	)
	canonical := &types.KnowledgeScopeRequest{
		KnowledgeBaseIDs: []string{prepareScopeKBOne},
		FolderScopes: prepareScopeFolders(types.FolderScopeRequest{
			KnowledgeBaseID:    prepareScopeKBOne,
			FolderIDs:          []string{prepareScopeFolderOne},
			IncludeDescendants: prepareScopeBool(false),
		}),
	}
	legacy := &types.KnowledgeScopeRequest{
		KnowledgeBaseIDs: []string{prepareScopeKBOne},
	}

	preparation, err := harness.service.PrepareKnowledgeScope(
		prepareScopeContext(prepareScopeCallerTenant),
		types.KnowledgeScopePrepareInput{
			CanonicalRequest: canonical,
			LegacyRequest:    legacy,
		},
	)

	require.NoError(t, err)
	require.NotNil(t, preparation)
	require.Equal(t, 1, harness.resolver.calls)
	request := preparation.Request()
	require.NotNil(t, request.FolderScopes)
	require.Len(t, *request.FolderScopes, 1)
	assert.Equal(t, prepareScopeFolderOne, (*request.FolderScopes)[0].FolderIDs[0])
	assert.False(t, *(*request.FolderScopes)[0].IncludeDescendants)
}

func TestPrepareKnowledgeScopeRejectsFolderSelectorBudgetBeforeMetadata(
	t *testing.T,
) {
	harness := newPrepareScopeHarness()
	folderRepository := &prepareScopeFolderRepositoryStub{}
	resolver, err := NewKnowledgeScopeResolver(
		folderRepository,
		KnowledgeScopeLimits{
			MaxSelectors:         100,
			MaxResolvedFolderIDs: 10000,
		},
	)
	require.NoError(t, err)
	harness.service.knowledgeScopeResolver = resolver

	folderIDs := make([]string, 101)
	for index := range folderIDs {
		folderIDs[index] = fmt.Sprintf(
			"00000000-0000-0000-0000-%012d",
			index+1,
		)
	}
	preparation, err := harness.service.PrepareKnowledgeScope(
		prepareScopeContext(prepareScopeCallerTenant),
		types.KnowledgeScopePrepareInput{
			CanonicalRequest: &types.KnowledgeScopeRequest{
				FolderScopes: prepareScopeFolders(
					types.FolderScopeRequest{
						KnowledgeBaseID: prepareScopeKBOne,
						FolderIDs:       folderIDs,
					},
				),
			},
		},
	)

	require.Nil(t, preparation)
	appError := requirePrepareScopeHTTPCode(t, err, http.StatusBadRequest)
	assert.Equal(t, "invalid knowledge scope", appError.Message)
	assert.Equal(t, 0, harness.authorization.folderCalls)
	assert.Equal(t, 0, folderRepository.calls)
	assert.Equal(t, 0, harness.knowledgeBases.calls)
}

func TestPrepareKnowledgeScopeCanonicalLegacyConflictIsBadRequest(t *testing.T) {
	harness := newPrepareScopeHarness()
	harness.addKnowledgeBase(prepareScopeKBOne, prepareScopeCallerTenant)
	harness.addKnowledgeBase(prepareScopeKBTwo, prepareScopeCallerTenant)

	preparation, err := harness.service.PrepareKnowledgeScope(
		prepareScopeContext(prepareScopeCallerTenant),
		types.KnowledgeScopePrepareInput{
			CanonicalRequest: &types.KnowledgeScopeRequest{
				KnowledgeBaseIDs: []string{prepareScopeKBOne},
			},
			LegacyRequest: &types.KnowledgeScopeRequest{
				KnowledgeBaseIDs: []string{prepareScopeKBTwo},
			},
		},
	)

	require.Nil(t, preparation)
	appError := requirePrepareScopeHTTPCode(t, err, http.StatusBadRequest)
	assert.Equal(t, "invalid knowledge scope", appError.Message)
	assert.Equal(t, 0, harness.resolver.calls)
	assert.Equal(t, 0, harness.knowledgeBases.calls)
}

func TestPrepareKnowledgeScopeObviousConflictDoesNotResolveKnowledge(t *testing.T) {
	harness := newPrepareScopeHarness()

	preparation, err := harness.service.PrepareKnowledgeScope(
		prepareScopeContext(prepareScopeCallerTenant),
		types.KnowledgeScopePrepareInput{
			CanonicalRequest: &types.KnowledgeScopeRequest{
				KnowledgeBaseIDs: []string{prepareScopeKBOne},
				KnowledgeIDs:     []string{prepareScopeKnowledgeOne},
			},
			LegacyRequest: &types.KnowledgeScopeRequest{
				KnowledgeBaseIDs: []string{prepareScopeKBTwo},
				KnowledgeIDs:     []string{prepareScopeKnowledgeOne},
			},
		},
	)

	require.Nil(t, preparation)
	appError := requirePrepareScopeHTTPCode(t, err, http.StatusBadRequest)
	assert.Equal(t, "invalid knowledge scope", appError.Message)
	assert.Equal(t, 0, harness.authorization.knowledgeCalls)
	assert.Equal(t, 0, harness.knowledgeBases.calls)
	assert.Equal(t, 0, harness.resolver.calls)
}

func TestPrepareKnowledgeScopeFolderScopeIntroducesParentKnowledgeBase(t *testing.T) {
	harness := newPrepareScopeHarness()
	harness.addKnowledgeBase(prepareScopeKBOne, prepareScopeCallerTenant)
	harness.addFolder(
		prepareScopeFolderOne,
		prepareScopeKBOne,
		prepareScopeCallerTenant,
	)
	canonical := &types.KnowledgeScopeRequest{
		FolderScopes: prepareScopeFolders(types.FolderScopeRequest{
			KnowledgeBaseID: prepareScopeKBOne,
			FolderIDs:       []string{prepareScopeFolderOne},
		}),
	}
	legacy := &types.KnowledgeScopeRequest{
		KnowledgeBaseIDs: []string{prepareScopeKBOne},
	}

	preparation, err := harness.service.PrepareKnowledgeScope(
		prepareScopeContext(prepareScopeCallerTenant),
		types.KnowledgeScopePrepareInput{
			CanonicalRequest: canonical,
			LegacyRequest:    legacy,
		},
	)

	require.NoError(t, err)
	require.NotNil(t, preparation)
	assert.Empty(t, preparation.Request().KnowledgeBaseIDs)
	require.Len(t, preparation.Execution().Targets(), 1)
	assert.Equal(
		t,
		prepareScopeKBOne,
		preparation.Execution().Targets()[0].KnowledgeBaseID(),
	)
}

func TestPrepareKnowledgeScopeUsesServerSourceTenant(t *testing.T) {
	harness := newPrepareScopeHarness()
	harness.addKnowledgeBase(prepareScopeKBOne, prepareScopeSourceTenant)
	harness.shares.allowed[prepareScopeKBOne] = true
	ctx := prepareScopeContext(prepareScopeCallerTenant)

	preparation, err := harness.service.PrepareKnowledgeScope(
		ctx,
		types.KnowledgeScopePrepareInput{
			CanonicalRequest: &types.KnowledgeScopeRequest{
				KnowledgeBaseIDs: []string{prepareScopeKBOne},
			},
		},
	)

	require.NoError(t, err)
	require.NotNil(t, preparation)
	require.Len(t, harness.shares.calls, 1)
	assert.Same(t, ctx, harness.shares.calls[0].ctx)
	assert.Equal(t, prepareScopeCallerTenant, harness.shares.calls[0].callerTenantID)
	assert.Equal(t, prepareScopeKBOne, harness.shares.calls[0].knowledgeBaseID)
	require.Len(t, harness.resolver.inputs, 1)
	require.Len(t, harness.resolver.inputs[0].AuthorizedTargets, 1)
	target := harness.resolver.inputs[0].AuthorizedTargets[0]
	assert.Equal(t, prepareScopeSourceTenant, target.SourceTenantID)
	assert.Equal(t, prepareScopeKBOne, target.KnowledgeBaseID)
	assert.Same(t, ctx, harness.resolver.contexts[0])
	executionTargets := preparation.Execution().Targets()
	require.Len(t, executionTargets, 1)
	assert.Equal(t, prepareScopeSourceTenant, executionTargets[0].SourceTenantID())
}

func TestPrepareKnowledgeScopeAPIKeyAllowlistCoversDerivedKnowledgeBases(t *testing.T) {
	testCases := []struct {
		name      string
		configure func(*prepareScopeHarness) *types.KnowledgeScopeRequest
	}{
		{
			name: "knowledge parent",
			configure: func(harness *prepareScopeHarness) *types.KnowledgeScopeRequest {
				harness.authorization.knowledges[prepareScopeKnowledgeOne] = &types.Knowledge{
					ID:              prepareScopeKnowledgeOne,
					TenantID:        prepareScopeCallerTenant,
					KnowledgeBaseID: prepareScopeKBTwo,
				}
				return &types.KnowledgeScopeRequest{
					KnowledgeIDs: []string{prepareScopeKnowledgeOne},
				}
			},
		},
		{
			name: "tag parent",
			configure: func(harness *prepareScopeHarness) *types.KnowledgeScopeRequest {
				harness.authorization.tags[prepareScopeTagOne] = &types.KnowledgeTag{
					ID:              prepareScopeTagOne,
					TenantID:        prepareScopeCallerTenant,
					KnowledgeBaseID: prepareScopeKBTwo,
				}
				return &types.KnowledgeScopeRequest{
					TagScopes: []types.TagScope{{
						KnowledgeBaseID: prepareScopeKBTwo,
						TagIDs:          []string{prepareScopeTagOne},
					}},
				}
			},
		},
		{
			name: "folder parent",
			configure: func(harness *prepareScopeHarness) *types.KnowledgeScopeRequest {
				harness.addFolder(
					prepareScopeFolderOne,
					prepareScopeKBTwo,
					prepareScopeCallerTenant,
				)
				return &types.KnowledgeScopeRequest{
					FolderScopes: prepareScopeFolders(types.FolderScopeRequest{
						KnowledgeBaseID: prepareScopeKBTwo,
						FolderIDs:       []string{prepareScopeFolderOne},
					}),
				}
			},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			harness := newPrepareScopeHarness()
			harness.addKnowledgeBase(prepareScopeKBTwo, prepareScopeCallerTenant)
			ctx := types.WithTenantAPIKeyScope(
				prepareScopeContext(prepareScopeCallerTenant),
				types.TenantAPIKeyScope{
					KeyID:            9001,
					KnowledgeBaseIDs: types.StringArray{prepareScopeKBOne},
				},
			)

			preparation, err := harness.service.PrepareKnowledgeScope(
				ctx,
				types.KnowledgeScopePrepareInput{
					CanonicalRequest: testCase.configure(harness),
				},
			)

			require.Nil(t, preparation)
			requirePrepareScopeHTTPCode(t, err, http.StatusForbidden)
			assert.Equal(t, 0, harness.resolver.calls)
			assert.Empty(t, harness.shares.calls)
		})
	}
}

func TestPrepareKnowledgeScopeFailsClosedBeforeResolver(t *testing.T) {
	t.Run("missing knowledge", func(t *testing.T) {
		harness := newPrepareScopeHarness()

		preparation, err := harness.service.PrepareKnowledgeScope(
			prepareScopeContext(prepareScopeCallerTenant),
			types.KnowledgeScopePrepareInput{
				CanonicalRequest: &types.KnowledgeScopeRequest{
					KnowledgeIDs: []string{prepareScopeKnowledgeOne},
				},
			},
		)

		require.Nil(t, preparation)
		requirePrepareScopeHTTPCode(t, err, http.StatusNotFound)
		assert.Equal(t, 0, harness.resolver.calls)
		assert.Equal(t, 0, harness.knowledgeBases.calls)
	})

	t.Run("folder belongs to another knowledge base", func(t *testing.T) {
		harness := newPrepareScopeHarness()
		harness.addFolder(
			prepareScopeFolderOne,
			prepareScopeKBTwo,
			prepareScopeCallerTenant,
		)

		preparation, err := harness.service.PrepareKnowledgeScope(
			prepareScopeContext(prepareScopeCallerTenant),
			types.KnowledgeScopePrepareInput{
				CanonicalRequest: &types.KnowledgeScopeRequest{
					FolderScopes: prepareScopeFolders(types.FolderScopeRequest{
						KnowledgeBaseID: prepareScopeKBOne,
						FolderIDs:       []string{prepareScopeFolderOne},
					}),
				},
			},
		)

		require.Nil(t, preparation)
		requirePrepareScopeHTTPCode(t, err, http.StatusNotFound)
		assert.Equal(t, 1, harness.authorization.folderCalls)
		assert.Equal(t, 0, harness.knowledgeBases.calls)
		assert.Equal(t, 0, harness.resolver.calls)
	})

	t.Run("permission denied", func(t *testing.T) {
		harness := newPrepareScopeHarness()
		harness.addKnowledgeBase(prepareScopeKBOne, prepareScopeCallerTenant)
		harness.addKnowledgeBase(prepareScopeKBTwo, prepareScopeSourceTenant)
		harness.shares.allowed[prepareScopeKBTwo] = false

		preparation, err := harness.service.PrepareKnowledgeScope(
			prepareScopeContext(prepareScopeCallerTenant),
			types.KnowledgeScopePrepareInput{
				CanonicalRequest: &types.KnowledgeScopeRequest{
					KnowledgeBaseIDs: []string{
						prepareScopeKBOne,
						prepareScopeKBTwo,
					},
				},
			},
		)

		require.Nil(t, preparation)
		requirePrepareScopeHTTPCode(t, err, http.StatusForbidden)
		assert.Equal(t, 0, harness.resolver.calls)
	})
}

func TestPrepareKnowledgeScopeTrustedAgentScopeModes(
	t *testing.T,
) {
	t.Run("tenantless builtin uses caller tenant", func(t *testing.T) {
		harness := newPrepareScopeHarness()
		harness.addKnowledgeBase(prepareScopeKBOne, prepareScopeCallerTenant)
		agent := &types.CustomAgent{
			IsBuiltin: true,
			Config: types.CustomAgentConfig{
				KBSelectionMode: "all",
			},
		}

		preparation, err := harness.service.PrepareKnowledgeScope(
			prepareScopeContext(prepareScopeCallerTenant),
			types.KnowledgeScopePrepareInput{
				Session: &types.Session{
					TenantID: prepareScopeCallerTenant,
				},
				CustomAgent: agent,
				SharedAgent: false,
			},
		)

		require.NoError(t, err)
		require.NotNil(t, preparation)
		assert.Zero(t, agent.TenantID)
		require.NotEmpty(t, harness.knowledgeBases.listContexts)
		for _, listContext := range harness.knowledgeBases.listContexts {
			assert.Equal(
				t,
				prepareScopeCallerTenant,
				types.MustTenantIDFromContext(listContext),
			)
		}
		targets := preparation.Execution().Targets()
		require.Len(t, targets, 1)
		assert.Equal(t, prepareScopeKBOne, targets[0].KnowledgeBaseID())
		assert.Equal(t, prepareScopeCallerTenant, targets[0].SourceTenantID())
		assert.Empty(t, harness.shares.calls)
	})

	t.Run("own agent uses own tenant", func(t *testing.T) {
		harness := newPrepareScopeHarness()
		harness.addKnowledgeBase(prepareScopeKBOne, prepareScopeCallerTenant)
		agent := &types.CustomAgent{
			TenantID: prepareScopeCallerTenant,
			Config: types.CustomAgentConfig{
				KBSelectionMode: "selected",
				KnowledgeBases:  []string{prepareScopeKBOne},
			},
		}

		preparation, err := harness.service.PrepareKnowledgeScope(
			prepareScopeContext(prepareScopeCallerTenant),
			types.KnowledgeScopePrepareInput{
				Session: &types.Session{
					TenantID: prepareScopeCallerTenant,
				},
				CustomAgent: agent,
				SharedAgent: false,
			},
		)

		require.NoError(t, err)
		require.NotNil(t, preparation)
		targets := preparation.Execution().Targets()
		require.Len(t, targets, 1)
		assert.Equal(t, prepareScopeKBOne, targets[0].KnowledgeBaseID())
		assert.Equal(t, prepareScopeCallerTenant, targets[0].SourceTenantID())
		assert.Empty(t, harness.shares.calls)
	})

	t.Run("real shared agent uses owner tenant", func(t *testing.T) {
		harness := newPrepareScopeHarness()
		harness.addKnowledgeBase(prepareScopeKBOne, prepareScopeSourceTenant)
		harness.shares.allowed[prepareScopeKBOne] = true
		agent := &types.CustomAgent{
			TenantID: prepareScopeSourceTenant,
			Config: types.CustomAgentConfig{
				KBSelectionMode: "all",
			},
		}

		preparation, err := harness.service.PrepareKnowledgeScope(
			prepareScopeContext(prepareScopeCallerTenant),
			types.KnowledgeScopePrepareInput{
				Session: &types.Session{
					TenantID: prepareScopeCallerTenant,
				},
				CustomAgent: agent,
				SharedAgent: true,
			},
		)

		require.NoError(t, err)
		require.NotNil(t, preparation)
		require.GreaterOrEqual(t, harness.knowledgeBases.listCalls, 1)
		for _, listContext := range harness.knowledgeBases.listContexts {
			assert.Equal(
				t,
				prepareScopeSourceTenant,
				types.MustTenantIDFromContext(listContext),
			)
		}
		targets := preparation.Execution().Targets()
		require.Len(t, targets, 1)
		assert.Equal(t, prepareScopeKBOne, targets[0].KnowledgeBaseID())
		assert.Equal(t, prepareScopeSourceTenant, targets[0].SourceTenantID())
	})

	t.Run("real shared source list failure", func(t *testing.T) {
		harness := newPrepareScopeHarness()
		harness.knowledgeBases.listErr = errors.New("private database error")
		agent := &types.CustomAgent{
			TenantID: prepareScopeSourceTenant,
			Config: types.CustomAgentConfig{
				KBSelectionMode: "all",
			},
		}

		preparation, err := harness.service.PrepareKnowledgeScope(
			prepareScopeContext(prepareScopeCallerTenant),
			types.KnowledgeScopePrepareInput{
				Session: &types.Session{
					TenantID: prepareScopeCallerTenant,
				},
				CustomAgent: agent,
				SharedAgent: true,
			},
		)

		require.Nil(t, preparation)
		appError := requirePrepareScopeHTTPCode(
			t,
			err,
			http.StatusInternalServerError,
		)
		assert.Equal(t, "knowledge scope preparation failed", appError.Message)
		assert.NotContains(t, appError.Message, "private database error")
		assert.Equal(t, 0, harness.resolver.calls)
	})
}

func TestPrepareKnowledgeScopeSharedAgentRejectsThirdPartyTargetsBeforeResolver(
	t *testing.T,
) {
	const thirdPartyTenantID = uint64(99)

	testCases := []struct {
		name      string
		targetIDs []string
	}{
		{
			name:      "single third-party target",
			targetIDs: []string{prepareScopeKBTwo},
		},
		{
			name: "mixed agent and third-party targets",
			targetIDs: []string{
				prepareScopeKBOne,
				prepareScopeKBTwo,
			},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			harness := newPrepareScopeHarness()
			harness.addKnowledgeBase(
				prepareScopeKBOne,
				prepareScopeSourceTenant,
			)
			harness.addKnowledgeBase(
				prepareScopeKBTwo,
				thirdPartyTenantID,
			)
			harness.shares.allowed[prepareScopeKBOne] = true
			harness.shares.allowed[prepareScopeKBTwo] = true
			agent := &types.CustomAgent{
				TenantID: prepareScopeSourceTenant,
				Config: types.CustomAgentConfig{
					KBSelectionMode: "selected",
					KnowledgeBases: []string{
						prepareScopeKBOne,
						prepareScopeKBTwo,
					},
				},
			}
			ctx := types.WithTenantAPIKeyScope(
				prepareScopeContext(prepareScopeCallerTenant),
				types.TenantAPIKeyScope{
					KeyID: 9002,
					KnowledgeBaseIDs: types.StringArray{
						prepareScopeKBOne,
						prepareScopeKBTwo,
					},
				},
			)

			preparation, err := harness.service.PrepareKnowledgeScope(
				ctx,
				types.KnowledgeScopePrepareInput{
					CanonicalRequest: &types.KnowledgeScopeRequest{
						KnowledgeBaseIDs: append(
							[]string(nil),
							testCase.targetIDs...,
						),
					},
					Session: &types.Session{
						TenantID: prepareScopeCallerTenant,
					},
					CustomAgent: agent,
					SharedAgent: true,
				},
			)

			require.Nil(t, preparation)
			appError := requirePrepareScopeHTTPCode(
				t,
				err,
				http.StatusForbidden,
			)
			assert.Equal(
				t,
				"knowledge scope is not authorized",
				appError.Message,
			)
			assert.Equal(t, 0, harness.resolver.calls)
			assert.Empty(t, harness.shares.calls)
		})
	}
}

func TestPrepareKnowledgeScopePreservesContextCancellation(t *testing.T) {
	t.Run("canceled", func(t *testing.T) {
		ctx, cancel := context.WithCancel(prepareScopeContext(prepareScopeCallerTenant))
		cancel()

		preparation, err := (&sessionService{}).PrepareKnowledgeScope(
			ctx,
			types.KnowledgeScopePrepareInput{},
		)

		require.Nil(t, preparation)
		require.ErrorIs(t, err, context.Canceled)
	})

	t.Run("deadline", func(t *testing.T) {
		ctx, cancel := context.WithDeadline(
			prepareScopeContext(prepareScopeCallerTenant),
			time.Now().Add(-time.Second),
		)
		defer cancel()

		preparation, err := (&sessionService{}).PrepareKnowledgeScope(
			ctx,
			types.KnowledgeScopePrepareInput{},
		)

		require.Nil(t, preparation)
		require.ErrorIs(t, err, context.DeadlineExceeded)
	})
}

func TestPrepareKnowledgeScopeNoFolderRequestSkipsFolderSnapshot(t *testing.T) {
	harness := newPrepareScopeHarness()
	harness.addKnowledgeBase(prepareScopeKBOne, prepareScopeCallerTenant)
	folderRepository := &prepareScopeFolderRepositoryStub{}
	resolver, err := NewKnowledgeScopeResolver(
		folderRepository,
		KnowledgeScopeLimits{
			MaxSelectors:         10,
			MaxResolvedFolderIDs: 100,
		},
	)
	require.NoError(t, err)
	harness.service.knowledgeScopeResolver = resolver

	preparation, err := harness.service.PrepareKnowledgeScope(
		prepareScopeContext(prepareScopeCallerTenant),
		types.KnowledgeScopePrepareInput{
			CanonicalRequest: &types.KnowledgeScopeRequest{
				KnowledgeBaseIDs: []string{prepareScopeKBOne},
			},
		},
	)

	require.NoError(t, err)
	require.NotNil(t, preparation)
	assert.Equal(t, 0, harness.authorization.folderCalls)
	assert.Equal(t, 0, folderRepository.calls)
	assert.Equal(t, 0, harness.knowledge.materializationCallCount())
	targets := preparation.Execution().Targets()
	require.Len(t, targets, 1)
	assert.False(t, targets[0].FolderFilter().Enabled())
	assert.Empty(t, targets[0].FolderFilter().FolderIDs())
}

func TestPrepareKnowledgeScopeVirtualRootSkipsFolderMetadataAndSnapshot(
	t *testing.T,
) {
	testCases := []struct {
		name               string
		includeDescendants bool
		wantEnabled        bool
		wantFolderIDs      []string
		wantMaterialCalls  int
	}{
		{
			name:               "direct root",
			includeDescendants: false,
			wantEnabled:        true,
			wantFolderIDs:      []string{types.KnowledgeFolderRootID},
			wantMaterialCalls:  1,
		},
		{
			name:               "recursive root",
			includeDescendants: true,
			wantEnabled:        false,
			wantFolderIDs:      []string{},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			harness := newPrepareScopeHarness()
			harness.addKnowledgeBase(
				prepareScopeKBOne,
				prepareScopeCallerTenant,
			)
			folderRepository := &prepareScopeFolderRepositoryStub{}
			resolver, err := NewKnowledgeScopeResolver(
				folderRepository,
				KnowledgeScopeLimits{
					MaxSelectors:         10,
					MaxResolvedFolderIDs: 100,
				},
			)
			require.NoError(t, err)
			harness.service.knowledgeScopeResolver = resolver

			preparation, err := harness.service.PrepareKnowledgeScope(
				prepareScopeContext(prepareScopeCallerTenant),
				types.KnowledgeScopePrepareInput{
					CanonicalRequest: &types.KnowledgeScopeRequest{
						FolderScopes: prepareScopeFolders(
							types.FolderScopeRequest{
								KnowledgeBaseID: prepareScopeKBOne,
								FolderIDs: []string{
									types.KnowledgeFolderRootID,
								},
								IncludeDescendants: prepareScopeBool(
									testCase.includeDescendants,
								),
							},
						),
					},
				},
			)

			require.NoError(t, err)
			require.NotNil(t, preparation)
			assert.Equal(t, 0, harness.authorization.folderCalls)
			assert.Equal(t, 0, folderRepository.calls)
			targets := preparation.Execution().Targets()
			require.Len(t, targets, 1)
			filter := targets[0].FolderFilter()
			assert.Equal(t, testCase.wantEnabled, filter.Enabled())
			assert.Equal(t, testCase.wantFolderIDs, filter.FolderIDs())
			assert.Equal(
				t,
				testCase.wantMaterialCalls,
				harness.knowledge.materializationCallCount(),
			)
			if testCase.wantMaterialCalls == 1 {
				calls := harness.knowledge.materializationCallSnapshot()
				require.Len(t, calls, 1)
				assert.Equal(t, []string{""}, calls[0].folderIDs)
				assert.True(t, calls[0].knowledgeIDsWasNil)
			}
		})
	}
}

func TestPrepareKnowledgeScopeVirtualRootIsScopedPerKnowledgeBase(t *testing.T) {
	harness := newPrepareScopeHarness()
	harness.addKnowledgeBase(prepareScopeKBOne, prepareScopeCallerTenant)
	harness.addKnowledgeBase(prepareScopeKBTwo, prepareScopeCallerTenant)
	folderRepository := &prepareScopeFolderRepositoryStub{}
	resolver, err := NewKnowledgeScopeResolver(
		folderRepository,
		KnowledgeScopeLimits{
			MaxSelectors:         10,
			MaxResolvedFolderIDs: 100,
		},
	)
	require.NoError(t, err)
	harness.service.knowledgeScopeResolver = resolver

	preparation, err := harness.service.PrepareKnowledgeScope(
		prepareScopeContext(prepareScopeCallerTenant),
		types.KnowledgeScopePrepareInput{
			CanonicalRequest: &types.KnowledgeScopeRequest{
				FolderScopes: prepareScopeFolders(
					types.FolderScopeRequest{
						KnowledgeBaseID: prepareScopeKBOne,
						FolderIDs: []string{
							types.KnowledgeFolderRootID,
						},
						IncludeDescendants: prepareScopeBool(false),
					},
					types.FolderScopeRequest{
						KnowledgeBaseID: prepareScopeKBTwo,
						FolderIDs: []string{
							types.KnowledgeFolderRootID,
						},
						IncludeDescendants: prepareScopeBool(false),
					},
				),
			},
		},
	)

	require.NoError(t, err)
	require.NotNil(t, preparation)
	assert.Equal(t, 0, harness.authorization.folderCalls)
	assert.Equal(t, 0, folderRepository.calls)
	assert.Equal(t, 2, harness.knowledge.materializationCallCount())
	require.Len(t, preparation.Execution().Targets(), 2)
}

func TestPrepareKnowledgeScopeMissingFolderEntryIsEnabledEmpty(t *testing.T) {
	harness := newPrepareScopeHarness()
	harness.addKnowledgeBase(prepareScopeKBOne, prepareScopeCallerTenant)
	harness.addKnowledgeBase(prepareScopeKBTwo, prepareScopeCallerTenant)
	harness.addFolder(
		prepareScopeFolderOne,
		prepareScopeKBOne,
		prepareScopeCallerTenant,
	)
	harness.addKnowledge(
		prepareScopeKnowledgeTwo,
		prepareScopeKBTwo,
		prepareScopeCallerTenant,
	)

	preparation, err := harness.service.PrepareKnowledgeScope(
		prepareScopeContext(prepareScopeCallerTenant),
		types.KnowledgeScopePrepareInput{
			CanonicalRequest: &types.KnowledgeScopeRequest{
				KnowledgeBaseIDs: []string{
					prepareScopeKBOne,
					prepareScopeKBTwo,
				},
				KnowledgeIDs: []string{prepareScopeKnowledgeTwo},
				FolderScopes: prepareScopeFolders(types.FolderScopeRequest{
					KnowledgeBaseID: prepareScopeKBOne,
					FolderIDs:       []string{prepareScopeFolderOne},
				}),
			},
		},
	)

	require.NoError(t, err)
	targets := preparation.Execution().Targets()
	require.Len(t, targets, 2)
	targetByKB := make(map[string]types.KnowledgeScopeTarget, len(targets))
	for _, target := range targets {
		targetByKB[target.KnowledgeBaseID()] = target
	}
	assert.True(t, targetByKB[prepareScopeKBOne].FolderFilter().Enabled())
	assert.Equal(
		t,
		[]string{prepareScopeFolderOne},
		targetByKB[prepareScopeKBOne].FolderFilter().FolderIDs(),
	)
	assert.True(t, targetByKB[prepareScopeKBTwo].FolderFilter().Empty())
	assert.Empty(
		t,
		targetByKB[prepareScopeKBTwo].FolderFilter().FolderIDs(),
	)
	assert.Empty(t, targetByKB[prepareScopeKBTwo].KnowledgeIDs())
	calls := harness.knowledge.materializationCallSnapshot()
	require.Len(t, calls, 1)
	assert.Equal(t, prepareScopeKBOne, calls[0].kbID)
}

func TestSearchKnowledgeWithScopeAppliesRuntimeGateBeforeDependencies(
	t *testing.T,
) {
	t.Run("enabled empty", func(t *testing.T) {
		harness := newPrepareScopeHarness()
		harness.addKnowledgeBase(prepareScopeKBOne, prepareScopeCallerTenant)
		empty := []types.FolderScopeRequest{}
		preparation, err := harness.service.PrepareKnowledgeScope(
			prepareScopeContext(prepareScopeCallerTenant),
			types.KnowledgeScopePrepareInput{
				CanonicalRequest: &types.KnowledgeScopeRequest{
					KnowledgeBaseIDs: []string{prepareScopeKBOne},
					FolderScopes:     &empty,
				},
			},
		)
		require.NoError(t, err)
		assert.Equal(t, 0, harness.knowledge.materializationCallCount())
		assert.False(t, preparation.HasEnabledNonEmptyFolderFilter())

		results, err := (&sessionService{}).SearchKnowledgeWithScope(
			prepareScopeContext(prepareScopeCallerTenant),
			"query",
			preparation,
		)

		require.NoError(t, err)
		assert.Empty(t, results)
	})

	t.Run("enabled nonempty", func(t *testing.T) {
		harness := newPrepareScopeHarness()
		harness.addKnowledgeBase(prepareScopeKBOne, prepareScopeCallerTenant)
		harness.addFolder(
			prepareScopeFolderOne,
			prepareScopeKBOne,
			prepareScopeCallerTenant,
		)
		preparation, err := harness.service.PrepareKnowledgeScope(
			prepareScopeContext(prepareScopeCallerTenant),
			types.KnowledgeScopePrepareInput{
				CanonicalRequest: &types.KnowledgeScopeRequest{
					KnowledgeBaseIDs: []string{prepareScopeKBOne},
					FolderScopes: prepareScopeFolders(
						types.FolderScopeRequest{
							KnowledgeBaseID: prepareScopeKBOne,
							FolderIDs: []string{
								prepareScopeFolderOne,
							},
						},
					),
				},
			},
		)
		require.NoError(t, err)
		assert.Equal(t, 1, harness.knowledge.materializationCallCount())
		assert.True(t, preparation.HasEnabledNonEmptyFolderFilter())
		targets := preparation.Execution().Targets()
		require.Len(t, targets, 1)
		assert.Empty(t, targets[0].KnowledgeIDs())

		results, err := (&sessionService{}).SearchKnowledgeWithScope(
			prepareScopeContext(prepareScopeCallerTenant),
			"query",
			preparation,
		)

		assert.Nil(t, results)
		appError := requirePrepareScopeHTTPCode(
			t,
			err,
			http.StatusServiceUnavailable,
		)
		assert.Equal(t, knowledgeScopeUnavailableMessage, appError.Message)
	})
}

func TestMapPreparedKnowledgeRuntimeErrorIsSafeAndPreservesContext(
	t *testing.T,
) {
	privateErr := errors.New(
		"private SQL SELECT source_tenant_id FROM folders",
	)
	mapped := mapPreparedKnowledgeRuntimeError(
		prepareScopeContext(prepareScopeCallerTenant),
		privateErr,
	)
	appError := requirePrepareScopeHTTPCode(
		t,
		mapped,
		http.StatusInternalServerError,
	)
	assert.Equal(t, "knowledge search failed", appError.Message)
	assert.NotContains(t, appError.Message, "SELECT")
	assert.NotContains(t, appError.Message, "source_tenant_id")

	assert.ErrorIs(
		t,
		mapPreparedKnowledgeRuntimeError(
			prepareScopeContext(prepareScopeCallerTenant),
			context.Canceled,
		),
		context.Canceled,
	)
	assert.ErrorIs(
		t,
		mapPreparedKnowledgeRuntimeError(
			prepareScopeContext(prepareScopeCallerTenant),
			context.DeadlineExceeded,
		),
		context.DeadlineExceeded,
	)
}

func TestPrepareKnowledgeScopePreparationOwnsDeepCopies(t *testing.T) {
	harness := newPrepareScopeHarness()
	harness.addKnowledgeBase(prepareScopeKBOne, prepareScopeCallerTenant)
	harness.addFolder(
		prepareScopeFolderOne,
		prepareScopeKBOne,
		prepareScopeCallerTenant,
	)
	harness.authorization.knowledges[prepareScopeKnowledgeOne] = &types.Knowledge{
		ID:              prepareScopeKnowledgeOne,
		TenantID:        prepareScopeCallerTenant,
		KnowledgeBaseID: prepareScopeKBOne,
	}
	harness.knowledge.setMaterializationPage(
		prepareScopeCallerTenant,
		prepareScopeKBOne,
		"",
		prepareScopeMaterializationPage{
			ids: []string{prepareScopeKnowledgeOne},
		},
	)
	includeDescendants := false
	canonical := &types.KnowledgeScopeRequest{
		KnowledgeBaseIDs: []string{prepareScopeKBOne},
		KnowledgeIDs:     []string{prepareScopeKnowledgeOne},
		FolderScopes: prepareScopeFolders(types.FolderScopeRequest{
			KnowledgeBaseID:    prepareScopeKBOne,
			FolderIDs:          []string{prepareScopeFolderOne},
			IncludeDescendants: &includeDescendants,
		}),
	}

	preparation, err := harness.service.PrepareKnowledgeScope(
		prepareScopeContext(prepareScopeCallerTenant),
		types.KnowledgeScopePrepareInput{CanonicalRequest: canonical},
	)
	require.NoError(t, err)

	canonical.KnowledgeBaseIDs[0] = "mutated-kb"
	canonical.KnowledgeIDs[0] = "mutated-knowledge"
	(*canonical.FolderScopes)[0].FolderIDs[0] = "mutated-folder"
	includeDescendants = true

	firstRequest := preparation.Request()
	assert.Equal(t, prepareScopeKBOne, firstRequest.KnowledgeBaseIDs[0])
	assert.Equal(t, prepareScopeKnowledgeOne, firstRequest.KnowledgeIDs[0])
	assert.Equal(t, prepareScopeFolderOne, (*firstRequest.FolderScopes)[0].FolderIDs[0])
	assert.False(t, *(*firstRequest.FolderScopes)[0].IncludeDescendants)

	firstRequest.KnowledgeIDs[0] = "getter-mutation"
	(*firstRequest.FolderScopes)[0].FolderIDs[0] = "getter-folder-mutation"
	*(*firstRequest.FolderScopes)[0].IncludeDescendants = true
	secondRequest := preparation.Request()
	assert.Equal(t, prepareScopeKnowledgeOne, secondRequest.KnowledgeIDs[0])
	assert.Equal(t, prepareScopeFolderOne, (*secondRequest.FolderScopes)[0].FolderIDs[0])
	assert.False(t, *(*secondRequest.FolderScopes)[0].IncludeDescendants)

	firstExecution := preparation.Execution()
	firstTargets := firstExecution.Targets()
	require.Len(t, firstTargets, 1)
	knowledgeIDs := firstTargets[0].KnowledgeIDs()
	require.Len(t, knowledgeIDs, 1)
	knowledgeIDs[0] = "execution-getter-mutation"
	folderIDs := firstTargets[0].FolderFilter().FolderIDs()
	require.Len(t, folderIDs, 1)
	folderIDs[0] = "execution-folder-mutation"
	secondTargets := preparation.Execution().Targets()
	assert.Equal(t, prepareScopeKnowledgeOne, secondTargets[0].KnowledgeIDs()[0])
	assert.Equal(t, prepareScopeFolderOne, secondTargets[0].FolderFilter().FolderIDs()[0])
	assert.NotEmpty(t, preparation.ExecutionScopeHash())
}

func TestPrepareKnowledgeScopeRepeatedPreparationReauthorizesAndResolves(t *testing.T) {
	harness := newPrepareScopeHarness()
	harness.addKnowledgeBase(prepareScopeKBOne, prepareScopeSourceTenant)
	harness.shares.allowed[prepareScopeKBOne] = true
	ctx := prepareScopeContext(prepareScopeCallerTenant)

	first, err := harness.service.PrepareKnowledgeScope(
		ctx,
		types.KnowledgeScopePrepareInput{
			CanonicalRequest: &types.KnowledgeScopeRequest{
				KnowledgeBaseIDs: []string{prepareScopeKBOne},
			},
		},
	)
	require.NoError(t, err)

	persistedRequest := first.Request()
	second, err := harness.service.PrepareKnowledgeScope(
		ctx,
		types.KnowledgeScopePrepareInput{
			CanonicalRequest: persistedRequest,
		},
	)

	require.NoError(t, err)
	require.NotNil(t, second)
	assert.Equal(t, 2, harness.knowledgeBases.calls)
	assert.Len(t, harness.shares.calls, 2)
	assert.Equal(t, 2, harness.resolver.calls)
	assert.Equal(t, 0, harness.knowledge.materializationCallCount())
	assert.Equal(t, first.ExecutionScopeHash(), second.ExecutionScopeHash())
}

func TestPrepareKnowledgeScopeMapsFolderResolverErrors(t *testing.T) {
	testCases := []struct {
		name         string
		resolverErr  error
		expectedHTTP int
	}{
		{
			name:         "folder not found",
			resolverErr:  ErrKnowledgeFolderNotFound,
			expectedHTTP: http.StatusNotFound,
		},
		{
			name:         "folder data integrity",
			resolverErr:  ErrKnowledgeFolderDataIntegrity,
			expectedHTTP: http.StatusInternalServerError,
		},
		{
			name:         "unsupported database",
			resolverErr:  ErrKnowledgeFolderUnsupportedDB,
			expectedHTTP: http.StatusInternalServerError,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			harness := newPrepareScopeHarness()
			harness.addKnowledgeBase(prepareScopeKBOne, prepareScopeCallerTenant)
			harness.addFolder(
				prepareScopeFolderOne,
				prepareScopeKBOne,
				prepareScopeCallerTenant,
			)
			harness.resolver.err = testCase.resolverErr

			preparation, err := harness.service.PrepareKnowledgeScope(
				prepareScopeContext(prepareScopeCallerTenant),
				types.KnowledgeScopePrepareInput{
					CanonicalRequest: &types.KnowledgeScopeRequest{
						FolderScopes: prepareScopeFolders(types.FolderScopeRequest{
							KnowledgeBaseID: prepareScopeKBOne,
							FolderIDs:       []string{prepareScopeFolderOne},
						}),
					},
				},
			)

			require.Nil(t, preparation)
			requirePrepareScopeHTTPCode(t, err, testCase.expectedHTTP)
			assert.Equal(t, 1, harness.resolver.calls)
		})
	}
}

func TestProjectKnowledgeQARuntimePopulatesPipelineRequest(t *testing.T) {
	filter, err := types.NewResolvedFolderFilter(false, nil)
	require.NoError(t, err)
	target, err := types.NewKnowledgeScopeTarget(
		prepareScopeKBOne,
		prepareScopeSourceTenant,
		[]string{prepareScopeKnowledgeOne},
		[]string{"physical-tag"},
		[]string{prepareScopeTagOne},
		filter,
	)
	require.NoError(t, err)
	scope, err := types.NewKnowledgeScope(
		[]types.KnowledgeScopeTarget{target},
	)
	require.NoError(t, err)

	projection, err := projectKnowledgeQARuntime(
		nil,
		scope,
		"prepared-execution-hash",
	)
	require.NoError(t, err)
	assert.True(t, projection.prepared)
	assert.True(t, projection.hasLocalKnowledge)
	assert.Equal(
		t,
		[]string{prepareScopeKBOne},
		projection.knowledgeBaseIDs,
	)
	assert.Equal(
		t,
		[]string{prepareScopeKnowledgeOne},
		projection.knowledgeIDs,
	)
	require.Len(t, projection.searchTargets, 1)

	pipelineRequest := types.PipelineRequest{}
	applyKnowledgeQARuntimeProjection(
		&pipelineRequest,
		projection.knowledgeBaseIDs,
		projection.knowledgeIDs,
		projection.searchTargets,
		scope,
		"prepared-execution-hash",
		projection.retrievalExplicitlyEmpty,
	)

	assert.Equal(
		t,
		[]string{prepareScopeKBOne},
		pipelineRequest.KnowledgeBaseIDs,
	)
	assert.Equal(
		t,
		[]string{prepareScopeKnowledgeOne},
		pipelineRequest.KnowledgeIDs,
	)
	assert.Equal(
		t,
		"prepared-execution-hash",
		pipelineRequest.ExecutionScopeHash,
	)
	require.NotNil(t, pipelineRequest.ExecutionScope)
	assert.NotSame(t, scope, pipelineRequest.ExecutionScope)
	require.Len(t, pipelineRequest.SearchTargets, 1)
	projectedTarget := pipelineRequest.SearchTargets[0]
	assert.Equal(t, prepareScopeKBOne, projectedTarget.KnowledgeBaseID)
	assert.Equal(
		t,
		prepareScopeSourceTenant,
		projectedTarget.SourceTenantID,
	)
	assert.Equal(
		t,
		[]string{prepareScopeKnowledgeOne},
		projectedTarget.KnowledgeIDs,
	)
	assert.Equal(t, []string{"physical-tag"}, projectedTarget.TagIDs)
	assert.Equal(
		t,
		[]string{prepareScopeTagOne},
		projectedTarget.ScopeTagIDs,
	)
	assert.False(t, projectedTarget.FolderFilter.Enabled())
	assert.Equal(
		t,
		"prepared-execution-hash",
		projectedTarget.ExecutionScopeHash,
	)

	pipelineRequest.KnowledgeBaseIDs[0] = "mutated-kb"
	pipelineRequest.KnowledgeIDs[0] = "mutated-knowledge"
	projectedTarget.KnowledgeIDs[0] = "mutated-target-knowledge"
	projectedTarget.TagIDs[0] = "mutated-physical-tag"
	projectedTarget.ScopeTagIDs[0] = "mutated-scope-tag"
	assert.Equal(
		t,
		[]string{prepareScopeKBOne},
		projection.knowledgeBaseIDs,
	)
	assert.Equal(
		t,
		[]string{prepareScopeKnowledgeOne},
		projection.knowledgeIDs,
	)
	assert.Equal(
		t,
		[]string{prepareScopeKnowledgeOne},
		projection.searchTargets[0].KnowledgeIDs,
	)
	assert.Equal(
		t,
		[]string{"physical-tag"},
		projection.searchTargets[0].TagIDs,
	)
	assert.Equal(
		t,
		[]string{prepareScopeTagOne},
		projection.searchTargets[0].ScopeTagIDs,
	)
}

func TestProjectKnowledgeQARuntimeEnabledEmptyUsesPureLLMPipeline(t *testing.T) {
	filter, err := types.NewResolvedFolderFilter(true, nil)
	require.NoError(t, err)
	target, err := types.NewKnowledgeScopeTarget(
		prepareScopeKBOne,
		prepareScopeSourceTenant,
		nil,
		nil,
		nil,
		filter,
	)
	require.NoError(t, err)
	scope, err := types.NewKnowledgeScope(
		[]types.KnowledgeScopeTarget{target},
	)
	require.NoError(t, err)

	projection, err := projectKnowledgeQARuntime(
		&types.KnowledgeScopeRequest{
			FolderScopes: prepareScopeFolders(),
		},
		scope,
		"enabled-empty-hash",
	)
	require.NoError(t, err)
	assert.True(t, projection.prepared)
	assert.False(t, projection.hasLocalKnowledge)
	assert.Empty(t, projection.knowledgeBaseIDs)
	assert.Empty(t, projection.knowledgeIDs)
	assert.Empty(t, projection.searchTargets)

	pipeline := buildKnowledgeQAPipeline(
		projection.hasLocalKnowledge,
		true,
		projection.retrievalExplicitlyEmpty,
		false,
		false,
	)
	assert.Equal(
		t,
		[]types.EventType{types.CHAT_COMPLETION_STREAM},
		pipeline,
	)
	assert.NotContains(t, pipeline, types.QUERY_UNDERSTAND)
	assert.NotContains(t, pipeline, types.CHUNK_SEARCH_PARALLEL)
	assert.NotContains(t, pipeline, types.CHUNK_RERANK)
	assert.NotContains(t, pipeline, types.CHUNK_MERGE)
	assert.NotContains(t, pipeline, types.FILTER_TOP_K)
}

func TestProjectKnowledgeQARuntimeMaterializedZeroUsesPureLLMPipeline(
	t *testing.T,
) {
	filter, err := types.NewResolvedFolderFilter(
		true,
		[]string{prepareScopeFolderOne},
	)
	require.NoError(t, err)
	target, err := types.NewKnowledgeScopeTarget(
		prepareScopeKBOne,
		prepareScopeSourceTenant,
		nil,
		nil,
		nil,
		filter,
	)
	require.NoError(t, err)
	scope, err := types.NewKnowledgeScope(
		[]types.KnowledgeScopeTarget{target},
	)
	require.NoError(t, err)

	projection, err := projectKnowledgeQARuntime(
		&types.KnowledgeScopeRequest{
			FolderScopes: prepareScopeFolders(),
		},
		scope,
		"materialized-zero-hash",
	)

	require.NoError(t, err)
	assert.True(t, projection.prepared)
	assert.False(t, projection.hasLocalKnowledge)
	assert.True(t, projection.retrievalExplicitlyEmpty)
	assert.Empty(t, projection.knowledgeBaseIDs)
	assert.Empty(t, projection.knowledgeIDs)
	assert.Empty(t, projection.searchTargets)
	assert.Equal(
		t,
		[]types.EventType{types.CHAT_COMPLETION_STREAM},
		buildKnowledgeQAPipeline(
			projection.hasLocalKnowledge,
			true,
			projection.retrievalExplicitlyEmpty,
			false,
			false,
		),
	)
}

func TestProjectKnowledgeQARuntimeMixedMaterializedZeroKeepsOnlyNonzeroTarget(
	t *testing.T,
) {
	filterOne, err := types.NewResolvedFolderFilter(
		true,
		[]string{prepareScopeFolderOne},
	)
	require.NoError(t, err)
	filterTwo, err := types.NewResolvedFolderFilter(
		true,
		[]string{prepareScopeFolderTwo},
	)
	require.NoError(t, err)
	zeroTarget, err := types.NewKnowledgeScopeTarget(
		prepareScopeKBOne,
		prepareScopeSourceTenant,
		nil,
		nil,
		nil,
		filterOne,
	)
	require.NoError(t, err)
	nonzeroTarget, err := types.NewKnowledgeScopeTarget(
		prepareScopeKBTwo,
		prepareScopeSourceTenant,
		[]string{"knowledge-two"},
		nil,
		nil,
		filterTwo,
	)
	require.NoError(t, err)
	scope, err := types.NewKnowledgeScope(
		[]types.KnowledgeScopeTarget{zeroTarget, nonzeroTarget},
	)
	require.NoError(t, err)
	folderScopes := []types.FolderScopeRequest{
		{
			KnowledgeBaseID: prepareScopeKBOne,
			FolderIDs:       []string{prepareScopeFolderOne},
		},
		{
			KnowledgeBaseID: prepareScopeKBTwo,
			FolderIDs:       []string{prepareScopeFolderTwo},
		},
	}

	projection, err := projectKnowledgeQARuntime(
		&types.KnowledgeScopeRequest{FolderScopes: &folderScopes},
		scope,
		"mixed-folder-hash",
	)

	require.NoError(t, err)
	assert.True(t, projection.prepared)
	assert.True(t, projection.hasLocalKnowledge)
	assert.False(t, projection.retrievalExplicitlyEmpty)
	assert.Equal(t, []string{prepareScopeKBTwo}, projection.knowledgeBaseIDs)
	assert.Equal(t, []string{"knowledge-two"}, projection.knowledgeIDs)
	require.Len(t, projection.searchTargets, 1)
	assert.Equal(
		t,
		prepareScopeKBTwo,
		projection.searchTargets[0].KnowledgeBaseID,
	)
	assert.Equal(
		t,
		[]string{"knowledge-two"},
		projection.searchTargets[0].KnowledgeIDs,
	)
}

func TestProjectKnowledgeQARuntimeExplicitEmptyWithoutTargetsSkipsWeb(
	t *testing.T,
) {
	requestScope := &types.KnowledgeScopeRequest{
		FolderScopes: prepareScopeFolders(),
	}
	scope, err := types.NewKnowledgeScope(nil)
	require.NoError(t, err)

	projection, err := projectKnowledgeQARuntime(
		requestScope,
		scope,
		"enabled-empty-hash",
	)
	require.NoError(t, err)
	assert.True(t, projection.prepared)
	assert.True(t, projection.retrievalExplicitlyEmpty)
	assert.False(t, projection.hasLocalKnowledge)
	assert.Empty(t, projection.searchTargets)

	pipeline := buildKnowledgeQAPipeline(
		projection.hasLocalKnowledge,
		true,
		projection.retrievalExplicitlyEmpty,
		false,
		false,
	)
	assert.Equal(
		t,
		[]types.EventType{types.CHAT_COMPLETION_STREAM},
		pipeline,
	)

	pipelineRequest := types.PipelineRequest{}
	applyKnowledgeQARuntimeProjection(
		&pipelineRequest,
		nil,
		nil,
		nil,
		scope,
		"enabled-empty-hash",
		projection.retrievalExplicitlyEmpty,
	)
	assert.True(t, pipelineRequest.RetrievalExplicitlyEmpty)
}

func TestProjectKnowledgeQARuntimeNilScopeKeepsLegacyBranch(t *testing.T) {
	projection, err := projectKnowledgeQARuntime(nil, nil, "")
	require.NoError(t, err)
	assert.False(t, projection.prepared)
	assert.False(t, projection.hasLocalKnowledge)
	assert.Empty(t, projection.knowledgeBaseIDs)
	assert.Empty(t, projection.knowledgeIDs)
	assert.Empty(t, projection.searchTargets)

	legacyTargets := types.SearchTargets{{
		Type:            types.SearchTargetTypeKnowledgeBase,
		KnowledgeBaseID: prepareScopeKBOne,
		TenantID:        prepareScopeCallerTenant,
	}}
	pipelineRequest := types.PipelineRequest{}
	applyKnowledgeQARuntimeProjection(
		&pipelineRequest,
		[]string{prepareScopeKBOne},
		nil,
		legacyTargets,
		nil,
		"",
		false,
	)
	assert.Equal(
		t,
		[]string{prepareScopeKBOne},
		pipelineRequest.KnowledgeBaseIDs,
	)
	require.Len(t, pipelineRequest.SearchTargets, 1)
	assert.Nil(t, pipelineRequest.ExecutionScope)
	assert.Empty(t, pipelineRequest.ExecutionScopeHash)

	hasLegacyKnowledge := types.HasKnowledgeRetrievalScope(
		pipelineRequest.SearchTargets,
		pipelineRequest.KnowledgeBaseIDs,
		pipelineRequest.KnowledgeIDs,
	)
	pipeline := buildKnowledgeQAPipeline(
		hasLegacyKnowledge,
		false,
		false,
		false,
		false,
	)
	assert.Contains(t, pipeline, types.QUERY_UNDERSTAND)
	assert.Contains(t, pipeline, types.CHUNK_SEARCH_PARALLEL)
	assert.Contains(t, pipeline, types.CHUNK_RERANK)
	assert.Contains(t, pipeline, types.CHUNK_MERGE)
}

func TestProjectKnowledgeQARuntimeRejectsPartialExecutionState(t *testing.T) {
	scope, err := types.NewKnowledgeScope(nil)
	require.NoError(t, err)

	projection, err := projectKnowledgeQARuntime(nil, scope, "")
	assert.False(t, projection.prepared)
	requirePrepareScopeHTTPCode(t, err, http.StatusBadRequest)

	projection, err = projectKnowledgeQARuntime(nil, nil, "orphan-hash")
	assert.False(t, projection.prepared)
	requirePrepareScopeHTTPCode(t, err, http.StatusBadRequest)
}

func TestDetachedTitleGenerationContextPreservesValuesWithoutCancellation(
	t *testing.T,
) {
	parent := logger.WithFields(
		context.WithValue(
			context.Background(),
			types.RequestIDContextKey,
			"request-1",
		),
		logger.Fields{"knowledge_scope_prepared": true},
	)
	parent, cancel := context.WithCancel(parent)
	detached := detachedTitleGenerationContext(parent)
	cancel()

	assert.Equal(t, "request-1", detached.Value(types.RequestIDContextKey))
	assert.NoError(t, detached.Err())
	assert.Equal(
		t,
		true,
		logger.GetLogger(detached).Data["knowledge_scope_prepared"],
	)
}
