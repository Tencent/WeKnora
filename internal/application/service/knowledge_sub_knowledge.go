package service

import (
	"context"
	"errors"

	"github.com/Tencent/WeKnora/internal/application/repository"
	werrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"gorm.io/gorm"
)

var _ interfaces.SubKnowledgeService = (*knowledgeService)(nil)

func subKnowledgeWriteError(err error) error {
	var conflict *repository.SubKnowledgeConflictError
	if errors.As(err, &conflict) {
		return werrors.NewConflictError(err.Error()).
			WithDetails(map[string]string{"existing_knowledge_id": conflict.KnowledgeID})
	}
	if errors.Is(err, repository.ErrSubKnowledgeConflict) {
		return werrors.NewConflictError(err.Error())
	}
	if errors.Is(err, repository.ErrSubKnowledgeIdentityChange) {
		return werrors.NewBadRequestError(err.Error())
	}
	return err
}

func (s *knowledgeService) createManualSubKnowledge(
	ctx context.Context,
	k *types.Knowledge,
	payload *types.ManualKnowledgePayload,
	status string,
) (*types.Knowledge, bool, error) {
	if err := s.validateImportParent(ctx, k.KnowledgeBaseID, payload.ParentKnowledgeID); err != nil {
		return nil, false, err
	}
	k.ParentKnowledgeID = payload.ParentKnowledgeID
	if payload.SubKnowledge == nil {
		if k.ParentKnowledgeID != "" {
			if err := s.validateKnowledgeTagIDs(ctx, k.TenantID, k.KnowledgeBaseID, payload.TagIDs); err != nil {
				return nil, false, err
			}
			r, ok := s.repo.(interfaces.KnowledgeTreeRepository)
			if !ok {
				return nil, false, werrors.NewServiceUnavailableError("parent repository unavailable")
			}
			err := r.CreateKnowledgeWithParent(ctx, k, payload.TagIDs...)
			return k, true, err
		}
		err := s.repo.CreateKnowledge(ctx, k)
		return k, true, err
	}
	if err := payload.SubKnowledge.Validate(); err != nil {
		return nil, false, werrors.NewBadRequestError(err.Error())
	}
	if payload.Title == "" {
		return nil, false, werrors.NewBadRequestError("sub_knowledge knowledge requires an explicit title")
	}
	if err := s.validateKnowledgeTagIDs(ctx, k.TenantID, k.KnowledgeBaseID, payload.TagIDs); err != nil {
		return nil, false, err
	}
	r, ok := s.repo.(interfaces.SubKnowledgeRepository)
	if !ok {
		return nil, false, werrors.NewInternalServerError("sub_knowledge repository unavailable")
	}
	meta, err := k.ManualMetadata()
	if err != nil {
		return nil, false, err
	}
	f := &types.SubKnowledgeMapping{
		SubKnowledgeInput: *payload.SubKnowledge,
		SourceHash:        payload.SubKnowledge.ContentHash(k.Title, meta.Content, status),
	}
	created, isNew, err := r.CreateSubKnowledge(ctx, k, f, payload.TagIDs...)
	return created, isNew, subKnowledgeWriteError(err)
}

func (s *knowledgeService) persistManualSubKnowledge(
	ctx context.Context,
	k *types.Knowledge,
	payload *types.ManualKnowledgePayload,
	status string,
) error {
	r, ok := s.repo.(interfaces.SubKnowledgeRepository)
	if !ok {
		if payload.SubKnowledge != nil {
			return werrors.NewInternalServerError("sub_knowledge repository unavailable")
		}
		return s.repo.UpdateKnowledge(ctx, k)
	}
	f, err := r.GetSubKnowledgeMapping(ctx, k.TenantID, k.ID)
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	if errors.Is(err, gorm.ErrRecordNotFound) && payload.SubKnowledge == nil {
		return s.repo.UpdateKnowledge(ctx, k)
	}
	var input types.SubKnowledgeInput
	if f != nil && err == nil {
		input = f.SubKnowledgeInput
	}
	if payload.SubKnowledge != nil {
		if e := payload.SubKnowledge.Validate(); e != nil {
			return werrors.NewBadRequestError(e.Error())
		}
		if err == nil && !input.SameIdentity(*payload.SubKnowledge) {
			return subKnowledgeWriteError(repository.ErrSubKnowledgeIdentityChange)
		}
		input = *payload.SubKnowledge
	}
	meta, e := k.ManualMetadata()
	if e != nil {
		return e
	}
	return subKnowledgeWriteError(
		r.UpdateSubKnowledge(ctx, k, &input, input.ContentHash(k.Title, meta.Content, status)),
	)
}

// GetSubKnowledge must be called after resolving the anchor's KB read grant.
// It never widens scope to another KB even if the group ID is the same.
func (s *knowledgeService) GetSubKnowledge(
	ctx context.Context,
	id, authorizedKB, cursor string,
	pageSize int,
) (*types.SubKnowledgeGroup, error) {
	tenant, ok := types.TenantIDFromContext(ctx)
	if !ok || tenant == 0 {
		return nil, werrors.NewUnauthorizedError("workspace context unavailable")
	}
	k, err := s.repo.GetKnowledgeByID(ctx, tenant, id)
	if err != nil || k == nil || authorizedKB == "" || k.KnowledgeBaseID != authorizedKB {
		return nil, werrors.NewNotFoundError("knowledge not found")
	}
	if k.ParseStatus == types.ParseStatusDeleting || k.ParseStatus == types.ManualKnowledgeStatusDraft {
		return nil, werrors.NewNotFoundError("published sub_knowledge knowledge not found")
	}
	r, ok := s.repo.(interfaces.SubKnowledgeRepository)
	if !ok {
		return nil, werrors.NewInternalServerError("sub_knowledge repository unavailable")
	}
	f, err := r.GetSubKnowledgeMapping(ctx, tenant, id)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, werrors.NewNotFoundError("sub_knowledge source not found")
	}
	if err != nil {
		return nil, err
	}
	if f.KnowledgeBaseID != authorizedKB {
		return nil, werrors.NewNotFoundError("sub_knowledge source scope mismatch")
	}
	result, err := r.ListSubKnowledge(ctx, tenant, authorizedKB, f.SourceNamespace, f.RootID, cursor, pageSize)
	if errors.Is(err, repository.ErrInvalidSubKnowledgeCursor) {
		return nil, werrors.NewBadRequestError(err.Error())
	}
	return result, err
}

// SetSubKnowledge is relation-only: it never rewrites the original or vectors.
func (s *knowledgeService) SetSubKnowledge(
	ctx context.Context,
	id, authorizedKB string,
	input *types.SubKnowledgeInput,
) error {
	tenant, ok := types.TenantIDFromContext(ctx)
	if !ok || tenant == 0 {
		return werrors.NewUnauthorizedError("workspace context unavailable")
	}
	if input == nil {
		return werrors.NewBadRequestError("sub_knowledge is required")
	}
	if err := input.Validate(); err != nil {
		return werrors.NewBadRequestError(err.Error())
	}
	r, ok := s.repo.(interfaces.SubKnowledgeRepository)
	if !ok {
		return werrors.NewInternalServerError("sub_knowledge repository unavailable")
	}
	err := r.SetSubKnowledge(ctx, tenant, id, authorizedKB, input)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return werrors.NewNotFoundError("knowledge not found in authorized KB")
	}
	return subKnowledgeWriteError(err)
}
