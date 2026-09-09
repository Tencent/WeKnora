// Package learning implements private source-backed Wiki practice. Database
// transactions and answer grading live behind LearningRepository.
package learning

import (
	"context"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

type service struct {
	repo   interfaces.LearningRepository
	models interfaces.ModelService
	tasks  interfaces.TaskEnqueuer
	memory interfaces.MemoryService
}

// NewService wires private learning operations to persistence, models, tasks and optional memory.
func NewService(
	repo interfaces.LearningRepository,
	models interfaces.ModelService,
	tasks interfaces.TaskEnqueuer,
	memory interfaces.MemoryService,
) interfaces.LearningService {
	return &service{repo: repo, models: models, tasks: tasks, memory: memory}
}

// ResolveScope deliberately bypasses the legacy fallback context getters.
// A borrowed execution tenant or synthetic UserID never creates a learner.
func ResolveScope(ctx context.Context) (interfaces.LearningScope, error) {
	if ctx == nil {
		return interfaces.LearningScope{}, types.ErrLearningForbidden
	}
	c, cok := ctx.Value(types.CallerContextKey).(types.Caller)
	p, pok := ctx.Value(types.PrincipalContextKey).(types.Principal)
	exec, eok := types.TenantIDFromContext(ctx)
	if !cok || !pok || !eok || c.TenantID == 0 || c.TenantID != exec || p.Type != types.PrincipalWebUser ||
		p.ID == "" || p.ID != strings.TrimSpace(p.ID) || c.UserID != p.ID || len(p.StorageID()) > 128 ||
		ctx.Value(types.TenantAPIKeyScopeContextKey) != nil {
		return interfaces.LearningScope{}, types.ErrLearningForbidden
	}
	return interfaces.LearningScope{TenantID: c.TenantID, SubjectID: p.StorageID()}, nil
}

func validID(id string) bool { return id != "" && len(id) <= 36 && strings.TrimSpace(id) == id }

func (s *service) GetSettings(ctx context.Context) (*types.LearningSettings, error) {
	scope, err := ResolveScope(ctx)
	if err != nil {
		return nil, err
	}
	return s.repo.Settings(ctx, scope)
}

func (s *service) SetEnabled(ctx context.Context, enabled bool) (*types.LearningSettings, error) {
	scope, err := ResolveScope(ctx)
	if err != nil {
		return nil, err
	}
	return s.repo.SetEnabled(ctx, scope, enabled)
}

func (s *service) Overview(ctx context.Context, kbID string) (*types.LearningOverview, error) {
	scope, err := ResolveScope(ctx)
	if err != nil {
		return nil, err
	}
	if !validID(kbID) {
		return nil, types.ErrLearningInvalid
	}
	return s.repo.Overview(ctx, scope, kbID)
}

func (s *service) familiar(ctx context.Context, nodes []*types.LearningNode, views []*types.LearningNodeView) {
	if s.memory == nil || !s.memory.MemoryAvailable(ctx) {
		return
	}
	ids := []string{}
	seen := map[string]bool{}
	for _, n := range nodes {
		for _, id := range n.Page.SourceKnowledgeIDs() {
			if !seen[id] && len(ids) < types.LearningMaxNodes {
				seen[id] = true
				ids = append(ids, id)
			}
		}
	}
	// DocumentAffinity also enforces the independent retrieval-conditioning
	// switch. Only source IDs from admitted pages reach memory lookup.
	known := s.memory.DocumentAffinity(ctx, ids)
	for i, n := range nodes {
		for _, id := range n.Page.SourceKnowledgeIDs() {
			if known[id] >= types.MemoryDocAffinityMinHits {
				views[i].Familiar = true
				break
			}
		}
	}
}

func (s *service) Node(ctx context.Context, id string) (*types.LearningNodeView, error) {
	scope, err := ResolveScope(ctx)
	if err != nil {
		return nil, err
	}
	if !validID(id) {
		return nil, types.ErrLearningInvalid
	}
	n, err := s.repo.Node(ctx, scope, id)
	if err != nil {
		return nil, err
	}
	v := types.LearningNodePublic(n, time.Now())
	s.familiar(ctx, []*types.LearningNode{n}, []*types.LearningNodeView{v})
	return v, nil
}

func (s *service) Overlay(ctx context.Context, kbID string, slugs []string) ([]*types.LearningNodeView, error) {
	scope, err := ResolveScope(ctx)
	if err != nil {
		return nil, err
	}
	if !validID(kbID) || len(slugs) > types.LearningMaxNodes {
		return nil, types.ErrLearningInvalid
	}
	for _, slug := range slugs {
		if slug == "" || len(slug) > 255 {
			return nil, types.ErrLearningInvalid
		}
	}
	nodes, err := s.repo.Overlay(ctx, scope, kbID, slugs)
	if err != nil {
		return nil, err
	}
	views := make([]*types.LearningNodeView, 0, len(nodes))
	for _, n := range nodes {
		views = append(views, types.LearningNodePublic(n, time.Now()))
	}
	s.familiar(ctx, nodes, views)
	return views, nil
}

func (s *service) RecordView(ctx context.Context, id string) error {
	scope, err := ResolveScope(ctx)
	if err != nil {
		return err
	}
	if !validID(id) {
		return types.ErrLearningInvalid
	}
	return s.repo.RecordView(ctx, scope, id)
}

func (s *service) PrepareQuiz(ctx context.Context, id string) (*types.LearningQuizView, error) {
	scope, err := ResolveScope(ctx)
	if err != nil {
		return nil, err
	}
	if !validID(id) {
		return nil, types.ErrLearningInvalid
	}
	quiz, wake, err := s.repo.PrepareQuiz(ctx, scope, id)
	if err != nil {
		return nil, err
	}
	// A failed trigger must not roll back durable pending work. Recover will
	// re-enqueue it; returning pending avoids duplicate client preparations.
	if wake != nil {
		_ = s.enqueue(*wake)
	}
	return quiz, nil
}

func (s *service) GetQuiz(ctx context.Context, id string) (*types.LearningQuizView, error) {
	scope, err := ResolveScope(ctx)
	if err != nil {
		return nil, err
	}
	if !validID(id) {
		return nil, types.ErrLearningInvalid
	}
	return s.repo.GetQuiz(ctx, scope, id)
}

func (s *service) SubmitAnswer(ctx context.Context, answer types.LearningAnswer) (*types.LearningAnswerResult, error) {
	scope, err := ResolveScope(ctx)
	if err != nil {
		return nil, err
	}
	return s.repo.SubmitAnswer(ctx, scope, answer)
}

func (s *service) Export(ctx context.Context, kbID string) (*types.LearningExport, error) {
	scope, err := ResolveScope(ctx)
	if err != nil {
		return nil, err
	}
	if kbID != "" && !validID(kbID) {
		return nil, types.ErrLearningInvalid
	}
	return s.repo.Export(ctx, scope, kbID)
}

func (s *service) Clear(ctx context.Context, kbID string) (*types.LearningClearResult, error) {
	scope, err := ResolveScope(ctx)
	if err != nil {
		return nil, err
	}
	if kbID != "" && !validID(kbID) {
		return nil, types.ErrLearningInvalid
	}
	return s.repo.Clear(ctx, scope, kbID)
}

func (s *service) Recommend(ctx context.Context, kbID string, limit int) ([]*types.LearningRecommendation, error) {
	scope, err := ResolveScope(ctx)
	if err != nil {
		return nil, err
	}
	if !validID(kbID) || limit < 0 || limit > 20 {
		return nil, types.ErrLearningInvalid
	}
	if limit == 0 {
		limit = 5
	}
	settings, err := s.repo.Settings(ctx, scope)
	if err != nil {
		return nil, err
	}
	if !settings.Enabled {
		return nil, types.ErrLearningDisabled
	}
	var interests []string
	if s.memory != nil && s.memory.MemoryAvailable(ctx) {
		interests = s.memory.RetrievalContextFor(ctx).Interests
	}
	nodes, err := s.repo.Candidates(ctx, scope, kbID, interests)
	if err != nil {
		return nil, err
	}
	views := make([]*types.LearningNodeView, 0, len(nodes))
	for _, n := range nodes {
		views = append(views, types.LearningNodePublic(n, time.Now()))
	}
	s.familiar(ctx, nodes, views)
	return rankRecommendations(nodes, views, interests, limit), nil
}
