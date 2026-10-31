package service

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/hibiken/asynq"
)

// DocumentWikiStartRequest bounds one operator request, not the automatic worker pool.
type DocumentWikiStartRequest struct {
	KnowledgeID     string `json:"knowledge_id,omitempty"`
	KnowledgeBaseID string `json:"knowledge_base_id,omitempty"`
	Search          string `json:"search,omitempty"`
	Count           int    `json:"count"`
}

// Valid rejects unbounded requests and ambiguous single-document batches.
func (r DocumentWikiStartRequest) Valid() bool {
	return r.Count >= 1 && r.Count <= 100 && validWikiStartKnowledgeID(r.KnowledgeID) &&
		validWikiStartKnowledgeID(r.KnowledgeBaseID) &&
		utf8.RuneCountInString(r.Search) <= 200 && (r.KnowledgeID == "" || r.Count == 1)
}

// DocumentWikiStartResult reports queue acceptance, never completed Wiki generation.
type DocumentWikiStartResult struct {
	Requested     int `json:"requested"`
	Started       int `json:"started"`
	AlreadyQueued int `json:"already_queued"`
}

// StartWiki re-arms existing operations without rewriting their payload or failure budget.
func (s *DocumentProgressService) StartWiki(
	ctx context.Context, request DocumentWikiStartRequest,
) (*DocumentWikiStartResult, error) {
	if !request.Valid() {
		return nil, errors.New("invalid Wiki start request")
	}
	if s.task == nil {
		return nil, errors.New("wiki task queue is unavailable")
	}
	result := &DocumentWikiStartResult{Requested: request.Count}
	// Bound candidate scans even when many older rows already have a trigger.
	query := s.query(ctx, DocumentProgressFilter{
		KnowledgeBaseID: request.KnowledgeBaseID, Search: request.Search, Status: "all",
	}).Joins("JOIN task_pending_ops p ON "+s.pendingSQL()).
		Where("k.parse_status IN ?", []string{types.ParseStatusCompleted, types.ParseStatusFinalizing}).
		Where(s.wikiEnabledSQL()).
		Where(`NOT EXISTS(SELECT 1 FROM task_pending_ops held WHERE held.task_type=p.task_type
			AND held.scope=p.scope AND held.scope_id=p.scope_id AND held.dedup_key=p.dedup_key
			AND held.claimed_at>=?)`, time.Now().Add(-wikiClaimStaleAfter))
	if request.KnowledgeID != "" {
		query = query.Where("k.id=?", request.KnowledgeID)
	}
	var candidates []struct {
		ID              string
		TenantID        uint64
		KnowledgeBaseID string
		AnchorID        int64
	}
	if err := query.Select("k.id,k.tenant_id,k.knowledge_base_id,MIN(p.id) AS anchor_id").
		Group("k.id,k.tenant_id,k.knowledge_base_id").Order("MIN(p.id) ASC,k.id ASC").Limit(1000).
		Scan(&candidates).Error; err != nil {
		return nil, err
	}
	for _, candidate := range candidates {
		if result.Started >= request.Count {
			break
		}
		payload := WikiIngestPayload{
			TenantID: candidate.TenantID, KnowledgeBaseID: candidate.KnowledgeBaseID, KnowledgeID: candidate.ID,
			Language: WikiPendingLanguage(ctx, s.db, candidate.TenantID, candidate.KnowledgeBaseID),
		}
		raw, err := json.Marshal(payload)
		if err != nil {
			return result, err
		}
		identity := fmt.Sprintf("%d:%s:%s:%d",
			candidate.TenantID, candidate.KnowledgeBaseID, candidate.ID, candidate.AnchorID)
		digest := sha256.Sum256([]byte(identity))
		task := asynq.NewTask(types.TypeWikiIngest, raw, asynq.Queue(types.QueueWiki),
			asynq.MaxRetry(wikiIngestMaxRetry), asynq.Timeout(time.Hour))
		_, err = s.task.Enqueue(task, asynq.TaskID(fmt.Sprintf("wiki-start-%x", digest[:16])))
		if errors.Is(err, asynq.ErrTaskIDConflict) || errors.Is(err, asynq.ErrDuplicateTask) {
			result.AlreadyQueued++
			continue
		}
		if err != nil {
			return result, fmt.Errorf("enqueue Wiki start: %w", err)
		}
		result.Started++
	}
	return result, nil
}

func validWikiStartKnowledgeID(id string) bool {
	return len(id) <= 64 && strings.TrimSpace(id) == id
}
