package pluginapi

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The host only classifies an item as a per-item failure when Metadata["error"]
// is set AND both Content and URL are empty. FailedItem must therefore leave
// Content and URL untouched: an author who fills either by hand would have the
// placeholder silently ingested as a normal document instead of reported.
func TestFailedItem_LeavesContentAndURLEmpty(t *testing.T) {
	item := FailedItem("file:docs/broken.md", "Broken", "export failed")

	assert.Equal(t, "file:docs/broken.md", item.ExternalID)
	assert.Equal(t, "Broken", item.Title)
	assert.Equal(t, "export failed", item.Metadata[MetadataKeyError])
	assert.Empty(t, item.Content, "content must stay empty or the host ingests the placeholder")
	assert.Empty(t, item.URL, "url must stay empty or the host ingests the placeholder")
	assert.False(t, item.IsDeleted, "a failed fetch is not a deletion")
}

// A structured reason lets the host record a localisable code + param instead of
// the raw upstream message (mirrors fetchFailureSyncError on the host side).
func TestFailedItemWithReason_CarriesStructuredReason(t *testing.T) {
	item := FailedItemWithReason("nt1", "季度报告", "raw api error", "feishu_api_error", "1663")

	require.NotNil(t, item.Metadata)
	assert.Equal(t, "raw api error", item.Metadata[MetadataKeyError])
	assert.Equal(t, "feishu_api_error", item.Metadata[MetadataKeyErrorReasonCode])
	assert.Equal(t, "1663", item.Metadata[MetadataKeyErrorReasonCodeValue])
	assert.Equal(t, "raw api error", item.Metadata[MetadataKeyErrorReason],
		"error_reason is the fallback message when the frontend lacks the code")
	assert.Empty(t, item.Content)
	assert.Empty(t, item.URL)
}

// Blank optional fields must be omitted rather than stored as empty strings, so
// the host does not treat the item as structured-but-messageless.
func TestFailedItemWithReason_OmitsBlankOptionals(t *testing.T) {
	item := FailedItemWithReason("x", "t", "boom", "", "")

	_, hasCode := item.Metadata[MetadataKeyErrorReasonCode]
	_, hasValue := item.Metadata[MetadataKeyErrorReasonCodeValue]
	assert.False(t, hasCode, "blank reason code must not be stored")
	assert.False(t, hasValue, "blank reason value must not be stored")
	assert.Equal(t, "boom", item.Metadata[MetadataKeyError])
}
