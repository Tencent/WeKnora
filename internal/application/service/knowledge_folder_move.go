package service

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/google/uuid"
)

const knowledgeFolderMoveMaxKnowledgeIDs = 200

var ErrKnowledgeFolderMoveKnowledgeNotFound = errors.New(
	"knowledge folder move knowledge not found",
)

type knowledgeFolderMoveService struct {
	repo interfaces.KnowledgeFolderMoveRepository
}

var _ interfaces.KnowledgeFolderMoveService = (*knowledgeFolderMoveService)(nil)

// NewKnowledgeFolderMoveService creates the scoped folder placement move service.
func NewKnowledgeFolderMoveService(
	repo interfaces.KnowledgeFolderMoveRepository,
) interfaces.KnowledgeFolderMoveService {
	return &knowledgeFolderMoveService{repo: repo}
}

func (s *knowledgeFolderMoveService) MoveKnowledge(
	ctx context.Context,
	input *types.KnowledgeFolderMoveInput,
) (*types.KnowledgeFolderMoveResult, error) {
	knowledgeIDs, targetFolderID, err := validateKnowledgeFolderMoveInput(ctx, input)
	if err != nil {
		return nil, err
	}
	if s == nil || s.repo == nil {
		return nil, ErrKnowledgeFolderInternal
	}

	pendingIDs := make(map[string]string, len(knowledgeIDs))
	for _, knowledgeID := range knowledgeIDs {
		pendingIDs[knowledgeID] = uuid.NewString()
	}
	requestedAt := time.Now().UTC()

	var result *types.KnowledgeFolderMoveResult
	err = s.repo.RunKnowledgeFolderMoveTransaction(
		ctx,
		input.TenantID,
		input.KnowledgeBaseID,
		func(txRepo interfaces.KnowledgeFolderMoveTxRepository) error {
			result = nil

			resolvedTargetFolderID, resolveErr := resolveKnowledgeFolderPlacement(
				ctx,
				txRepo,
				input.TenantID,
				input.KnowledgeBaseID,
				targetFolderID,
			)
			if resolveErr != nil {
				return resolveErr
			}

			knowledges, lockErr := txRepo.LockKnowledgeForFolderMove(
				ctx,
				input.TenantID,
				input.KnowledgeBaseID,
				knowledgeIDs,
			)
			if lockErr != nil {
				return lockErr
			}

			expectedIDs := make(map[string]struct{}, len(knowledgeIDs))
			for _, knowledgeID := range knowledgeIDs {
				expectedIDs[knowledgeID] = struct{}{}
			}
			knowledgeByID := make(map[string]*types.Knowledge, len(knowledges))
			for _, knowledge := range knowledges {
				if knowledge == nil {
					return repository.ErrKnowledgeFolderMoveDataIntegrity
				}
				if _, expected := expectedIDs[knowledge.ID]; !expected {
					return repository.ErrKnowledgeFolderMoveDataIntegrity
				}
				if _, duplicate := knowledgeByID[knowledge.ID]; duplicate {
					return repository.ErrKnowledgeFolderMoveDataIntegrity
				}
				if knowledge.TenantID != input.TenantID ||
					knowledge.KnowledgeBaseID != input.KnowledgeBaseID ||
					knowledge.DeletedAt.Valid {
					return repository.ErrKnowledgeFolderMoveDataIntegrity
				}
				knowledgeByID[knowledge.ID] = knowledge
			}
			if len(knowledges) != len(expectedIDs) ||
				len(knowledgeByID) != len(expectedIDs) {
				return repository.ErrKnowledgeFolderMoveKnowledgeNotFound
			}

			for _, knowledgeID := range knowledgeIDs {
				knowledge, exists := knowledgeByID[knowledgeID]
				if !exists {
					return repository.ErrKnowledgeFolderMoveKnowledgeNotFound
				}
				if knowledge.ParseStatus == types.ParseStatusDeleting {
					return repository.ErrKnowledgeFolderMoveKnowledgeNotFound
				}
				if knowledge.FolderVersion == 0 ||
					knowledge.FolderVersion >= uint64(math.MaxInt64) ||
					knowledge.FolderIndexedVersion > knowledge.FolderVersion {
					return repository.ErrKnowledgeFolderMoveDataIntegrity
				}
			}

			changedCount := 0
			unchangedCount := 0
			for _, knowledgeID := range knowledgeIDs {
				knowledge := knowledgeByID[knowledgeID]
				if knowledge.FolderID == resolvedTargetFolderID {
					unchangedCount++
					continue
				}

				updateErr := txRepo.UpdateKnowledgeFolderForMove(
					ctx,
					interfaces.KnowledgeFolderMoveUpdate{
						TenantID:              input.TenantID,
						KnowledgeBaseID:       input.KnowledgeBaseID,
						KnowledgeID:           knowledge.ID,
						ExpectedFolderID:      knowledge.FolderID,
						ExpectedFolderVersion: knowledge.FolderVersion,
						TargetFolderID:        resolvedTargetFolderID,
						UpdatedAt:             requestedAt,
					},
				)
				if updateErr != nil {
					return updateErr
				}

				newVersion := knowledge.FolderVersion + 1
				pendingErr := txRepo.UpsertKnowledgeFolderIndexPending(
					ctx,
					&types.KnowledgeFolderIndexPending{
						ID:               pendingIDs[knowledge.ID],
						TenantID:         input.TenantID,
						KnowledgeBaseID:  input.KnowledgeBaseID,
						KnowledgeID:      knowledge.ID,
						TargetFolderID:   resolvedTargetFolderID,
						RequestedVersion: newVersion,
						CreatedAt:        requestedAt,
						UpdatedAt:        requestedAt,
					},
				)
				if pendingErr != nil {
					return pendingErr
				}
				changedCount++
			}

			result = &types.KnowledgeFolderMoveResult{
				ChangedCount:   changedCount,
				UnchangedCount: unchangedCount,
			}
			return nil
		},
	)
	if err != nil {
		return nil, mapKnowledgeFolderMoveError(err)
	}
	if result == nil ||
		result.ChangedCount < 0 ||
		result.UnchangedCount < 0 ||
		result.ChangedCount+result.UnchangedCount != len(knowledgeIDs) {
		return nil, ErrKnowledgeFolderInternal
	}
	return result, nil
}

func validateKnowledgeFolderMoveInput(
	ctx context.Context,
	input *types.KnowledgeFolderMoveInput,
) ([]string, string, error) {
	if ctx == nil || input == nil {
		return nil, "", ErrKnowledgeFolderInvalidArgument
	}
	if err := ctx.Err(); err != nil {
		return nil, "", err
	}
	tenantID, ok := types.TenantIDFromContext(ctx)
	if !ok || tenantID == 0 || input.TenantID == 0 || input.TenantID != tenantID {
		return nil, "", ErrKnowledgeFolderInvalidArgument
	}
	if input.KnowledgeBaseID == "" ||
		input.KnowledgeBaseID != strings.TrimSpace(input.KnowledgeBaseID) {
		return nil, "", ErrKnowledgeFolderInvalidArgument
	}
	if len(input.KnowledgeIDs) == 0 ||
		len(input.KnowledgeIDs) > knowledgeFolderMoveMaxKnowledgeIDs {
		return nil, "", ErrKnowledgeFolderInvalidArgument
	}

	seen := make(map[string]struct{}, len(input.KnowledgeIDs))
	knowledgeIDs := make([]string, 0, len(input.KnowledgeIDs))
	for _, rawKnowledgeID := range input.KnowledgeIDs {
		if rawKnowledgeID != strings.TrimSpace(rawKnowledgeID) {
			return nil, "", ErrKnowledgeFolderInvalidArgument
		}
		knowledgeID, err := normalizeKnowledgeFolderID(
			rawKnowledgeID,
			"knowledge_id",
			false,
		)
		if err != nil || knowledgeID != rawKnowledgeID {
			return nil, "", ErrKnowledgeFolderInvalidArgument
		}
		if _, exists := seen[knowledgeID]; exists {
			continue
		}
		seen[knowledgeID] = struct{}{}
		knowledgeIDs = append(knowledgeIDs, knowledgeID)
	}
	sort.Strings(knowledgeIDs)

	targetFolderID := input.TargetFolderID
	if targetFolderID != types.KnowledgeFolderRootID {
		if targetFolderID != strings.TrimSpace(targetFolderID) {
			return nil, "", ErrKnowledgeFolderInvalidArgument
		}
		normalizedTargetFolderID, err := normalizeKnowledgeFolderID(
			targetFolderID,
			"target_folder_id",
			false,
		)
		if err != nil || normalizedTargetFolderID != targetFolderID {
			return nil, "", ErrKnowledgeFolderInvalidArgument
		}
		targetFolderID = normalizedTargetFolderID
	}
	return knowledgeIDs, targetFolderID, nil
}

func mapKnowledgeFolderMoveError(err error) error {
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, context.Canceled),
		errors.Is(err, context.DeadlineExceeded):
		return err
	case errors.Is(err, ErrKnowledgeFolderMoveKnowledgeNotFound):
		return err
	case errors.Is(err, repository.ErrKnowledgeFolderMoveKnowledgeNotFound):
		return fmt.Errorf("%w: resource unavailable", ErrKnowledgeFolderMoveKnowledgeNotFound)
	case errors.Is(err, repository.ErrKnowledgeFolderMoveDataIntegrity),
		errors.Is(err, repository.ErrKnowledgeFolderMoveConflict):
		return fmt.Errorf("%w: move transaction rejected", ErrKnowledgeFolderDataIntegrity)
	default:
		return mapKnowledgeFolderError(err)
	}
}
