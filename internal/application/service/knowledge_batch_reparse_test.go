package service

import (
	"context"
	"errors"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/hibiken/asynq"
	"github.com/stretchr/testify/require"
)

type reparseFailureKnowledgeRepo struct {
	interfaces.KnowledgeRepository
	knowledge   *types.Knowledge
	updateCalls int
}

func (r *reparseFailureKnowledgeRepo) GetKnowledgeByID(
	_ context.Context,
	_ uint64,
	_ string,
) (*types.Knowledge, error) {
	return r.knowledge, nil
}

func (r *reparseFailureKnowledgeRepo) UpdateKnowledge(
	_ context.Context,
	_ *types.Knowledge,
) error {
	r.updateCalls++
	return nil
}

func (r *reparseFailureKnowledgeRepo) UpdateKnowledgeColumn(
	_ context.Context,
	_ string,
	_ string,
	_ interface{},
) error {
	return nil
}

type reparseFailureKBService struct {
	interfaces.KnowledgeBaseService
	kb *types.KnowledgeBase
}

type parserRulesKnowledgeRepo struct {
	interfaces.KnowledgeRepository
	knowledge       *types.Knowledge
	getErr          error
	updateErr       error
	requestedTenant uint64
	updateCalls     int
	updatedID       string
	updatedColumn   string
	updatedValue    interface{}
}

func (r *parserRulesKnowledgeRepo) GetKnowledgeByID(
	_ context.Context,
	tenantID uint64,
	_ string,
) (*types.Knowledge, error) {
	r.requestedTenant = tenantID
	return r.knowledge, r.getErr
}

func (r *parserRulesKnowledgeRepo) UpdateKnowledgeColumn(
	_ context.Context,
	id string,
	column string,
	value interface{},
) error {
	r.updateCalls++
	r.updatedID = id
	r.updatedColumn = column
	r.updatedValue = value
	return r.updateErr
}

func (s *reparseFailureKBService) GetKnowledgeBaseByID(
	_ context.Context,
	_ string,
) (*types.KnowledgeBase, error) {
	return s.kb, nil
}

type failingReparseTaskEnqueuer struct {
	err error
}

func (e failingReparseTaskEnqueuer) Enqueue(
	_ *asynq.Task,
	_ ...asynq.Option,
) (*asynq.TaskInfo, error) {
	return nil, e.err
}

func TestReparseKnowledgeManualEnqueueFailureIsVisible(t *testing.T) {
	enqueueErr := errors.New("queue unavailable")
	knowledge := &types.Knowledge{
		ID:              "knowledge-1",
		TenantID:        7,
		KnowledgeBaseID: "kb-1",
		Type:            types.KnowledgeTypeManual,
		ParseStatus:     types.ParseStatusCompleted,
		EnableStatus:    "enabled",
	}
	require.NoError(t, knowledge.SetManualMetadata(
		types.NewManualKnowledgeMetadata("# content", types.ManualKnowledgeStatusPublish, 1),
	))
	repo := &reparseFailureKnowledgeRepo{knowledge: knowledge}
	svc := &knowledgeService{
		repo:      repo,
		kbService: &reparseFailureKBService{kb: &types.KnowledgeBase{ID: "kb-1"}},
		task:      failingReparseTaskEnqueuer{err: enqueueErr},
	}
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(7))

	got, err := svc.ReparseKnowledge(ctx, knowledge.ID, nil)

	require.Error(t, err)
	require.Same(t, knowledge, got)
	require.Equal(t, types.ParseStatusFailed, knowledge.ParseStatus)
	require.Equal(t, "disabled", knowledge.EnableStatus)
	require.Equal(t, "Failed to enqueue processing task", knowledge.ErrorMessage)
	require.GreaterOrEqual(t, repo.updateCalls, 2, "pending and failed states must both be persisted")
}

func TestRunKnowledgeListReparseSubmissionsReportsPartialFailure(t *testing.T) {
	firstErr := errors.New("first failed")
	secondErr := errors.New("second failed")
	var attempted []string

	outcome, err := runKnowledgeListReparseSubmissions(
		[]string{"ok-1", "bad-1", "ok-2", "bad-2"},
		func(id string) error {
			attempted = append(attempted, id)
			switch id {
			case "bad-1":
				return firstErr
			case "bad-2":
				return secondErr
			default:
				return nil
			}
		},
	)

	require.Equal(t, []string{"ok-1", "bad-1", "ok-2", "bad-2"}, attempted)
	require.Equal(t, knowledgeListReparseOutcome{Submitted: 2, Failed: 2}, outcome)
	require.ErrorIs(t, err, asynq.SkipRetry)
	require.ErrorIs(t, err, firstErr)
	require.ErrorIs(t, err, secondErr)
	require.ErrorContains(t, err, "knowledge bad-1")
	require.ErrorContains(t, err, "knowledge bad-2")
}

func TestRunKnowledgeListReparseSubmissionsSucceeds(t *testing.T) {
	outcome, err := runKnowledgeListReparseSubmissions(
		[]string{"knowledge-1", "knowledge-2"},
		func(string) error { return nil },
	)

	require.NoError(t, err)
	require.Equal(t, knowledgeListReparseOutcome{Submitted: 2}, outcome)
}

func TestClearStoredParserEngineRulesUsesCurrentKnowledgeBaseRules(t *testing.T) {
	enableMultimodel := true
	knowledge := &types.Knowledge{
		ID:       "knowledge-1",
		TenantID: 7,
		Metadata: types.JSON(`{
			"source_id":"keep-me",
			"process_overrides":{
				"parser_engine_rules":[{"file_types":["pdf"],"engine":"old-top-level"}],
				"chunking_config":{
					"chunk_size":1024,
					"chunk_overlap":128,
					"enable_parent_child":true,
					"parser_engine_rules":[{"file_types":["docx"],"engine":"old-nested"}]
				},
				"enable_multimodel":true,
				"parser_engine_overrides":{"pdf_force_scanned":"true"}
			}
		}`),
	}
	repo := &parserRulesKnowledgeRepo{knowledge: knowledge}
	svc := &knowledgeService{repo: repo}
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(7))

	err := svc.clearStoredParserEngineRules(ctx, knowledge.ID)

	require.NoError(t, err)
	require.Equal(t, uint64(7), repo.requestedTenant)
	require.Equal(t, 1, repo.updateCalls)
	require.Equal(t, knowledge.ID, repo.updatedID)
	require.Equal(t, "metadata", repo.updatedColumn)
	require.Equal(t, knowledge.Metadata, repo.updatedValue)

	overrides, err := knowledge.ProcessOverrides()
	require.NoError(t, err)
	require.NotNil(t, overrides)
	require.Empty(t, overrides.ParserEngineRules)
	require.NotNil(t, overrides.ChunkingConfig)
	require.Empty(t, overrides.ChunkingConfig.ParserEngineRules)
	require.Equal(t, 1024, overrides.ChunkingConfig.ChunkSize)
	require.Equal(t, 128, overrides.ChunkingConfig.ChunkOverlap)
	require.True(t, overrides.ChunkingConfig.EnableParentChild)
	require.Equal(t, &enableMultimodel, overrides.EnableMultimodel)
	require.Equal(t, map[string]string{"pdf_force_scanned": "true"}, overrides.ParserEngineOverrides)

	metadata, err := knowledge.Metadata.Map()
	require.NoError(t, err)
	require.Equal(t, "keep-me", metadata["source_id"])

	currentRules := []types.ParserEngineRule{{FileTypes: []string{"pdf", "docx"}, Engine: "current"}}
	effective := ResolveProcessConfig(&types.KnowledgeBase{
		ChunkingConfig: types.ChunkingConfig{ParserEngineRules: currentRules},
	}, overrides)
	require.Equal(t, currentRules, effective.ChunkingConfig.ParserEngineRules)
}

func TestClearStoredParserEngineRulesWithoutSnapshotsIsNoOp(t *testing.T) {
	knowledge := &types.Knowledge{
		ID:       "knowledge-2",
		TenantID: 7,
		Metadata: types.JSON(`{
			"source_id":"keep-me",
			"process_overrides":{
				"chunking_config":{"chunk_size":2048},
				"parser_engine_overrides":{"xlsx_first_row_as_header":"true"}
			}
		}`),
	}
	originalMetadata := append(types.JSON(nil), knowledge.Metadata...)
	repo := &parserRulesKnowledgeRepo{knowledge: knowledge}
	svc := &knowledgeService{repo: repo}
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(7))

	err := svc.clearStoredParserEngineRules(ctx, knowledge.ID)

	require.NoError(t, err)
	require.Zero(t, repo.updateCalls)
	require.Equal(t, originalMetadata, knowledge.Metadata)
}

func TestClearStoredParserEngineRulesPropagatesUpdateFailure(t *testing.T) {
	updateErr := errors.New("metadata update failed")
	knowledge := &types.Knowledge{
		ID:       "knowledge-3",
		TenantID: 7,
		Metadata: types.JSON(`{
			"process_overrides":{
				"parser_engine_rules":[{"file_types":["pdf"],"engine":"old"}]
			}
		}`),
	}
	repo := &parserRulesKnowledgeRepo{knowledge: knowledge, updateErr: updateErr}
	svc := &knowledgeService{repo: repo}
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(7))

	err := svc.clearStoredParserEngineRules(ctx, knowledge.ID)

	require.ErrorIs(t, err, updateErr)
	require.Equal(t, 1, repo.updateCalls)
}
