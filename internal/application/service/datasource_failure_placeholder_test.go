package service

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
)

// A failure placeholder is recognised only when Metadata["error"] is set AND
// both Content and URL are empty (see docs/plugin-development-datasource.md
// §3.6). These tests pin that conjunction: loosening it to "the error key is
// present" would start reporting ordinary documents as failures, while
// tightening it would make placeholders silently regress to Skipped — the same
// silent-loss failure mode the documented contract exists to prevent.

func TestApplyFetchedItem_EmptyItemWithoutErrorMarkerIsSkipped(t *testing.T) {
	s := &DataSourceService{}
	result := &types.SyncResult{}

	s.applyFetchedItem(context.Background(), &types.DataSource{}, &types.FetchedItem{
		ExternalID: "empty-1",
		Title:      "Empty",
	}, nil, result)

	assert.Equal(t, 1, result.Skipped, "an empty item without an error marker is skipped, not failed")
	assert.Equal(t, 0, result.Failed)
	assert.Empty(t, result.Errors, "skipping is not a failure worth surfacing to the operator")
}

// Content wins over the error marker: the host trusts the payload and ingests
// the item. This is exactly why pluginapi.FailedItem always leaves Content and
// URL empty — filling either turns a reported failure into a stored document.
func TestApplyFetchedItem_ContentTakesPrecedenceOverErrorMarker(t *testing.T) {
	ks := &sweepFakeKS{repo: &sweepFakeRepo{}}
	s := &DataSourceService{knowledgeService: ks}
	result := &types.SyncResult{}

	s.applyFetchedItem(context.Background(),
		&types.DataSource{ID: "ds-1", TenantID: 1, KnowledgeBaseID: "kb-1"},
		&types.FetchedItem{
			ExternalID: "doc-1",
			Title:      "Doc",
			FileName:   "doc.md",
			Content:    []byte("real content"),
			Metadata:   map[string]string{"error": "stale marker on a hand-built item"},
		}, nil, result)

	assert.Equal(t, 0, result.Failed, "an item carrying content must not be counted as a failed fetch")
	assert.Equal(t, 1, result.Created, "it is ingested as a normal document instead")
}

// A placeholder must keep counting toward result.Failed, because that counter is
// the only thing gating cursor advancement in ProcessSync (the cursor moves only
// when Failed == 0). If a placeholder stopped incrementing it, the failed
// document would be skipped forever past an advanced cursor.
func TestApplyFetchedItem_FailurePlaceholderIncrementsFailedCounter(t *testing.T) {
	s := &DataSourceService{}
	result := &types.SyncResult{}

	s.applyFetchedItem(context.Background(), &types.DataSource{}, &types.FetchedItem{
		ExternalID: "broken-1",
		Title:      "Broken",
		Metadata:   map[string]string{"error": "export failed"},
	}, nil, result)

	assert.Equal(t, 1, result.Failed, "the placeholder must increment the counter that blocks cursor advancement")
	assert.Equal(t, 0, result.Skipped, "a reported failure is not a silent skip")
	assert.Len(t, result.Errors, 1, "the operator must see which document failed")
	assert.Equal(t, "Broken", result.Errors[0].Title)
}
