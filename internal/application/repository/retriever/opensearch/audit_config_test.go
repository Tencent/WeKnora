package opensearch

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

// spySink records audit events for assertions.
type spySink struct {
	mu           sync.Mutex
	indexCreated []indexCreatedEvent
	reindex      []reindexEvent
}

type indexCreatedEvent struct {
	alias string
	dim   int
}
type reindexEvent struct {
	src, dst string
	docs     int64
}

func (s *spySink) EmitIndexCreated(_ context.Context, alias string, dim int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.indexCreated = append(s.indexCreated, indexCreatedEvent{alias, dim})
}

func (s *spySink) EmitReindexExecuted(_ context.Context, src, dst string, docs int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reindex = append(s.reindex, reindexEvent{src, dst, docs})
}

// TestBuildInternalCfg_ReadsHNSWFields verifies the HNSW IndexConfig fields
// flow into internalCfg, falling back to defaults when unset.
func TestBuildInternalCfg_ReadsHNSWFields(t *testing.T) {
	t.Run("reads set fields", func(t *testing.T) {
		cfg, err := buildInternalCfg(&types.IndexConfig{
			HNSWM:              24,
			HNSWEFConstruction: 200,
			HNSWEFSearch:       128,
			KNNEngine:          "faiss",
			NumberOfShards:     2,
		})
		if err != nil {
			t.Fatal(err)
		}
		if cfg.hnswM != 24 || cfg.hnswEFConstruction != 200 || cfg.efSearch != 128 || cfg.knnEngine != "faiss" {
			t.Errorf("HNSW not wired: %+v", cfg)
		}
		if cfg.shards != 2 {
			t.Errorf("shards: want 2, got %d", cfg.shards)
		}
	})

	t.Run("defaults when unset", func(t *testing.T) {
		cfg, err := buildInternalCfg(&types.IndexConfig{})
		if err != nil {
			t.Fatal(err)
		}
		if cfg.hnswM != 16 || cfg.hnswEFConstruction != 100 || cfg.efSearch != 100 || cfg.knnEngine != "lucene" {
			t.Errorf("defaults not applied: %+v", cfg)
		}
	})

	t.Run("nil config is all defaults", func(t *testing.T) {
		cfg, err := buildInternalCfg(nil)
		if err != nil {
			t.Fatal(err)
		}
		if cfg.knnEngine != "lucene" || cfg.hnswM != 16 {
			t.Errorf("nil defaults wrong: %+v", cfg)
		}
	})
}

// TestBuildIndexMapping_ReflectsHNSWConfig verifies the end-to-end path
// IndexConfig → buildInternalCfg → buildIndexMapping carries the operator's
// HNSW values into the cluster mapping JSON (regression guard for the wire-
// through, since defaults coincide with common values).
func TestBuildIndexMapping_ReflectsHNSWConfig(t *testing.T) {
	cfg, err := buildInternalCfg(&types.IndexConfig{
		HNSWM:              24,
		HNSWEFConstruction: 200,
		HNSWEFSearch:       128,
		KNNEngine:          "faiss",
	})
	if err != nil {
		t.Fatal(err)
	}
	body, err := buildIndexMapping(cfg, 768)
	if err != nil {
		t.Fatal(err)
	}
	s := string(body)
	for _, want := range []string{`"m":24`, `"ef_construction":200`, `"engine":"faiss"`, `"knn.algo_param.ef_search":128`} {
		if !strings.Contains(s, want) {
			t.Errorf("mapping JSON missing %q\n%s", want, s)
		}
	}
}

// TestWithAuditSink_SetsAndNilSafe verifies the functional option.
func TestWithAuditSink_SetsAndNilSafe(t *testing.T) {
	spy := &spySink{}
	r := &Repository{}
	WithAuditSink(spy)(r)
	if r.sink != spy {
		t.Fatal("WithAuditSink did not set the sink")
	}
	// nil must not clobber an already-set sink
	WithAuditSink(nil)(r)
	if r.sink != spy {
		t.Fatal("WithAuditSink(nil) clobbered the sink")
	}
}

// TestAuditSink_NopByDefault verifies a Repository with no sink does not panic
// when the audit accessor is used (nopSink fallback).
func TestAuditSink_NopByDefault(t *testing.T) {
	var _ AuditSink = nopSink{} // compile-time assertion
	r := &Repository{}          // sink left nil
	// Must not panic.
	r.auditSink().EmitIndexCreated(context.Background(), "weknora_768", 768)
	r.auditSink().EmitReindexExecuted(context.Background(), "a", "b", 3)
}

// TestAuditSink_EmitIndexCreated_OnEnsureReady verifies createIndexAndAlias
// emits exactly one index-created event with the per-dim alias when a new
// index is provisioned.
func TestAuditSink_EmitIndexCreated_OnEnsureReady(t *testing.T) {
	repo, ts := newTestRepo(t, (&indexLifecycleHandler{}).ServeHTTP)
	defer ts.Close()
	spy := &spySink{}
	repo.sink = spy

	if err := repo.ensureReady(context.Background(), 768); err != nil {
		t.Fatalf("ensureReady: %v", err)
	}
	if len(spy.indexCreated) != 1 {
		t.Fatalf("want 1 index_created event, got %d", len(spy.indexCreated))
	}
	got := spy.indexCreated[0]
	if got.alias != "weknora_test_768" || got.dim != 768 {
		t.Errorf("event mismatch: %+v", got)
	}
}
