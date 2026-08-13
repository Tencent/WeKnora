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
	prefixCalls  []string           // recorded prefix arguments
	prefixReturn []*types.Knowledge // children to return from FindByMetadataKeyPrefix
}

func (r *sweepFakeRepo) FindByMetadataKey(ctx context.Context, tenantID uint64, kbID, key, value string) (*types.Knowledge, error) {
	return nil, nil // no existing main item → skip the case-1 update delete
}

func (r *sweepFakeRepo) FindByDataSourceExternalID(
	_ context.Context, _ uint64, _, _, _ string,
) (*types.Knowledge, error) {
	return nil, nil // no existing main item -> skip the case-1 update delete
}

func (r *sweepFakeRepo) FindByMetadataKeyPrefix(ctx context.Context, tenantID uint64, kbID, key, prefix string) ([]*types.Knowledge, error) {
	r.prefixCalls = append(r.prefixCalls, key+"|"+prefix)
	return r.prefixReturn, nil
}

type sweepFakeKS struct {
	interfaces.KnowledgeService
	repo               interfaces.KnowledgeRepository
	events             []string // ordered log of "delete:<id>" and "create:<fname>"
	deleted            []string
	createErr          error            // if set, CreateKnowledgeFromFile returns it after logging
	deleteErr          error            // if set, DeleteKnowledge returns it after logging
	createURLKnowledge *types.Knowledge // if set, CreateKnowledgeFromURL returns it
}

func (k *sweepFakeKS) GetRepository() interfaces.KnowledgeRepository { return k.repo }

func (k *sweepFakeKS) CreateKnowledgeFromURL(
	_ context.Context, _ string, _ string, _ string, _ string, _ *bool,
	_ string, _ []string, _ string, _ *types.KnowledgeProcessOverrides,
) (*types.Knowledge, error) {
	return k.createURLKnowledge, nil
}

func (k *sweepFakeKS) DeleteKnowledge(ctx context.Context, id string) error {
	k.events = append(k.events, "delete:"+id)
	k.deleted = append(k.deleted, id)
	return k.deleteErr
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
	return &types.Knowledge{ID: "new-knowledge"}, nil
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
		{ID: "stale-child-1"},
		{ID: "stale-child-2"},
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
	repo := &sweepFakeRepo{prefixReturn: []*types.Knowledge{{ID: "stale-child-1"}}}
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
// knowledge item (dedup keys on file_hash plus file_type), the parent row under this
// node's external_id no longer exists (it was deleted for the update and never
// recreated), so the subtree must NOT be swept — deleting the children would
// destroy them with no parent to replace them.
func TestIngestItem_NoSweepWhenDuplicateIsDifferentNode(t *testing.T) {
	repo := &sweepFakeRepo{prefixReturn: []*types.Knowledge{{ID: "would-be-orphaned-child"}}}
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
	if !errors.As(err, &dupErr) {
		t.Fatalf("want DuplicateKnowledgeError, got %v", err)
	}
	if len(ks.deleted) != 0 {
		t.Fatalf("dup against a different node must NOT sweep this node's children, deleted = %+v", ks.deleted)
	}
}

// childWithExternalID builds a stale-child Knowledge row carrying the external_id
// metadata the sweep reads to decide whether the child is still present.
func childWithExternalID(id, externalID string) *types.Knowledge {
	b, _ := json.Marshal(map[string]string{"external_id": externalID})
	return &types.Knowledge{ID: id, Metadata: types.JSON(b)}
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
