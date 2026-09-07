package service

import (
	"context"
	"fmt"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/workspaceevent"
)

type captureSink struct {
	evs []types.WorkspaceEvent
}

func (c *captureSink) Emit(_ context.Context, ev types.WorkspaceEvent) {
	c.evs = append(c.evs, ev)
}

func TestEmitKnowledgeCreatedSkipsFAQ(t *testing.T) {
	sink := &captureSink{}
	s := &knowledgeService{events: sink}
	s.emitKnowledgeCreated(context.Background(), &types.Knowledge{
		ID: "faq-1", Type: types.KnowledgeTypeFAQ, TenantID: 1, KnowledgeBaseID: "kb",
	})
	s.emitKnowledgeCreated(context.Background(), &types.Knowledge{
		ID: "doc-1", Type: types.KnowledgeTypeFile, TenantID: 1, KnowledgeBaseID: "kb",
	})
	if len(sink.evs) != 1 || sink.evs[0].Type != types.EventKnowledgeCreated {
		t.Fatalf("events = %+v", sink.evs)
	}
	data, ok := sink.evs[0].Data.(types.WebhookKnowledgeData)
	if !ok || data.KnowledgeID != "doc-1" {
		t.Fatalf("data = %+v", sink.evs[0].Data)
	}
}

func TestEmitKnowledgeListDeletedChunksByKB(t *testing.T) {
	sink := &captureSink{}
	s := &knowledgeService{events: sink}
	list := make([]*types.Knowledge, 0, 101)
	for i := 0; i < 101; i++ {
		list = append(list, &types.Knowledge{
			ID:              fmt.Sprintf("k-%03d", i),
			TenantID:        7,
			KnowledgeBaseID: "kb-a",
			Type:            types.KnowledgeTypeFile,
		})
	}
	s.emitKnowledgeListDeleted(context.Background(), list)
	if len(sink.evs) != 2 {
		t.Fatalf("101 ids in one kb should split into 2 batch_deleted, got %d", len(sink.evs))
	}
	ids := []string{}
	batchID := ""
	for i, ev := range sink.evs {
		if ev.Type != types.EventKnowledgeBatchDeleted {
			t.Fatalf("type[%d] = %s", i, ev.Type)
		}
		data, ok := ev.Data.(types.WebhookKnowledgeData)
		if !ok {
			t.Fatalf("unexpected data %T", ev.Data)
		}
		if data.Truncated {
			t.Fatal("truncated must stay false")
		}
		if data.BatchTotal != 2 {
			t.Fatalf("batch_total = %d", data.BatchTotal)
		}
		if data.BatchIndex != i+1 {
			t.Fatalf("batch_index = %d want %d", data.BatchIndex, i+1)
		}
		if batchID == "" {
			batchID = data.DeleteBatchID
		} else if data.DeleteBatchID != batchID {
			t.Fatalf("delete_batch_id mismatch")
		}
		ids = append(ids, data.KnowledgeIDs...)
	}
	if len(ids) != 101 {
		t.Fatalf("id union = %d, want 101", len(ids))
	}
}

func TestEmitKnowledgeListDeletedGroupsByKB(t *testing.T) {
	sink := &captureSink{}
	s := &knowledgeService{events: sink}
	s.emitKnowledgeListDeleted(context.Background(), []*types.Knowledge{
		{ID: "a1", TenantID: 7, KnowledgeBaseID: "kb-a", Type: types.KnowledgeTypeFile},
		{ID: "a2", TenantID: 7, KnowledgeBaseID: "kb-a", Type: types.KnowledgeTypeFile},
		{ID: "b1", TenantID: 7, KnowledgeBaseID: "kb-b", Type: types.KnowledgeTypeFile},
	})
	if len(sink.evs) != 2 {
		t.Fatalf("two kbs → 2 events (kb-b still batch because call len>1), got %d", len(sink.evs))
	}
	seen := map[string]int{}
	for _, ev := range sink.evs {
		data := ev.Data.(types.WebhookKnowledgeData)
		if ev.Type != types.EventKnowledgeBatchDeleted {
			t.Fatalf("type = %s", ev.Type)
		}
		seen[data.KnowledgeBaseID] = data.Count
	}
	if seen["kb-a"] != 2 || seen["kb-b"] != 1 {
		t.Fatalf("counts = %+v", seen)
	}
}

func TestEmitKnowledgeListDeletedSingleIsDeleted(t *testing.T) {
	sink := &captureSink{}
	s := &knowledgeService{events: sink}
	s.emitKnowledgeListDeleted(context.Background(), []*types.Knowledge{
		{ID: "only", TenantID: 7, KnowledgeBaseID: "kb-a", Type: types.KnowledgeTypeFile},
	})
	if len(sink.evs) != 1 || sink.evs[0].Type != types.EventKnowledgeDeleted {
		t.Fatalf("events = %+v", sink.evs)
	}
}

func TestWebhookEnvelopeKnowledgeIDsNeverNull(t *testing.T) {
	data := workspaceevent.KnowledgeDataFrom(&types.Knowledge{
		ID: "k1", TenantID: 1, KnowledgeBaseID: "kb", Type: types.KnowledgeTypeFile,
	}, false)
	if data.KnowledgeIDs == nil {
		t.Fatal("knowledge_ids must be empty slice, not nil")
	}
}
