package service

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
)

// A sync that fails thousands of documents must not accumulate an unbounded
// result.Errors slice: that slice is persisted as jsonb and shipped in every
// sync-log list response, so an uncapped list means multi-MB rows and payloads.
// The accurate count lives in result.Failed (a bounded int), so Errors only
// needs to retain a sample for display (Tencent/WeKnora#2136 / #1262).
func TestApplyFetchedItem_CapsErrorSampleButKeepsAccurateFailedCount(t *testing.T) {
	s := &DataSourceService{}
	ds := &types.DataSource{}
	result := &types.SyncResult{}
	ctx := context.Background()

	const failures = 5000
	for i := 0; i < failures; i++ {
		s.applyFetchedItem(ctx, ds, &types.FetchedItem{
			ExternalID: "node",
			Title:      "doc",
			Metadata:   map[string]string{"error": "export failed: rate limited"},
		}, nil, result)
	}

	assert.Equal(t, failures, result.Failed,
		"Failed must count every failure regardless of the error-sample cap")
	assert.LessOrEqual(t, len(result.Errors), maxSyncResultErrors,
		"Errors sample must be capped so the persisted jsonb stays bounded")
	assert.Equal(t, maxSyncResultErrors, len(result.Errors),
		"with more failures than the cap, the sample should be exactly the cap")
}

// The user-facing sample must be a structured SyncItemError carrying the
// connector's stable i18n code + params (so the frontend localises it), the
// title as a separate field, and no raw API body/log_id — that stays in logs.
func TestApplyFetchedItem_ProducesLocalisableStructuredError(t *testing.T) {
	s := &DataSourceService{}
	ds := &types.DataSource{}
	result := &types.SyncResult{}

	s.applyFetchedItem(context.Background(), ds, &types.FetchedItem{
		ExternalID: "nt1",
		Title:      "季度报告",
		Metadata: map[string]string{
			// raw, for logs only
			"error": `export 季度报告 (docx): feishu api error: status=500 body={"code":1663,"error":{"log_id":"20260"}}`,
			// classification, for a localisable UI sample
			"error_reason_code":       "feishu_api_error",
			"error_reason_code_value": "1663",
			"error_reason":            "Feishu API error (code=1663); will retry automatically",
		},
	}, nil, result)

	assert.Equal(t, 1, len(result.Errors))
	got := result.Errors[0]
	assert.Equal(t, "季度报告", got.Title, "title is a separate field, not embedded in the message")
	assert.Equal(t, "feishu_api_error", got.Code, "stable i18n code drives frontend localisation")
	assert.Equal(t, "1663", got.Params["code"], "feishu error code is passed as an interpolation param")
	assert.NotContains(t, got.Message, "body=", "raw API body must not reach the UI sample")
	assert.NotContains(t, got.Message, "log_id", "raw log_id must not reach the UI sample")
}
