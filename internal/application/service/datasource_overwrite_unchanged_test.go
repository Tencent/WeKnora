package service

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// Regression tests for Tencent/WeKnora#4237: with the overwrite strategy,
// ingestItem used to delete + recreate an existing external_id on EVERY sync
// round without ever comparing content — each rebuild paying the full
// DeleteKnowledge cost (wiki cascade, embedding cleanup, chunk deletion),
// which serially stalls whole syncs. An unchanged, fully-parsed document must
// now be left untouched; anything not provably identical keeps rebuilding so
// sync stays self-healing.

// overwrite4237Repo returns a fixed "existing" knowledge and records deletes.
type overwrite4237Repo struct {
	interfaces.KnowledgeRepository
	existing  *types.Knowledge
	storedTag map[string][]*types.KnowledgeTag // knowledge ID → tags
	deleted   []string
}

func (r *overwrite4237Repo) FindByDataSourceExternalID(
	_ context.Context, _ uint64, _, _, _ string,
) (*types.Knowledge, error) {
	return r.existing, nil
}

func (r *overwrite4237Repo) HardDeleteKnowledge(_ context.Context, _ uint64, id string) error {
	r.deleted = append(r.deleted, id)
	return nil
}

func (r *overwrite4237Repo) GetKnowledgeTags(
	_ context.Context, ids []string,
) (map[string][]*types.KnowledgeTag, error) {
	out := make(map[string][]*types.KnowledgeTag, len(ids))
	for _, id := range ids {
		out[id] = r.storedTag[id]
	}
	return out, nil
}

func fileHashOf(b []byte) string {
	sum := md5.Sum(b)
	return hex.EncodeToString(sum[:])
}

func unchanged4237Knowledge() *types.Knowledge {
	return &types.Knowledge{
		ID:              "kb-entry-1",
		KnowledgeBaseID: "kb-1",
		FileName:        "doc.md",
		FileHash:        fileHashOf([]byte("# hello\n")),
		ParseStatus:     types.ParseStatusCompleted,
	}
}

func unchanged4237Item() *types.FetchedItem {
	return &types.FetchedItem{
		ExternalID:  "doc-1",
		Title:       "Same Doc",
		Content:     []byte("# hello\n"),
		ContentType: "text/markdown",
		FileName:    "doc.md",
	}
}

func new4237Service(
	existing *types.Knowledge, storedTag map[string][]*types.KnowledgeTag,
) (*DataSourceService, *overwrite4237Repo, *sweepFakeKS) {
	repo := &overwrite4237Repo{existing: existing, storedTag: storedTag}
	ks := &sweepFakeKS{repo: repo}
	return &DataSourceService{knowledgeService: ks}, repo, ks
}

func TestIngestItem_OverwriteSkipsUnchangedContent(t *testing.T) {
	svc, repo, ks := new4237Service(unchanged4237Knowledge(), nil)
	ds := &types.DataSource{ID: "ds-1", Type: "feishu", TenantID: 7, KnowledgeBaseID: "kb-1"}

	updated, unchanged, err := svc.ingestItem(context.Background(), ds, unchanged4237Item(), nil)
	if err != nil {
		t.Fatalf("ingestItem error: %v", err)
	}
	if !unchanged || updated {
		t.Fatalf("unchanged=%v updated=%v, want unchanged=true updated=false", unchanged, updated)
	}
	if len(repo.deleted) != 0 || len(ks.deleted) != 0 || len(ks.events) != 0 {
		t.Fatalf("unchanged doc must not be deleted or recreated: delete=%v hardDelete=%v events=%v",
			ks.deleted, repo.deleted, ks.events)
	}
}

func TestIngestItem_OverwriteRebuildsChangedContent(t *testing.T) {
	svc, repo, ks := new4237Service(unchanged4237Knowledge(), nil)
	ds := &types.DataSource{ID: "ds-1", Type: "feishu", TenantID: 7, KnowledgeBaseID: "kb-1"}

	item := unchanged4237Item()
	item.Content = []byte("# changed body\n")

	updated, unchanged, err := svc.ingestItem(context.Background(), ds, item, nil)
	if err != nil {
		t.Fatalf("ingestItem error: %v", err)
	}
	if unchanged || !updated {
		t.Fatalf("changed content must rebuild: unchanged=%v updated=%v", unchanged, updated)
	}
	if len(ks.deleted) != 1 || ks.deleted[0] != "kb-entry-1" {
		t.Fatalf("old knowledge must be deleted, got %v", ks.deleted)
	}
	if len(repo.deleted) != 1 {
		t.Fatalf("old knowledge must be hard-deleted, got %v", repo.deleted)
	}
	if len(ks.events) == 0 || ks.events[len(ks.events)-1] != "create:doc.md" {
		t.Fatalf("replacement must be created, got events %v", ks.events)
	}
}

func TestIngestItem_OverwriteRebuildsFailedParse(t *testing.T) {
	// A failed/stale row keeps flowing through delete+recreate: that rebuild
	// IS the self-healing retry (issue report shows parse_status=failed rows).
	existing := unchanged4237Knowledge()
	existing.ParseStatus = types.ParseStatusFailed
	svc, repo, ks := new4237Service(existing, nil)
	ds := &types.DataSource{ID: "ds-1", Type: "feishu", TenantID: 7, KnowledgeBaseID: "kb-1"}

	_, unchanged, err := svc.ingestItem(context.Background(), ds, unchanged4237Item(), nil)
	if err != nil {
		t.Fatalf("ingestItem error: %v", err)
	}
	if unchanged {
		t.Fatalf("failed parse must not be skipped")
	}
	if len(ks.deleted) != 1 || len(repo.deleted) != 1 {
		t.Fatalf("failed parse must be rebuilt, deletes=%v hardDeletes=%v", ks.deleted, repo.deleted)
	}
}

func TestIngestItem_OverwriteRebuildsOnRename(t *testing.T) {
	svc, _, ks := new4237Service(unchanged4237Knowledge(), nil)
	ds := &types.DataSource{ID: "ds-1", Type: "feishu", TenantID: 7, KnowledgeBaseID: "kb-1"}

	item := unchanged4237Item()
	item.FileName = "renamed.md"

	_, unchanged, err := svc.ingestItem(context.Background(), ds, item, nil)
	if err != nil {
		t.Fatalf("ingestItem error: %v", err)
	}
	if unchanged {
		t.Fatalf("rename must not be skipped")
	}
	if len(ks.deleted) != 1 {
		t.Fatalf("rename must rebuild, deletes=%v", ks.deleted)
	}
}

func TestIngestItem_OverwriteReactsToAutoTagDrift(t *testing.T) {
	existing := unchanged4237Knowledge()
	storedTag := map[string][]*types.KnowledgeTag{
		"kb-entry-1": {{ID: "tag-old"}},
	}
	ds := &types.DataSource{ID: "ds-1", Type: "feishu", TenantID: 7, KnowledgeBaseID: "kb-1"}

	// Drifted data-source auto-tag set → rebuild (recreate re-applies tags).
	svc, _, ks := new4237Service(existing, storedTag)
	_, unchanged, err := svc.ingestItem(context.Background(), ds, unchanged4237Item(), []string{"tag-new"})
	if err != nil {
		t.Fatalf("ingestItem error: %v", err)
	}
	if unchanged || len(ks.deleted) != 1 {
		t.Fatalf("tag drift must rebuild: unchanged=%v deletes=%v", unchanged, ks.deleted)
	}

	// Matching tag set → skip.
	var repo *overwrite4237Repo
	svc, repo, ks = new4237Service(unchanged4237Knowledge(), map[string][]*types.KnowledgeTag{
		"kb-entry-1": {{ID: "tag-new"}},
	})
	_, unchanged, err = svc.ingestItem(context.Background(), ds, unchanged4237Item(), []string{"tag-new"})
	if err != nil {
		t.Fatalf("ingestItem error: %v", err)
	}
	if !unchanged || len(ks.deleted) != 0 || len(repo.deleted) != 0 {
		t.Fatalf("matching tags must skip: unchanged=%v deletes=%v hardDeletes=%v",
			unchanged, ks.deleted, repo.deleted)
	}
}

func TestIngestItem_URLItemsKeepRebuildBehavior(t *testing.T) {
	// URL-only items carry no bytes to hash at ingest time; the unchanged
	// shortcut must not fire for them (conservative: rebuild as before).
	svc, _, ks := new4237Service(unchanged4237Knowledge(), nil)
	ds := &types.DataSource{ID: "ds-1", Type: "feishu", TenantID: 7, KnowledgeBaseID: "kb-1"}

	item := &types.FetchedItem{
		ExternalID: "doc-1",
		URL:        "https://example.com/doc.md",
		FileName:   "doc.md",
	}
	updated, unchanged, err := svc.ingestItem(context.Background(), ds, item, nil)
	if err != nil {
		t.Fatalf("ingestItem error: %v", err)
	}
	if unchanged {
		t.Fatalf("URL items must not take the unchanged shortcut")
	}
	if !updated || len(ks.deleted) != 1 {
		t.Fatalf("URL item must go through delete+recreate: updated=%v deletes=%v", updated, ks.deleted)
	}
}
