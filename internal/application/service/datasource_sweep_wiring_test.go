package service

import (
	"context"
	"encoding/json"
	"errors"
	"mime/multipart"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// ── interface-embedding fakes: only the methods ingestItem touches are
// implemented; everything else is nil (never called by this path). ──

type sweepFakeRepo struct {
	interfaces.KnowledgeRepository
	prefixCalls   []string           // recorded prefix arguments
	prefixReturn  []*types.Knowledge // children to return from FindByMetadataKeyPrefix
	findReturn    *types.Knowledge
	findAllReturn []*types.Knowledge
	findErr       error
	updated       []*types.Knowledge
}

func (r *sweepFakeRepo) FindByMetadataKey(ctx context.Context, tenantID uint64, kbID, key, value string) (*types.Knowledge, error) {
	return r.findReturn, r.findErr
}

func (r *sweepFakeRepo) FindAllByMetadataKeys(
	context.Context,
	uint64,
	string,
	map[string]string,
) ([]*types.Knowledge, error) {
	if r.findErr != nil {
		return nil, r.findErr
	}
	if r.findAllReturn != nil {
		return r.findAllReturn, nil
	}
	if r.findReturn == nil {
		return nil, nil
	}
	return []*types.Knowledge{r.findReturn}, nil
}

func (r *sweepFakeRepo) UpdateKnowledge(_ context.Context, knowledge *types.Knowledge) error {
	copied := *knowledge
	copied.Metadata = append(types.JSON(nil), knowledge.Metadata...)
	r.updated = append(r.updated, &copied)
	return nil
}

func (r *sweepFakeRepo) FindByMetadataKeyPrefix(ctx context.Context, tenantID uint64, kbID, key, prefix string) ([]*types.Knowledge, error) {
	r.prefixCalls = append(r.prefixCalls, key+"|"+prefix)
	return r.prefixReturn, nil
}

type sweepFakeKS struct {
	interfaces.KnowledgeService
	repo         *sweepFakeRepo
	events       []string // ordered log of "delete:<id>" and "create:<fname>"
	deleted      []string
	createErr    error // if set, CreateKnowledgeFromFile returns it after logging
	created      *types.Knowledge
	deleteErr    error
	deleteErrors []error
}

func (k *sweepFakeKS) GetRepository() interfaces.KnowledgeRepository { return k.repo }

func (k *sweepFakeKS) DeleteKnowledge(ctx context.Context, id string) error {
	k.events = append(k.events, "delete:"+id)
	if len(k.deleteErrors) > 0 {
		err := k.deleteErrors[0]
		k.deleteErrors = k.deleteErrors[1:]
		if err != nil {
			return err
		}
	}
	if k.deleteErr != nil {
		return k.deleteErr
	}
	k.deleted = append(k.deleted, id)
	return nil
}

// DeleteKnowledgeList is the batched delete the subtree sweep now uses; record
// each id in order so create-before-delete ordering is still asserted.
func (k *sweepFakeKS) DeleteKnowledgeList(ctx context.Context, ids []string) error {
	for _, id := range ids {
		k.events = append(k.events, "delete:"+id)
		k.deleted = append(k.deleted, id)
	}
	return nil
}

func (k *sweepFakeKS) CreateKnowledgeFromFile(
	ctx context.Context, kbID string, file *multipart.FileHeader, metadata map[string]string,
	enableMultimodel *bool, customFileName string, tagIDs []string, channel string,
	processOverrides *types.KnowledgeProcessOverrides,
) (*types.Knowledge, error) {
	k.events = append(k.events, "create:"+customFileName)
	if k.createErr != nil {
		return nil, k.createErr
	}
	if k.created != nil {
		if len(k.created.Metadata) == 0 {
			encoded, _ := json.Marshal(metadata)
			k.created.Metadata = types.JSON(encoded)
		}
		return k.created, nil
	}
	encoded, _ := json.Marshal(metadata)
	return &types.Knowledge{ID: "new-knowledge", Metadata: types.JSON(encoded)}, nil
}

// TestIngestItem_ReplacesSubtreeSweepsStaleChildrenAfterCreate verifies the
// orphan-cleanup wiring end-to-end at the service layer: when a re-synced parent
// item carries ReplacesSubtree, ingestItem must (1) query the child subtree by
// the "<externalID>#" prefix, (2) delete every stale child, and (3) do so only
// AFTER the parent content is written — so a failed/duplicate-skipped parent
// write never destroys existing children (the new children arrive as separate
// items later in the same sync, so the sweep still precedes their creation).
func TestIngestItem_ReplacesSubtreeSweepsStaleChildrenAfterCreate(t *testing.T) {
	repo := &sweepFakeRepo{prefixReturn: []*types.Knowledge{
		childWithExternalID("stale-child-1", "nt-parent#file#1"),
		childWithExternalID("stale-child-2", "nt-parent#file#2"),
	}}
	ks := &sweepFakeKS{repo: repo}
	s := &DataSourceService{knowledgeService: ks}

	ds := &types.DataSource{
		ID:              "ds-1",
		Type:            "feishu",
		TenantID:        7,
		KnowledgeBaseID: "kb-1",
	}
	item := &types.FetchedItem{
		ExternalID:      "nt-parent",
		Title:           "Parent Doc",
		Content:         []byte("# hello\n"),
		ContentType:     "text/markdown",
		FileName:        "parent.md",
		ReplacesSubtree: true,
	}

	if _, err := s.ingestItem(context.Background(), ds, item, nil); err != nil {
		t.Fatalf("ingestItem error: %v", err)
	}

	// (1) queried by the correct prefix
	if len(repo.prefixCalls) != 1 || repo.prefixCalls[0] != "external_id|nt-parent#" {
		t.Fatalf("prefix query = %+v, want [external_id|nt-parent#]", repo.prefixCalls)
	}

	// (2) every stale child deleted
	if len(ks.deleted) != 2 || ks.deleted[0] != "stale-child-1" || ks.deleted[1] != "stale-child-2" {
		t.Fatalf("deleted children = %+v, want [stale-child-1 stale-child-2]", ks.deleted)
	}

	// (3) the create precedes all deletes (a failed parent write never sweeps)
	wantOrder := []string{"create:parent.md", "delete:stale-child-1", "delete:stale-child-2"}
	if len(ks.events) != len(wantOrder) {
		t.Fatalf("events = %+v, want %+v", ks.events, wantOrder)
	}
	for i := range wantOrder {
		if ks.events[i] != wantOrder[i] {
			t.Fatalf("event[%d] = %q, want %q (full: %+v)", i, ks.events[i], wantOrder[i], ks.events)
		}
	}
}

// TestIngestItem_ReplacesSubtreeSweepsOnDuplicateParent verifies that when the
// parent body is a content-dedup hit (CreateKnowledgeFromFile returns a
// DuplicateKnowledgeError), the subtree is still swept: the parent effectively
// exists, so children removed from the doc must not linger. Regression guard for
// the "sweep runs only after a *fresh* create" gap.
func TestIngestItem_ReplacesSubtreeSweepsOnDuplicateParent(t *testing.T) {
	repo := &sweepFakeRepo{prefixReturn: []*types.Knowledge{
		childWithExternalID("stale-child-1", "nt-parent#file#1"),
	}}
	// The dedup hit is THIS node's own row (same external_id) — a genuine
	// self-dedup where the parent effectively exists, so the sweep must run.
	ks := &sweepFakeKS{
		repo:      repo,
		createErr: types.NewDuplicateFileError(childWithExternalID("existing-parent", "nt-parent")),
	}
	s := &DataSourceService{knowledgeService: ks}

	ds := &types.DataSource{ID: "ds-1", Type: "feishu", TenantID: 7, KnowledgeBaseID: "kb-1"}
	item := &types.FetchedItem{
		ExternalID:      "nt-parent",
		Content:         []byte("# hello\n"),
		FileName:        "parent.md",
		ReplacesSubtree: true,
	}

	// ingestItem surfaces the dup error to the caller (applyFetchedItem counts it
	// Skipped), but the sweep must still have run.
	_, err := s.ingestItem(context.Background(), ds, item, nil)
	var dupErr *types.DuplicateKnowledgeError
	if !errors.As(err, &dupErr) {
		t.Fatalf("want DuplicateKnowledgeError, got %v", err)
	}
	if len(ks.deleted) != 1 || ks.deleted[0] != "stale-child-1" {
		t.Fatalf("dup parent must still sweep stale children, deleted = %+v", ks.deleted)
	}
}

// TestIngestItem_NoSweepWhenDuplicateIsDifferentNode guards the data-loss path:
// when an updated node's rebuilt body content-hash-collides with a DIFFERENT
// knowledge item (dedup keys on file_hash plus file_type on this baseline), the
// replacement was not accepted, so the subtree must NOT be swept while the
// previous parent and children remain the last known-good version.
func TestIngestItem_NoSweepWhenDuplicateIsDifferentNode(t *testing.T) {
	repo := &sweepFakeRepo{prefixReturn: []*types.Knowledge{
		childWithExternalID("would-be-orphaned-child", "nt-parent#file#1"),
	}}
	// The dedup hit is a DIFFERENT node's row (external_id "nt-other"). A manual
	// upload with no external_id at all would behave identically (empty != ours).
	ks := &sweepFakeKS{
		repo:      repo,
		createErr: types.NewDuplicateFileError(childWithExternalID("some-other-doc", "nt-other")),
	}
	s := &DataSourceService{knowledgeService: ks}

	ds := &types.DataSource{ID: "ds-1", Type: "feishu", TenantID: 7, KnowledgeBaseID: "kb-1"}
	item := &types.FetchedItem{
		ExternalID:      "nt-parent",
		Content:         []byte("# hello\n"),
		FileName:        "parent.md",
		ReplacesSubtree: true,
	}

	_, err := s.ingestItem(context.Background(), ds, item, nil)
	var dupErr *types.DuplicateKnowledgeError
	if err == nil || errors.As(err, &dupErr) {
		t.Fatalf("foreign duplicate must be a retryable ingest failure, got %v", err)
	}
	if len(ks.deleted) != 0 {
		t.Fatalf("dup against a different node must NOT sweep this node's children, deleted = %+v", ks.deleted)
	}
}

func TestIngestItemCreateFailurePreservesPreviousKnowledge(t *testing.T) {
	previous := childWithExternalID("previous-knowledge", "doc-1")
	ks := &sweepFakeKS{
		repo:      &sweepFakeRepo{findReturn: previous},
		createErr: errors.New("storage unavailable"),
	}
	svc := &DataSourceService{knowledgeService: ks}
	ds := &types.DataSource{
		ID:              "ds-1",
		Type:            "dingtalk",
		TenantID:        7,
		KnowledgeBaseID: "kb-1",
	}

	isUpdate, err := svc.ingestItem(context.Background(), ds, &types.FetchedItem{
		ExternalID: "doc-1",
		Content:    []byte("replacement"),
		FileName:   "replacement.md",
	}, nil)

	if err == nil || !isUpdate {
		t.Fatalf("create failure isUpdate=%v err=%v", isUpdate, err)
	}
	if len(ks.deleted) != 0 {
		t.Fatalf("create failure deleted last known-good knowledge: %v", ks.deleted)
	}
	wantEvents := []string{"create:replacement.md"}
	if len(ks.events) != len(wantEvents) || ks.events[0] != wantEvents[0] {
		t.Fatalf("events = %v, want %v", ks.events, wantEvents)
	}
}

func TestIngestItemChangedContentDeletesPreviousOnlyAfterReplacementExists(t *testing.T) {
	previous := childWithExternalID("previous-knowledge", "doc-1")
	previous.ParseStatus = types.ParseStatusCompleted
	ks := &sweepFakeKS{
		repo:    &sweepFakeRepo{findReturn: previous},
		created: &types.Knowledge{ID: "completed-replacement", ParseStatus: types.ParseStatusCompleted},
	}
	svc := &DataSourceService{knowledgeService: ks}
	ds := &types.DataSource{
		ID:              "ds-1",
		Type:            "dingtalk",
		TenantID:        7,
		KnowledgeBaseID: "kb-1",
	}

	isUpdate, err := svc.ingestItem(context.Background(), ds, &types.FetchedItem{
		ExternalID: "doc-1",
		Content:    []byte("replacement"),
		FileName:   "replacement.md",
	}, nil)

	if err != nil || !isUpdate {
		t.Fatalf("changed content isUpdate=%v err=%v", isUpdate, err)
	}
	wantEvents := []string{"create:replacement.md", "delete:previous-knowledge"}
	if len(ks.events) != len(wantEvents) {
		t.Fatalf("events = %v, want %v", ks.events, wantEvents)
	}
	for i := range wantEvents {
		if ks.events[i] != wantEvents[i] {
			t.Fatalf("events = %v, want %v", ks.events, wantEvents)
		}
	}
}

func TestIngestItemPendingReplacementPreservesPreviousAndRetainsCursorSignal(t *testing.T) {
	previous := childWithExternalID("previous-knowledge", "doc-1")
	previous.ParseStatus = types.ParseStatusCompleted
	ks := &sweepFakeKS{
		repo:    &sweepFakeRepo{findReturn: previous},
		created: &types.Knowledge{ID: "pending-replacement", ParseStatus: types.ParseStatusPending},
	}
	svc := &DataSourceService{knowledgeService: ks}
	ds := &types.DataSource{
		ID:              "ds-1",
		Type:            types.ConnectorTypeDingTalk,
		TenantID:        7,
		KnowledgeBaseID: "kb-1",
	}
	item := &types.FetchedItem{
		ExternalID: "doc-1",
		Content:    []byte("replacement"),
		FileName:   "replacement.md",
		Metadata:   map[string]string{"revision": "rev-2"},
	}

	isUpdate, err := svc.ingestItem(context.Background(), ds, item, nil)

	if !isUpdate || !errors.Is(err, errDataSourceReplacementPending) {
		t.Fatalf("pending replacement isUpdate=%v err=%v", isUpdate, err)
	}
	if len(ks.deleted) != 0 {
		t.Fatalf("pending replacement deleted last known-good knowledge: %v", ks.deleted)
	}
	if got := ks.created.GetMetadata()[dataSourceReplacementOfMetadataKey]; got != previous.ID {
		t.Fatalf("replacement predecessor marker = %q, want %q", got, previous.ID)
	}
}

func TestIngestItemPromotesCompletedDeferredReplacementOnRetry(t *testing.T) {
	item := &types.FetchedItem{
		ExternalID: "doc-1",
		Content:    []byte("replacement"),
		FileName:   "replacement.md",
		Metadata:   map[string]string{"revision": "rev-2"},
	}
	previous := childWithExternalID("previous-knowledge", item.ExternalID)
	previous.ParseStatus = types.ParseStatusCompleted
	replacementMetadata := map[string]string{
		"external_id":                               item.ExternalID,
		"datasource_id":                             "ds-1",
		dataSourceReplacementOfMetadataKey:          previous.ID,
		dataSourceReplacementFingerprintMetadataKey: dataSourceReplacementFingerprint(item),
		dataSourceIngestPendingMetadataKey:          "true",
	}
	encoded, err := json.Marshal(replacementMetadata)
	if err != nil {
		t.Fatal(err)
	}
	replacement := &types.Knowledge{
		ID:          "completed-replacement",
		ParseStatus: types.ParseStatusCompleted,
		Metadata:    types.JSON(encoded),
	}
	repo := &sweepFakeRepo{findAllReturn: []*types.Knowledge{previous, replacement}}
	ks := &sweepFakeKS{repo: repo}
	svc := &DataSourceService{knowledgeService: ks}
	ds := &types.DataSource{
		ID:              "ds-1",
		Type:            types.ConnectorTypeDingTalk,
		TenantID:        7,
		KnowledgeBaseID: "kb-1",
	}

	isUpdate, err := svc.ingestItem(context.Background(), ds, item, nil)

	if err != nil || !isUpdate {
		t.Fatalf("promote completed replacement isUpdate=%v err=%v", isUpdate, err)
	}
	if len(ks.events) != 1 || ks.events[0] != "delete:previous-knowledge" {
		t.Fatalf("promotion events = %v", ks.events)
	}
	if len(repo.updated) != 1 {
		t.Fatalf("promoted metadata updates = %d, want 1", len(repo.updated))
	}
	promotedMetadata := repo.updated[0].GetMetadata()
	if promotedMetadata[dataSourceReplacementOfMetadataKey] != "" ||
		promotedMetadata[dataSourceReplacementFingerprintMetadataKey] != "" ||
		promotedMetadata[dataSourceIngestPendingMetadataKey] != "" {
		t.Fatalf("promotion markers were not cleared: %v", promotedMetadata)
	}
}

func TestIngestItemInitialPendingCreateIsNotAcknowledged(t *testing.T) {
	repo := &sweepFakeRepo{}
	ks := &sweepFakeKS{
		repo:    repo,
		created: &types.Knowledge{ID: "initial-pending", ParseStatus: types.ParseStatusPending},
	}
	svc := &DataSourceService{knowledgeService: ks}
	ds := &types.DataSource{
		ID:              "ds-1",
		Type:            types.ConnectorTypeDingTalk,
		TenantID:        7,
		KnowledgeBaseID: "kb-1",
	}
	item := &types.FetchedItem{
		ExternalID: "doc-new",
		Content:    []byte("first version"),
		FileName:   "first.md",
		Metadata:   map[string]string{"revision": "rev-1"},
	}

	isUpdate, err := svc.ingestItem(context.Background(), ds, item, nil)

	if isUpdate || !errors.Is(err, errDataSourceReplacementPending) {
		t.Fatalf("initial pending create isUpdate=%v err=%v", isUpdate, err)
	}
	createdMetadata := ks.created.GetMetadata()
	if createdMetadata[dataSourceIngestPendingMetadataKey] != "true" ||
		createdMetadata[dataSourceReplacementFingerprintMetadataKey] == "" ||
		createdMetadata[dataSourceReplacementOfMetadataKey] != "" {
		t.Fatalf("initial pending markers = %v", createdMetadata)
	}
}

func TestIngestItemInitialCompletedCreateIsAcknowledgedOnRetry(t *testing.T) {
	item := &types.FetchedItem{
		ExternalID: "doc-new",
		Content:    []byte("first version"),
		FileName:   "first.md",
		Metadata:   map[string]string{"revision": "rev-1"},
	}
	metadata, err := json.Marshal(map[string]string{
		"external_id":   item.ExternalID,
		"datasource_id": "ds-1",
		dataSourceReplacementFingerprintMetadataKey: dataSourceReplacementFingerprint(item),
		dataSourceIngestPendingMetadataKey:          "true",
	})
	if err != nil {
		t.Fatal(err)
	}
	completed := &types.Knowledge{
		ID:          "initial-completed",
		ParseStatus: types.ParseStatusCompleted,
		Metadata:    types.JSON(metadata),
	}
	repo := &sweepFakeRepo{findAllReturn: []*types.Knowledge{completed}}
	ks := &sweepFakeKS{repo: repo}
	svc := &DataSourceService{knowledgeService: ks}
	ds := &types.DataSource{
		ID:              "ds-1",
		Type:            types.ConnectorTypeDingTalk,
		TenantID:        7,
		KnowledgeBaseID: "kb-1",
	}

	isUpdate, err := svc.ingestItem(context.Background(), ds, item, nil)

	if err != nil || isUpdate {
		t.Fatalf("initial completion isUpdate=%v err=%v", isUpdate, err)
	}
	if len(ks.events) != 0 {
		t.Fatalf("initial promotion unexpectedly recreated/deleted rows: %v", ks.events)
	}
	if len(repo.updated) != 1 ||
		repo.updated[0].GetMetadata()[dataSourceIngestPendingMetadataKey] != "" {
		t.Fatalf("initial promotion did not clear pending marker: %+v", repo.updated)
	}
}

func TestIngestItemRetriesCleanupForDeletingReplacement(t *testing.T) {
	item := &types.FetchedItem{
		ExternalID: "doc-1",
		Content:    []byte("replacement"),
		FileName:   "replacement.md",
		Metadata:   map[string]string{"revision": "rev-2"},
	}
	previous := childWithExternalID("previous-knowledge", item.ExternalID)
	previous.ParseStatus = types.ParseStatusCompleted
	metadata, err := json.Marshal(map[string]string{
		"external_id":                               item.ExternalID,
		"datasource_id":                             "ds-1",
		dataSourceReplacementOfMetadataKey:          previous.ID,
		dataSourceReplacementFingerprintMetadataKey: dataSourceReplacementFingerprint(item),
		dataSourceIngestPendingMetadataKey:          "true",
	})
	if err != nil {
		t.Fatal(err)
	}
	candidate := &types.Knowledge{
		ID:          "failed-candidate",
		ParseStatus: types.ParseStatusFailed,
		Metadata:    types.JSON(metadata),
	}
	repo := &sweepFakeRepo{findAllReturn: []*types.Knowledge{previous, candidate}}
	ks := &sweepFakeKS{
		repo:         repo,
		created:      &types.Knowledge{ID: "retry-candidate", ParseStatus: types.ParseStatusPending},
		deleteErrors: []error{errors.New("vector cleanup unavailable"), nil},
	}
	svc := &DataSourceService{knowledgeService: ks}
	ds := &types.DataSource{
		ID:              "ds-1",
		Type:            types.ConnectorTypeDingTalk,
		TenantID:        7,
		KnowledgeBaseID: "kb-1",
	}

	if _, err := svc.ingestItem(context.Background(), ds, item, nil); err == nil {
		t.Fatal("first failed cleanup unexpectedly succeeded")
	}
	// Real DeleteKnowledge persists this state before its downstream cleanup can
	// fail; the next reconciliation must retry deletion instead of treating it
	// as an indefinitely in-flight parse.
	candidate.ParseStatus = types.ParseStatusDeleting

	_, err = svc.ingestItem(context.Background(), ds, item, nil)

	if !errors.Is(err, errDataSourceReplacementPending) {
		t.Fatalf("second reconcile error = %v, want new pending replacement", err)
	}
	if len(ks.events) < 3 ||
		ks.events[0] != "delete:failed-candidate" ||
		ks.events[1] != "delete:failed-candidate" ||
		ks.events[2] != "create:replacement.md" {
		t.Fatalf("deleting candidate was not retried before recreate: %v", ks.events)
	}
}

func TestIngestItemEnqueueFailurePreservesPreviousAndRemovesFailedReplacement(t *testing.T) {
	previous := childWithExternalID("previous-knowledge", "doc-1")
	ks := &sweepFakeKS{
		repo:    &sweepFakeRepo{findReturn: previous},
		created: &types.Knowledge{ID: "failed-replacement", ParseStatus: "failed"},
	}
	svc := &DataSourceService{knowledgeService: ks}
	ds := &types.DataSource{
		ID:              "ds-1",
		Type:            "dingtalk",
		TenantID:        7,
		KnowledgeBaseID: "kb-1",
	}

	isUpdate, err := svc.ingestItem(context.Background(), ds, &types.FetchedItem{
		ExternalID: "doc-1",
		Content:    []byte("replacement"),
		FileName:   "replacement.md",
	}, nil)

	if err == nil || !isUpdate {
		t.Fatalf("enqueue failure isUpdate=%v err=%v", isUpdate, err)
	}
	wantEvents := []string{"create:replacement.md", "delete:failed-replacement"}
	if len(ks.events) != len(wantEvents) {
		t.Fatalf("events = %v, want %v", ks.events, wantEvents)
	}
	for i := range wantEvents {
		if ks.events[i] != wantEvents[i] {
			t.Fatalf("events = %v, want %v", ks.events, wantEvents)
		}
	}
}

func TestIngestItemDingTalkSameBodyRenameAndMoveUpdatesOwnedRow(t *testing.T) {
	previous := childWithExternalID("existing-knowledge", "doc-1")
	previous.ParseStatus = types.ParseStatusCompleted
	previous.Title = "Old title"
	previous.FileName = "old.md"
	oldMetadata := previous.GetMetadata()
	oldMetadata["source_resource_id"] = "space-old"
	encoded, err := json.Marshal(oldMetadata)
	if err != nil {
		t.Fatal(err)
	}
	previous.Metadata = types.JSON(encoded)

	repo := &sweepFakeRepo{findReturn: previous}
	ks := &sweepFakeKS{
		repo:      repo,
		createErr: types.NewDuplicateFileError(previous),
	}
	svc := &DataSourceService{knowledgeService: ks}
	ds := &types.DataSource{
		ID:              "ds-1",
		Type:            types.ConnectorTypeDingTalk,
		TenantID:        7,
		KnowledgeBaseID: "kb-1",
	}
	item := &types.FetchedItem{
		ExternalID:       "doc-1",
		Title:            "Renamed title",
		Content:          []byte("unchanged body"),
		FileName:         "renamed.md",
		URL:              "https://alidocs.dingtalk.com/i/nodes/doc-1",
		SourceResourceID: "space-new",
		Metadata:         map[string]string{"revision": "rev-moved"},
	}

	isUpdate, err := svc.ingestItem(context.Background(), ds, item, nil)

	if err != nil || !isUpdate {
		t.Fatalf("same-body move isUpdate=%v err=%v", isUpdate, err)
	}
	if len(repo.updated) != 1 {
		t.Fatalf("metadata update calls = %d, want 1", len(repo.updated))
	}
	updated := repo.updated[0]
	assertMetadata := updated.GetMetadata()
	if updated.Title != item.Title ||
		updated.FileName != item.FileName ||
		updated.Source != item.URL ||
		assertMetadata["source_resource_id"] != item.SourceResourceID ||
		assertMetadata["revision"] != "rev-moved" {
		t.Fatalf("same-body rename/move not persisted: row=%+v metadata=%v", updated, assertMetadata)
	}
	if assertMetadata[dataSourceIngestPendingMetadataKey] != "" ||
		assertMetadata[dataSourceReplacementFingerprintMetadataKey] != "" ||
		assertMetadata[dataSourceReplacementOfMetadataKey] != "" {
		t.Fatalf("same-body update leaked control markers: %v", assertMetadata)
	}
}

// childWithExternalID builds a stale-child Knowledge row carrying the external_id
// metadata the sweep reads to decide whether the child is still present.
func childWithExternalID(id, externalID string) *types.Knowledge {
	b, _ := json.Marshal(map[string]string{
		"external_id":   externalID,
		"datasource_id": "ds-1",
	})
	return &types.Knowledge{ID: id, Metadata: types.JSON(b)}
}

func TestApplyFetchedItemDeletesOnlyCurrentDataSourceOwnership(t *testing.T) {
	ds := &types.DataSource{
		ID:              "ds-1",
		Type:            "dingtalk",
		TenantID:        7,
		KnowledgeBaseID: "kb-1",
		SyncDeletions:   true,
	}
	item := &types.FetchedItem{ExternalID: "same-upstream-id", IsDeleted: true}

	t.Run("owned row is deleted", func(t *testing.T) {
		repo := &sweepFakeRepo{
			findReturn: childWithExternalID("owned-knowledge", item.ExternalID),
		}
		ks := &sweepFakeKS{repo: repo}
		result := &types.SyncResult{}
		(&DataSourceService{knowledgeService: ks}).applyFetchedItem(
			context.Background(), ds, item, nil, result,
		)
		if result.Deleted != 1 || result.Failed != 0 || len(ks.deleted) != 1 {
			t.Fatalf("owned delete result=%+v deleted=%v", result, ks.deleted)
		}
	})

	t.Run("foreign row is never deleted", func(t *testing.T) {
		foreign := childWithExternalID("foreign-knowledge", item.ExternalID)
		meta := foreign.GetMetadata()
		meta["datasource_id"] = "ds-2"
		data, err := json.Marshal(meta)
		if err != nil {
			t.Fatal(err)
		}
		foreign.Metadata = types.JSON(data)
		ks := &sweepFakeKS{repo: &sweepFakeRepo{findReturn: foreign}}
		result := &types.SyncResult{}
		(&DataSourceService{knowledgeService: ks}).applyFetchedItem(
			context.Background(), ds, item, nil, result,
		)
		if result.Skipped != 1 || result.Deleted != 0 || len(ks.deleted) != 0 {
			t.Fatalf("foreign delete result=%+v deleted=%v", result, ks.deleted)
		}
	})

	t.Run("delete failure is visible and retried", func(t *testing.T) {
		ks := &sweepFakeKS{
			repo:      &sweepFakeRepo{findReturn: childWithExternalID("owned-knowledge", item.ExternalID)},
			deleteErr: errors.New("delete failed"),
		}
		result := &types.SyncResult{}
		(&DataSourceService{knowledgeService: ks}).applyFetchedItem(
			context.Background(), ds, item, nil, result,
		)
		if result.Failed != 1 || result.Deleted != 0 || len(result.Errors) != 1 {
			t.Fatalf("failed delete result=%+v", result)
		}
	})
}

func TestIngestItemDoesNotReplaceAnotherDataSourceWithSameExternalID(t *testing.T) {
	foreign := childWithExternalID("foreign-knowledge", "same-upstream-id")
	meta := foreign.GetMetadata()
	meta["datasource_id"] = "ds-2"
	data, err := json.Marshal(meta)
	if err != nil {
		t.Fatal(err)
	}
	foreign.Metadata = types.JSON(data)
	ks := &sweepFakeKS{
		repo:    &sweepFakeRepo{findReturn: foreign},
		created: &types.Knowledge{ID: "owned-new", ParseStatus: types.ParseStatusCompleted},
	}
	svc := &DataSourceService{knowledgeService: ks}
	ds := &types.DataSource{
		ID:              "ds-1",
		Type:            "dingtalk",
		TenantID:        7,
		KnowledgeBaseID: "kb-1",
	}
	_, err = svc.ingestItem(context.Background(), ds, &types.FetchedItem{
		ExternalID: "same-upstream-id",
		Content:    []byte("new content"),
		FileName:   "new.md",
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(ks.deleted) != 0 {
		t.Fatalf("foreign row was deleted: %v", ks.deleted)
	}
	if len(ks.events) != 1 || ks.events[0] != "create:new.md" {
		t.Fatalf("unexpected events: %v", ks.events)
	}
}

// TestIngestItem_SubtreeKeepPreservesPresentChild verifies the per-child sweep
// semantics: a child still present in the source (listed in SubtreeKeep) is
// preserved even though the parent carries ReplacesSubtree, while a child that
// vanished from the source is swept. This guards the data-loss fix where a
// still-present attachment that failed to re-ingest this cycle must keep its
// previously-synced good copy instead of being deleted with nothing to replace it.
func TestIngestItem_SubtreeKeepPreservesPresentChild(t *testing.T) {
	repo := &sweepFakeRepo{prefixReturn: []*types.Knowledge{
		childWithExternalID("child-present", "nt-parent#file#present"),
		childWithExternalID("child-gone", "nt-parent#file#gone"),
	}}
	ks := &sweepFakeKS{repo: repo}
	s := &DataSourceService{knowledgeService: ks}

	ds := &types.DataSource{ID: "ds-1", Type: "feishu", TenantID: 7, KnowledgeBaseID: "kb-1"}
	item := &types.FetchedItem{
		ExternalID:      "nt-parent",
		Content:         []byte("# hello\n"),
		FileName:        "parent.md",
		ReplacesSubtree: true,
		// "present" is still in the doc (kept even if it couldn't re-ingest);
		// "gone" was removed from the doc and must be swept.
		SubtreeKeep: []string{"nt-parent#file#present"},
	}

	if _, err := s.ingestItem(context.Background(), ds, item, nil); err != nil {
		t.Fatalf("ingestItem error: %v", err)
	}
	if len(ks.deleted) != 1 || ks.deleted[0] != "child-gone" {
		t.Fatalf("only the removed child must be swept, deleted = %+v (want [child-gone])", ks.deleted)
	}
}

// TestIngestItem_NoSweepWhenFlagUnset verifies a normal item (ReplacesSubtree
// false) never triggers the subtree prefix query — the sweep is opt-in.
func TestIngestItem_NoSweepWhenFlagUnset(t *testing.T) {
	repo := &sweepFakeRepo{}
	ks := &sweepFakeKS{repo: repo}
	s := &DataSourceService{knowledgeService: ks}

	ds := &types.DataSource{ID: "ds-1", Type: "feishu", TenantID: 7, KnowledgeBaseID: "kb-1"}
	item := &types.FetchedItem{
		ExternalID: "nt-plain",
		Content:    []byte("data"),
		FileName:   "plain.md",
		// ReplacesSubtree deliberately false
	}

	if _, err := s.ingestItem(context.Background(), ds, item, nil); err != nil {
		t.Fatalf("ingestItem error: %v", err)
	}
	if len(repo.prefixCalls) != 0 {
		t.Fatalf("no subtree query expected when ReplacesSubtree is false, got %+v", repo.prefixCalls)
	}
	if len(ks.deleted) != 0 {
		t.Fatalf("no deletes expected, got %+v", ks.deleted)
	}
}

// TestApplyFetchedItem_EmbeddedImageIngestFailureCountsAsSkip verifies that an
// embedded image extracted for OCR is best-effort: if the KB cannot ingest it
// (e.g. VLM/object-storage not configured for images), the failure is counted as
// Skipped, not Failed, so it never marks the whole document sync as failed. A
// non-image item with the same error must still count as Failed.
func TestApplyFetchedItem_EmbeddedImageIngestFailureCountsAsSkip(t *testing.T) {
	ingestErr := errors.New("上传图片文件需要设置VLM模型")
	ds := &types.DataSource{ID: "ds-1", Type: "feishu", TenantID: 7, KnowledgeBaseID: "kb-1"}
	newItem := func(extID string, meta map[string]string) *types.FetchedItem {
		return &types.FetchedItem{
			ExternalID:  extID,
			Title:       "img",
			Content:     []byte("\x89PNG\r\n\x1a\nxxxx"),
			ContentType: "image/png",
			FileName:    "image-x.png",
			Metadata:    meta,
		}
	}

	// Embedded image whose ingest fails → Skipped, not Failed.
	ksImg := &sweepFakeKS{repo: &sweepFakeRepo{}, createErr: ingestErr}
	sImg := &DataSourceService{knowledgeService: ksImg}
	resImg := &types.SyncResult{}
	sImg.applyFetchedItem(context.Background(), ds,
		newItem("nt#image#x", map[string]string{"embedded_image": "true"}), nil, resImg)
	if resImg.Skipped != 1 || resImg.Failed != 0 {
		t.Fatalf("embedded image failure: Skipped=%d Failed=%d, want Skipped=1 Failed=0",
			resImg.Skipped, resImg.Failed)
	}

	// Control: a non-image item with the same error → Failed.
	ksDoc := &sweepFakeKS{repo: &sweepFakeRepo{}, createErr: ingestErr}
	sDoc := &DataSourceService{knowledgeService: ksDoc}
	resDoc := &types.SyncResult{}
	sDoc.applyFetchedItem(context.Background(), ds, newItem("nt-doc", nil), nil, resDoc)
	if resDoc.Failed != 1 || resDoc.Skipped != 0 {
		t.Fatalf("non-image failure: Failed=%d Skipped=%d, want Failed=1 Skipped=0",
			resDoc.Failed, resDoc.Skipped)
	}
}
