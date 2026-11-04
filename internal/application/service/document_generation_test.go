package service

import (
	"context"
	"errors"
	"testing"

	"github.com/Tencent/WeKnora/internal/application/access"
	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/hibiken/asynq"
	"github.com/stretchr/testify/require"
)

type documentGenerationSpanProbe struct {
	repository.KnowledgeSpanRepository
	legacyCalls int
}

func (r *documentGenerationSpanProbe) NextAttempt(context.Context, string) (int, error) {
	r.legacyCalls++
	return 0, errors.New("unfenced attempt allocation")
}

func (r *documentGenerationSpanProbe) OpenDocumentGenerationAttempt(
	context.Context, *types.KnowledgeProcessingSpan,
) (int, error) {
	return 0, types.ErrDocumentGenerationSuperseded
}

func TestDocumentGenerationSpanOpeningUsesAtomicFence(t *testing.T) {
	r := &documentGenerationSpanProbe{}
	tracker := NewSpanTracker(r, nil)
	ctx := types.WithDocumentGeneration(context.Background(), 1, "k", "stale-A")
	root, _, err := tracker.OpenAttempt(ctx, "k", "")
	require.ErrorIs(t, err, types.ErrDocumentGenerationSuperseded)
	require.Nil(t, root)
	require.Zero(t, r.legacyCalls)
}

type documentGenerationFake struct {
	documentTestScheduler
	current         string
	initial         string
	updates         int
	begins          int
	checkErr        error
	initialFailures int
}

type documentGenerationTransferProbe struct {
	interfaces.KnowledgeRepository
	tokens []string
}

func (r *documentGenerationTransferProbe) UpdateKnowledgeForTransfer(
	ctx context.Context, before, after *types.Knowledge,
) error {
	if token, ok := types.DocumentGenerationFromContext(ctx, after.TenantID, after.ID); ok {
		r.tokens = append(r.tokens, token)
		// This fixture exercises service propagation; repository generation CAS
		// is covered separately against both SQLite and PostgreSQL.
		ctx = types.WithDocumentGeneration(ctx, after.TenantID, after.ID, "")
	}
	return r.KnowledgeRepository.UpdateKnowledgeForTransfer(ctx, before, after)
}

func TestDocumentGenerationMovePersistsAndReusesToken(t *testing.T) {
	f := transferFixture(t, access.KBTransferMove)
	row, err := f.repo.GetKnowledgeByID(f.ctx, 7, "doc")
	require.NoError(t, err)
	row.Type = "file"
	row.FilePath = "source.pdf"
	row.FileName = "source.pdf"
	require.NoError(t, f.repo.UpdateKnowledge(f.ctx, row))
	r := &documentGenerationFake{}
	probe := &documentGenerationTransferProbe{KnowledgeRepository: f.svc.repo}
	f.svc.repo = probe
	f.svc.taskPendingRepo = r
	f.svc.task = &documentTestQueue{}
	require.NoError(t, f.svc.ProcessKnowledgeMove(context.Background(), moveTask(t, []string{"doc"}, "reparse")))
	require.Equal(t, 1, r.begins)
	require.NotEmpty(t, probe.tokens)
	for _, token := range probe.tokens {
		require.Equal(t, "new", token)
	}
	row, err = f.repo.GetKnowledgeByID(f.ctx, 7, "doc")
	require.NoError(t, err)
	state, err := transferState(row)
	require.NoError(t, err)
	require.Equal(t, "new", state.DocumentGeneration)
	require.NoError(t, f.svc.enqueueMovedKnowledge(f.ctx, row, f.kbs.values["kb"], f.kbs.values["other"]))
	require.Equal(t, 1, r.begins, "retry must reuse the persisted generation and budget")
}

func (r *documentGenerationFake) CheckDocumentGeneration(_ context.Context, _ uint64, _, token string) (bool, error) {
	return token == r.current, r.checkErr
}

func (r *documentGenerationFake) BeginDocumentGeneration(
	context.Context, uint64, string, string, string,
) (string, error) {
	r.begins++
	r.current = "new"
	return r.current, nil
}

func (r *documentGenerationFake) InitialDocumentGeneration(context.Context, uint64, string, string) (string, error) {
	if r.initialFailures > 0 {
		r.initialFailures--
		return "", errors.New("initial token unavailable")
	}
	return r.initial, nil
}

func (r *documentGenerationFake) EnqueueDocumentGeneration(
	ctx context.Context, op *types.TaskPendingOp, token string,
) (bool, error) {
	if token != r.current {
		return false, nil
	}
	return r.EnqueueDocument(ctx, op)
}

func (r *documentGenerationFake) UpdateDocumentGeneration(
	_ context.Context, _ uint64, _, token string, _ map[string]any,
) (bool, error) {
	if token != r.current {
		return false, nil
	}
	r.updates++
	return true, nil
}

func (r *documentGenerationFake) SaveDocumentGeneration(
	_ context.Context, token string, _ *types.Knowledge,
) (bool, error) {
	if token != r.current {
		return false, nil
	}
	r.updates++
	return true, nil
}

func (r *documentGenerationFake) FailLegacyDocumentGeneration(
	context.Context, *types.DocumentProcessPayload, string,
) (bool, error) {
	return false, nil
}

func TestDocumentGenerationNestedBeginAndStaleFailure(t *testing.T) {
	r := &documentGenerationFake{}
	s := &knowledgeService{taskPendingRepo: r}
	k := &types.Knowledge{ID: "k", TenantID: 1, KnowledgeBaseID: "kb"}
	ctx, err := s.beginDocumentGeneration(context.Background(), k, "kb")
	require.NoError(t, err)
	ctx, err = s.beginDocumentGeneration(ctx, k, "kb")
	require.NoError(t, err)
	require.Equal(t, 1, r.begins)
	r.current = "newer"
	s.markKnowledgeEnqueueFailed(ctx, k)
	require.Zero(t, r.updates)
	require.ErrorIs(t, s.updateDocumentGenerationColumns(ctx, 1, "k", map[string]any{"parse_status": "pending"}),
		types.ErrDocumentGenerationSuperseded)
}

func TestDocumentGenerationInitialProducerKeepsOriginalToken(t *testing.T) {
	r := &documentGenerationFake{initial: "initial", current: "initial"}
	s := &knowledgeService{taskPendingRepo: r}
	k := &types.Knowledge{ID: "k", TenantID: 1, KnowledgeBaseID: "kb"}
	ctx, err := s.bindInitialDocumentGeneration(context.Background(), k)
	require.NoError(t, err)
	r.current = "replacement"
	require.ErrorIs(t, s.saveDocumentGeneration(ctx, k), types.ErrDocumentGenerationSuperseded)
	s.markKnowledgeEnqueueFailed(ctx, k)
	require.Zero(t, r.updates)
	r.initial = ""
	_, err = s.bindInitialDocumentGeneration(context.Background(), k)
	require.ErrorIs(t, err, types.ErrDocumentGenerationSuperseded)
}

func TestDocumentGenerationInitialBindingFailureSettlesOnlyInitialOwner(t *testing.T) {
	for _, superseded := range []bool{false, true} {
		r := &documentGenerationFake{current: "initial", initial: "initial", initialFailures: 1}
		if superseded {
			r.current, r.initial = "newer", ""
		}
		s := &knowledgeService{taskPendingRepo: r}
		_, err := s.bindInitialDocumentGeneration(context.Background(),
			&types.Knowledge{ID: "k", TenantID: 1, KnowledgeBaseID: "kb"})
		require.ErrorContains(t, err, "initial token unavailable")
		if superseded {
			require.Zero(t, r.updates)
		} else {
			require.Equal(t, 1, r.updates)
		}
	}
}

func TestDocumentGenerationStaleEnqueueAcknowledged(t *testing.T) {
	for _, withToken := range []bool{false, true} {
		r := &documentGenerationFake{current: "newer"}
		q := &documentTestQueue{}
		s := &knowledgeService{taskPendingRepo: r, task: q}
		ctx := context.Background()
		if withToken {
			ctx = types.WithDocumentGeneration(ctx, 1, "k", "old")
		}
		task := asynq.NewTask(types.TypeDocumentProcess,
			[]byte(`{"tenant_id":1,"knowledge_id":"k","knowledge_base_id":"kb"}`))
		info, err := s.enqueueDocument(ctx, task)
		require.NoError(t, err)
		require.NotNil(t, info)
		require.Nil(t, r.op)
		require.Empty(t, q.tasks)
		s.markKnowledgeEnqueueFailed(ctx, &types.Knowledge{ID: "k", TenantID: 1, KnowledgeBaseID: "kb"})
		require.Zero(t, r.updates)
		handled, err := s.FailLegacyDocument(ctx, task, errors.New("old admission failure"))
		require.True(t, handled)
		require.NoError(t, err)
	}
}

func TestDocumentGenerationFencesUnversionedProviderCheckpoint(t *testing.T) {
	r := &documentGenerationFake{current: "newer"}
	s := &knowledgeService{taskPendingRepo: r}
	ctx := types.WithDocumentGeneration(context.Background(), 1, "k", "original-zero")
	require.True(t, s.currentAttemptSuperseded(ctx, "k"))
	require.True(t, s.isKnowledgeSourceReplaced(ctx, &types.Knowledge{ID: "k", TenantID: 1}))
}

func TestDocumentGenerationUnknownCheckpointPreservesFailure(t *testing.T) {
	cause := errors.New("generation database unavailable")
	r := &documentGenerationFake{current: "original", checkErr: cause}
	s := &knowledgeService{taskPendingRepo: r}
	ctx := types.WithDocumentGeneration(context.Background(), 1, "k", "original")
	err := invokeDocumentGenerationTask(ctx, 0, func(runCtx context.Context) error {
		require.True(t, s.isKnowledgeSourceReplaced(runCtx, &types.Knowledge{ID: "k", TenantID: 1}))
		return nil // The pipeline's bool checkpoint stops, but this is not successful completion.
	})
	require.ErrorIs(t, err, cause)
	require.Equal(t, "retry", documentOutcome(err, 0))
}

type documentReparseGenerationProbe struct {
	*replacementGenerationProbe
	afterBegin func()
}

func (p *documentReparseGenerationProbe) BeginDocumentGeneration(
	ctx context.Context, tenant uint64, id, source, target string,
) (string, error) {
	token, err := p.replacementGenerationProbe.BeginDocumentGeneration(ctx, tenant, id, source, target)
	p.op = nil
	if p.afterBegin != nil {
		p.afterBegin()
	}
	return token, err
}

type documentReparseCleanupFailure struct {
	replaceFileChunks
	beforeFailure func()
}

func (r documentReparseCleanupFailure) DeleteChunksByKnowledgeID(context.Context, uint64, string) error {
	if r.beforeFailure != nil {
		r.beforeFailure()
	}
	return errors.New("cleanup storage unavailable")
}

func TestDocumentReparseGenerationFailureSettlesOnlyCurrentOwner(t *testing.T) {
	for _, superseded := range []bool{false, true} {
		t.Run(map[bool]string{false: "current", true: "superseded"}[superseded], func(t *testing.T) {
			h := newReplaceFileHarness(t)
			h.repo.row.ParseStatus = types.ParseStatusProcessing
			tracker := &documentFailureTestTracker{latest: 7}
			h.svc.spanTracker = tracker
			p := &documentReparseGenerationProbe{
				replacementGenerationProbe: &replacementGenerationProbe{row: h.repo},
				afterBegin:                 func() { tracker.latest = 8 },
			}
			p.op = &types.TaskPendingOp{ID: 17}
			h.svc.taskPendingRepo = p
			h.svc.chunkRepo = documentReparseCleanupFailure{beforeFailure: func() {
				if superseded {
					p.token = "winner"
					h.repo.row.ParseStatus = types.ParseStatusPending
					p.op = &types.TaskPendingOp{ID: 18}
					tracker.latest = 9
				}
			}}
			_, err := h.svc.ReparseKnowledge(h.ctx, h.original.ID, nil)
			require.ErrorContains(t, err, "cleanup storage unavailable")
			if superseded {
				require.Equal(t, types.ParseStatusPending, h.repo.row.ParseStatus)
				require.EqualValues(t, 18, p.op.ID)
				require.Empty(t, tracker.finalized)
			} else {
				require.Nil(t, p.op)
				require.Equal(t, types.ParseStatusFailed, h.repo.row.ParseStatus)
				require.Contains(t, h.repo.row.ErrorMessage, "cleanup storage unavailable")
				require.Equal(t, []int{8}, tracker.finalized,
					"failure must settle the root superseded when generation ownership advanced")
			}
		})
	}
}
