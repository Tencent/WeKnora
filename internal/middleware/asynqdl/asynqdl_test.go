package asynqdl

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/hibiken/asynq"
)

// fakeRepo records calls to Insert in memory so tests can assert how
// many dead-letter rows would have been written. It implements only
// the subset of TaskDeadLetterRepository the middleware uses; the
// Lister methods are unused in the middleware path.
type fakeRepo struct {
	mu   sync.Mutex
	rows []*types.TaskDeadLetter
	fail error
}

func (f *fakeRepo) Insert(ctx context.Context, dl *types.TaskDeadLetter) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail != nil {
		return f.fail
	}
	cp := *dl // defensive copy: callers may reuse the struct
	f.rows = append(f.rows, &cp)
	return nil
}

func (f *fakeRepo) ListByScope(context.Context, string, string, string, int) ([]*types.TaskDeadLetter, string, error) {
	return nil, "", nil
}

func (f *fakeRepo) ListByTaskType(context.Context, string, string, int) ([]*types.TaskDeadLetter, string, error) {
	return nil, "", nil
}

func (f *fakeRepo) DeleteByID(context.Context, int64) error { return nil }

// captureRow returns the i'th row safely under the lock.
func (f *fakeRepo) captureRow(i int) *types.TaskDeadLetter {
	f.mu.Lock()
	defer f.mu.Unlock()
	if i >= len(f.rows) {
		return nil
	}
	return f.rows[i]
}

func (f *fakeRepo) rowCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.rows)
}

// runMiddleware wires up the middleware around a handler that always
// returns handlerErr. We invoke it directly (no asynq server) so retry
// counters come from the helper-injected ctx values; isFinalAttempt
// treats a missing pair as "final" for testability.
func runMiddleware(repo *fakeRepo, taskType string, payload []byte, handlerErr error) error {
	mw := Middleware(repo)
	wrapped := mw(asynq.HandlerFunc(func(_ context.Context, _ *asynq.Task) error {
		return handlerErr
	}))
	t := asynq.NewTask(taskType, payload)
	return wrapped.ProcessTask(context.Background(), t)
}

func TestMiddleware_NilHandlerErr_NoInsert(t *testing.T) {
	repo := &fakeRepo{}
	if err := runMiddleware(repo, "any:task", []byte(`{"tenant_id":1}`), nil); err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if got := repo.rowCount(); got != 0 {
		t.Fatalf("expected zero inserts on success, got %d", got)
	}
}

func TestMiddleware_FailureWithoutAsynqCtx_RecordsRow(t *testing.T) {
	repo := &fakeRepo{}
	payload, _ := json.Marshal(map[string]any{
		"tenant_id":         42,
		"knowledge_base_id": "kb-abc",
		"knowledge_id":      "k-1",
	})
	wantErr := errors.New("boom")
	gotErr := runMiddleware(repo, "wiki:ingest", payload, wantErr)
	if !errors.Is(gotErr, wantErr) {
		t.Fatalf("expected the original task error to propagate, got %v", gotErr)
	}
	if got := repo.rowCount(); got != 1 {
		t.Fatalf("expected exactly one insert, got %d", got)
	}
	row := repo.captureRow(0)
	if row.TaskType != "wiki:ingest" {
		t.Errorf("task_type: got %q", row.TaskType)
	}
	if row.TenantID != 42 {
		t.Errorf("tenant_id: got %d", row.TenantID)
	}
	if row.Scope != types.TaskScopeKnowledgeBase || row.ScopeID != "kb-abc" {
		t.Errorf("scope tuple: got (%q, %q)", row.Scope, row.ScopeID)
	}
	if row.RelatedID != "k-1" {
		t.Errorf("related_id: got %q", row.RelatedID)
	}
	if row.LastError != "boom" {
		t.Errorf("last_error: got %q", row.LastError)
	}
	// Outside an asynq worker ctx GetRetryCount returns !ok, so the
	// middleware records 0 attempts (clearer than a misleading 1).
	if row.FailCount != 0 {
		t.Errorf("fail_count: expected 0 outside worker ctx, got %d", row.FailCount)
	}
}

func TestMiddleware_UnknownPayload_StillRecords(t *testing.T) {
	repo := &fakeRepo{}
	// Payload that doesn't carry any of the well-known identifier keys.
	payload := []byte(`{"some_unrelated":"value"}`)
	_ = runMiddleware(repo, "future:weird", payload, errors.New("nope"))
	if got := repo.rowCount(); got != 1 {
		t.Fatalf("expected one insert for unknown payload, got %d", got)
	}
	row := repo.captureRow(0)
	if row.Scope != types.TaskScopeUnknown {
		t.Errorf("expected scope=unknown for unparseable payload, got %q", row.Scope)
	}
	if row.ScopeID != "" {
		t.Errorf("expected empty scope_id for unknown scope, got %q", row.ScopeID)
	}
	if string(row.Payload) != string(payload) {
		t.Errorf("expected payload preserved verbatim, got %s", string(row.Payload))
	}
}

func TestMiddleware_NilPayload_DefaultsToEmptyObject(t *testing.T) {
	repo := &fakeRepo{}
	_ = runMiddleware(repo, "any:task", nil, errors.New("err"))
	row := repo.captureRow(0)
	if row == nil {
		t.Fatal("expected a row")
	}
	if string(row.Payload) != "{}" {
		t.Errorf("expected payload normalized to {}, got %s", string(row.Payload))
	}
}

func TestMiddleware_LongErrorTruncated(t *testing.T) {
	repo := &fakeRepo{}
	long := make([]byte, 16384)
	for i := range long {
		long[i] = 'x'
	}
	_ = runMiddleware(repo, "any", []byte("{}"), errors.New(string(long)))
	row := repo.captureRow(0)
	if got := len(row.LastError); got > 8192 {
		t.Errorf("expected last_error truncated to <=8192 bytes, got %d", got)
	}
	const suffix = "...(truncated)"
	if got := row.LastError[len(row.LastError)-len(suffix):]; got != suffix {
		t.Errorf("expected truncation suffix %q, got %q", suffix, got)
	}
}

func TestMiddleware_RepoFailure_DoesNotMaskTaskError(t *testing.T) {
	repo := &fakeRepo{fail: errors.New("db down")}
	wantErr := errors.New("task boom")
	gotErr := runMiddleware(repo, "any", []byte(`{"tenant_id":1}`), wantErr)
	if !errors.Is(gotErr, wantErr) {
		t.Fatalf("repo failure must not mask task error; got %v", gotErr)
	}
}

func TestInferScope_Priority(t *testing.T) {
	tests := []struct {
		name      string
		probe     payloadProbe
		wantScope string
		wantID    string
	}{
		{
			name:      "knowledge_base_id wins over knowledge_id",
			probe:     payloadProbe{KnowledgeBaseID: "kb1", KnowledgeID: "k1"},
			wantScope: types.TaskScopeKnowledgeBase,
			wantID:    "kb1",
		},
		{
			name:      "kb_id alias resolves to knowledge_base scope",
			probe:     payloadProbe{KBID: "kb-faq"},
			wantScope: types.TaskScopeKnowledgeBase,
			wantID:    "kb-faq",
		},
		{
			name:      "source_kb_id from KnowledgeMovePayload",
			probe:     payloadProbe{SourceKBID: "src-kb"},
			wantScope: types.TaskScopeKnowledgeBase,
			wantID:    "src-kb",
		},
		{
			name:      "knowledge_id only",
			probe:     payloadProbe{KnowledgeID: "k-only"},
			wantScope: types.TaskScopeKnowledge,
			wantID:    "k-only",
		},
		{
			name:      "tenant_id fallback",
			probe:     payloadProbe{TenantID: 7},
			wantScope: types.TaskScopeTenant,
			wantID:    "7",
		},
		{
			name:      "no identifiers — unknown scope",
			probe:     payloadProbe{},
			wantScope: types.TaskScopeUnknown,
			wantID:    "",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gotScope, gotID := inferScope(tc.probe)
			if gotScope != tc.wantScope || gotID != tc.wantID {
				t.Errorf("inferScope: got (%q, %q), want (%q, %q)",
					gotScope, gotID, tc.wantScope, tc.wantID)
			}
		})
	}
}
