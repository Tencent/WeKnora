package events

import (
	"context"
	"sync"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/Tencent/WeKnora/pluginsdk/pluginapi"
)

// WatchKnowledge wraps the knowledge repository to publish
// knowledge.ingested and knowledge.failed. A document reaches either state
// through many code paths (enrichment finishing last, no enrichment to do,
// a dozen failure branches), but only through these few writes, so the
// events are raised here, on the transition only.
func WatchKnowledge(repo interfaces.KnowledgeRepository) interfaces.KnowledgeRepository {
	return &knowledgeWatch{KnowledgeRepository: repo}
}

type knowledgeWatch struct {
	interfaces.KnowledgeRepository
}

func terminal(status string) bool {
	return status == types.ParseStatusCompleted || status == types.ParseStatusFailed
}

// PublishKnowledge raises knowledge.ingested or knowledge.failed for a row
// that reached that status outside the watched repository methods (bulk
// updates, recovery sweeps).
func PublishKnowledge(ctx context.Context, k *types.Knowledge) { publishKnowledge(ctx, k) }

// deferred holds rows whose status changed before plugins were loaded
// (the startup reset of interrupted documents).
var deferred struct {
	mu   sync.Mutex
	rows []*types.Knowledge
}

// DeferKnowledge keeps rows to publish once plugins are loaded: at startup
// the dispatcher does not know yet which plugins subscribe.
func DeferKnowledge(rows ...*types.Knowledge) {
	deferred.mu.Lock()
	defer deferred.mu.Unlock()
	deferred.rows = append(deferred.rows, rows...)
}

// FlushDeferred publishes the rows DeferKnowledge kept; call it once plugins
// are loaded.
func FlushDeferred(ctx context.Context) {
	deferred.mu.Lock()
	rows := deferred.rows
	deferred.rows = nil
	deferred.mu.Unlock()
	for _, k := range rows {
		publishKnowledge(ctx, k)
	}
}

// PublishDeleted raises knowledge.deleted for rows that were deleted.
func PublishDeleted(ctx context.Context, tenantID uint64, rows []*types.Knowledge) {
	for _, k := range rows {
		if k == nil {
			continue
		}
		Publish(ctx, tenantID, pluginapi.EventKnowledgeDeleted, pluginapi.KnowledgeEventData{
			KnowledgeBaseID: k.KnowledgeBaseID, KnowledgeID: k.ID, Title: k.Title,
			FileName: k.FileName, FileType: k.FileType, Source: k.Source,
		})
	}
}

// publishKnowledge raises the event for a row that just reached status.
func publishKnowledge(ctx context.Context, k *types.Knowledge) {
	if k == nil {
		return
	}
	data := pluginapi.KnowledgeEventData{
		KnowledgeBaseID: k.KnowledgeBaseID, KnowledgeID: k.ID, Title: k.Title,
		FileName: k.FileName, FileType: k.FileType, Source: k.Source,
	}
	switch k.ParseStatus {
	case types.ParseStatusCompleted:
		Publish(ctx, k.TenantID, pluginapi.EventKnowledgeIngested, data)
	case types.ParseStatusFailed:
		data.Error = k.ErrorMessage
		Publish(ctx, k.TenantID, pluginapi.EventKnowledgeFailed, data)
	}
}

// previous reads a row's status before a write that may change it, only
// when events are on (the read is not free).
func (w *knowledgeWatch) previous(ctx context.Context, id string) string {
	if current.Load() == nil {
		return ""
	}
	before, err := w.GetKnowledgeByIDOnly(ctx, id)
	if err != nil || before == nil {
		return ""
	}
	return before.ParseStatus
}

func (w *knowledgeWatch) UpdateKnowledge(ctx context.Context, k *types.Knowledge) error {
	if k == nil || !terminal(k.ParseStatus) {
		return w.KnowledgeRepository.UpdateKnowledge(ctx, k)
	}
	was := w.previous(ctx, k.ID)
	if err := w.KnowledgeRepository.UpdateKnowledge(ctx, k); err != nil {
		return err
	}
	if was != k.ParseStatus {
		publishKnowledge(ctx, k)
	}
	return nil
}

func (w *knowledgeWatch) UpdateKnowledgeForTransfer(ctx context.Context, before, after *types.Knowledge) error {
	if err := w.KnowledgeRepository.UpdateKnowledgeForTransfer(ctx, before, after); err != nil {
		return err
	}
	if after != nil && terminal(after.ParseStatus) && (before == nil || before.ParseStatus != after.ParseStatus) {
		publishKnowledge(ctx, after)
	}
	return nil
}

func (w *knowledgeWatch) UpdateKnowledgeColumns(ctx context.Context, id string, values map[string]interface{}) error {
	status, _ := values["parse_status"].(string)
	if !terminal(status) {
		return w.KnowledgeRepository.UpdateKnowledgeColumns(ctx, id, values)
	}
	was := w.previous(ctx, id)
	if err := w.KnowledgeRepository.UpdateKnowledgeColumns(ctx, id, values); err != nil {
		return err
	}
	if was != status {
		w.publishByID(ctx, id)
	}
	return nil
}

func (w *knowledgeWatch) FinalizeSubtask(ctx context.Context, id string) (int, bool, error) {
	n, promoted, err := w.KnowledgeRepository.FinalizeSubtask(ctx, id)
	if err == nil && promoted {
		w.publishByID(ctx, id)
	}
	return n, promoted, err
}

func (w *knowledgeWatch) CompleteProcessingWithoutSubtasks(ctx context.Context, id string) (bool, error) {
	completed, err := w.KnowledgeRepository.CompleteProcessingWithoutSubtasks(ctx, id)
	if err == nil && completed {
		w.publishByID(ctx, id)
	}
	return completed, err
}

func (w *knowledgeWatch) publishByID(ctx context.Context, id string) {
	if current.Load() == nil {
		return
	}
	k, err := w.GetKnowledgeByIDOnly(ctx, id)
	if err == nil {
		publishKnowledge(ctx, k)
	}
}
