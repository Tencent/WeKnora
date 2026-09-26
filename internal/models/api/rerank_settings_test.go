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
			// A documented request budget is the vendor's own bound; the
			// default must not become a second ceiling on top of it.
			name:     "a documented request budget leaves the item cap unset",
			settings: RerankSettings{MaxRequestChars: 20000},
			want:     0,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, tc.settings.BatchLimits().MaxItems)
		})
	}
}
