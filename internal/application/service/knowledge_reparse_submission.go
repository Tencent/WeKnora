package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// ErrReparseSubmissionConflict rejects reuse for a different action or overlapping work.
var ErrReparseSubmissionConflict = errors.New("reparse submission conflicts with an existing submission")

// ErrInvalidReparseBatch rejects a batch that cannot own the selected submission.
var ErrInvalidReparseBatch = errors.New("invalid managed reparse batch or member selection")

// ReparseSubmissionService persists dispatch receipts and guards destructive member handoffs.
type ReparseSubmissionService interface {
	PrepareReparseSubmission(
		context.Context, uint64, string, string, []string, *types.KnowledgeProcessOverrides, string,
	) (*types.ReparseBatchSubmission, bool, error)
	MarkReparseSubmissionQueued(context.Context, *types.ReparseBatchSubmission) error
	MarkReparseSubmissionEnqueueUnknown(context.Context, *types.ReparseBatchSubmission) error
	MarkReparseSubmissionProcessing(context.Context, string) error
	CompleteReparseSubmission(context.Context, string, bool) error
	FailReparseSubmissionScope(context.Context, string) error
	ClaimReparseMember(context.Context, string, string, string) (string, error)
}

func (s *knowledgeService) reparseSubmissions() (repository.ReparseSubmissionStore, error) {
	p, ok := s.repo.(interface {
		ReparseSubmissionStore() repository.ReparseSubmissionStore
	})
	if !ok {
		return nil, fmt.Errorf("reparse submission persistence is unavailable")
	}
	return p.ReparseSubmissionStore(), nil
}

func canonicalSubmissionIDs(ids []string) []string {
	out := append([]string(nil), ids...)
	sort.Strings(out)
	return out
}

func reparseSubmissionHash(
	kbID, batchID string, ids []string, config *types.KnowledgeProcessOverrides,
) (string, error) {
	body := struct {
		KBID    string                           `json:"kb_id"`
		BatchID string                           `json:"batch_id,omitempty"`
		IDs     []string                         `json:"ids"`
		Config  *types.KnowledgeProcessOverrides `json:"process_config,omitempty"`
	}{kbID, batchID, canonicalSubmissionIDs(ids), config}
	b, err := json.Marshal(body)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}

func frozenMembersContain(members []*types.ReparseBatchMember, ids []string) bool {
	available := make(map[string]struct{}, len(members))
	for _, m := range members {
		available[m.KnowledgeID] = struct{}{}
	}
	for _, id := range ids {
		if _, ok := available[id]; !ok {
			return false
		}
	}
	return true
}

// PrepareReparseSubmission persists an immutable receipt before touching Redis.
// replay says the existing receipt is authoritative and must not be enqueued again.
func (s *knowledgeService) PrepareReparseSubmission(
	ctx context.Context, tenant uint64, kbID, batchID string, ids []string,
	config *types.KnowledgeProcessOverrides, key string,
) (*types.ReparseBatchSubmission, bool, error) {
	store, err := s.reparseSubmissions()
	if err != nil {
		return nil, false, err
	}
	requestedBatchID := batchID
	automatic := batchID == ""
	hash, err := reparseSubmissionHash(kbID, requestedBatchID, ids, config)
	if err != nil {
		return nil, false, err
	}
	key = strings.TrimSpace(key)
	if key != "" {
		old, lookupErr := store.GetReparseSubmissionByKey(ctx, tenant, key)
		if lookupErr == nil {
			if old.RequestSHA256 != hash {
				return nil, false, ErrReparseSubmissionConflict
			}
			return old, true, nil
		}
		if !errors.Is(lookupErr, gorm.ErrRecordNotFound) {
			return nil, false, lookupErr
		}
	}
	batches, err := s.reparseBatches()
	if err != nil {
		return nil, false, err
	}
	if batchID == "" {
		overlap, overlapErr := store.HasActiveReparseSubmissionOverlap(ctx, tenant, ids, "")
		if overlapErr != nil {
			return nil, false, overlapErr
		}
		if overlap {
			return nil, false, ErrReparseSubmissionConflict
		}
		batch, createErr := s.CreateReparseBatch(ctx, tenant, kbID, "managed", "", ids)
		if createErr != nil {
			return nil, false, createErr
		}
		batchID = batch.ID
	} else {
		batch, batchErr := batches.GetReparseBatch(ctx, tenant, batchID)
		if batchErr != nil && !errors.Is(batchErr, gorm.ErrRecordNotFound) {
			return nil, false, batchErr
		}
		if batch == nil || batch.Mode != "managed" || batch.KnowledgeBaseID != kbID {
			return nil, false, ErrInvalidReparseBatch
		}
		members, _, listErr := batches.ListReparseBatchMembers(ctx, batchID, 0, 50100)
		if listErr != nil {
			return nil, false, listErr
		}
		if !frozenMembersContain(members, ids) {
			return nil, false, ErrInvalidReparseBatch
		}
		old, lookupErr := store.FindReparseSubmission(ctx, tenant, batchID, hash)
		if lookupErr == nil {
			return old, true, nil
		}
		if !errors.Is(lookupErr, gorm.ErrRecordNotFound) {
			return nil, false, lookupErr
		}
		overlap, overlapErr := store.HasActiveReparseSubmissionOverlap(ctx, tenant, ids, "")
		if overlapErr != nil {
			return nil, false, overlapErr
		}
		if overlap {
			return nil, false, ErrReparseSubmissionConflict
		}
	}
	receipt := &types.ReparseBatchSubmission{
		ID: uuid.NewString(), BatchID: batchID, TenantID: tenant, IdempotencyKey: key,
		RequestSHA256: hash, State: "created",
	}
	receipt.TaskID = "reparse-batch-" + receipt.ID
	if err := store.CreateAndAssignReparseSubmission(ctx, receipt, ids); err != nil {
		if automatic {
			if cleanupErr := batches.DiscardUnsubmittedReparseBatch(ctx, tenant, batchID); cleanupErr != nil {
				return nil, false, cleanupErr
			}
		}
		if key != "" {
			if old, lookupErr := store.GetReparseSubmissionByKey(ctx, tenant, key); lookupErr == nil {
				if old.RequestSHA256 == hash {
					return old, true, nil
				}
				return nil, false, ErrReparseSubmissionConflict
			}
		}
		if requestedBatchID != "" {
			if old, lookupErr := store.FindReparseSubmission(ctx, tenant, requestedBatchID, hash); lookupErr == nil {
				return old, true, nil
			}
		}
		if errors.Is(err, repository.ErrReparseSubmissionMembersUnavailable) {
			return nil, false, ErrReparseSubmissionConflict
		}
		return nil, false, err
	}
	return receipt, false, nil
}

func (s *knowledgeService) MarkReparseSubmissionQueued(
	ctx context.Context, receipt *types.ReparseBatchSubmission,
) error {
	store, err := s.reparseSubmissions()
	if err != nil {
		return err
	}
	return store.MarkReparseSubmissionQueued(ctx, receipt.ID, receipt.TaskID)
}

func (s *knowledgeService) MarkReparseSubmissionEnqueueUnknown(
	ctx context.Context, receipt *types.ReparseBatchSubmission,
) error {
	store, err := s.reparseSubmissions()
	if err != nil {
		return err
	}
	return store.MarkReparseSubmissionEnqueueUnknown(ctx, receipt.ID, receipt.TaskID)
}

func (s *knowledgeService) MarkReparseSubmissionProcessing(ctx context.Context, id string) error {
	store, err := s.reparseSubmissions()
	if err != nil {
		return err
	}
	return store.MarkReparseSubmissionProcessing(ctx, id)
}

func (s *knowledgeService) CompleteReparseSubmission(ctx context.Context, id string, succeeded bool) error {
	store, err := s.reparseSubmissions()
	if err != nil {
		return err
	}
	state := "completed"
	if !succeeded {
		state = "failed"
	}
	return store.SetReparseSubmissionState(ctx, id, state, "")
}

func (s *knowledgeService) FailReparseSubmissionScope(ctx context.Context, id string) error {
	store, err := s.reparseSubmissions()
	if err != nil {
		return err
	}
	return store.FailReparseSubmissionScope(ctx, id)
}

func (s *knowledgeService) ClaimReparseMember(
	ctx context.Context, batchID, knowledgeID, submissionID string,
) (string, error) {
	store, err := s.reparseSubmissions()
	if err != nil {
		return "", err
	}
	return store.ClaimReparseMember(ctx, batchID, knowledgeID, submissionID)
}
