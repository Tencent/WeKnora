package api

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// The batching layer splits on what the catalog states, and the catalog states
// nothing for most rerank vendors, so the settings themselves have to bound the
// request. Without this, "no ceiling documented" reaches SplitBatches as "no
// limits" and the whole candidate set travels in one request (#3559).
func TestRerankBatchLimitsBoundAnUndocumentedVendor(t *testing.T) {
	cases := []struct {
		name     string
		settings RerankSettings
		want     int
	}{
		{
			name: "nothing documented falls back to the default bound",
			want: DefaultRerankMaxDocuments,
		},
		{
			name:     "a documented document ceiling wins",
			settings: RerankSettings{MaxDocuments: 500},
			want:     500,
		},
		{
			// A request budget in characters cannot bound a count: 500
			// one-character documents fit inside 20000 characters, so the
			// item cap stays undocumented and the default applies beside it.
			name:     "a documented request budget does not lift the item cap",
			settings: RerankSettings{MaxRequestChars: 20000},
			want:     DefaultRerankMaxDocuments,
		},
		{
			// Same for a per-document ceiling: it bounds one item, not how
			// many of them travel together.
			name:     "a documented per-document ceiling does not lift the item cap",
			settings: RerankSettings{MaxDocumentChars: 4096},
			want:     DefaultRerankMaxDocuments,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, tc.settings.BatchLimits().MaxItems)
		})
	}
}
