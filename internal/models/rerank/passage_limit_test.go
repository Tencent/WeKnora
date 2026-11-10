package rerank

import (
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/models/api"
	"github.com/stretchr/testify/assert"
)

func TestMaxPassageRunesFromSettings(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		settings api.RerankSettings
		query    string
		want     int
	}{
		{"no limits", api.RerankSettings{}, "query", 0},
		{"per document", api.RerankSettings{MaxDocumentChars: 4096}, "query", 4096},
		{"request budget minus query", api.RerankSettings{MaxRequestChars: 2000}, "查询内容", 1996},
		{"tighter of the two", api.RerankSettings{MaxDocumentChars: 500, MaxRequestChars: 2000}, "q", 500},
		{"request budget tighter", api.RerankSettings{MaxDocumentChars: 4096, MaxRequestChars: 2000}, "q", 1999},
		{"query fills the budget", api.RerankSettings{MaxRequestChars: 3}, "long query", 1},
	}
	for _, tc := range cases {
		r := newWrapped(&fakeProtocol{}, tc.settings)
		assert.Equal(t, tc.want, r.MaxPassageRunes(tc.query), tc.name)
		// Wrappers must not hide the limit.
		for _, wrapped := range []Reranker{&debugReranker{inner: r}, &langfuseReranker{inner: r}} {
			assert.Equal(t, tc.want, MaxPassageRunes(wrapped, tc.query), tc.name+" via %T", wrapped)
		}
	}
}

func TestRerankHTTPClientHasDefaultTimeout(t *testing.T) {
	t.Parallel()
	assert.Equal(t, defaultRerankRequestTimeout, newRerankHTTPClient(0).Timeout)
	assert.Equal(t, 5*time.Second, newRerankHTTPClient(5*time.Second).Timeout)
}
