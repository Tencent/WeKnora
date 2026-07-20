package journalrank

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestExtractPublication(t *testing.T) {
	publication, doi := ExtractPublication(map[string]string{"journal_name": "  Nature. "}, "doi: 10.1000/example")
	if publication != "Nature" || doi != "" {
		t.Fatalf("metadata extraction = %q, %q", publication, doi)
	}
	publication, doi = ExtractPublication(nil, "Journal: Journal of Testing\nDOI: 10.1234/test.1")
	if publication != "Journal of Testing" || doi != "10.1234/test.1" {
		t.Fatalf("text extraction = %q, %q", publication, doi)
	}
}

func TestExtractDocumentTitle(t *testing.T) {
	text := "# Supply chain digital twin design and implementation at scale\n\nAbstract"
	if got := ExtractDocumentTitle(nil, text); got != "Supply chain digital twin design and implementation at scale" {
		t.Fatalf("document title = %q", got)
	}
	if got := ExtractDocumentTitle(map[string]string{"article_title": "Metadata title"}, text); got != "Metadata title" {
		t.Fatalf("metadata title = %q", got)
	}
}

func TestClientEnrichAndCache(t *testing.T) {
	requests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if got := r.URL.Query().Get("secretKey"); got != "test-key" {
			t.Errorf("secretKey = %q", got)
		}
		if got := r.URL.Query().Get("publicationName"); got != "Nature" {
			t.Errorf("publicationName = %q", got)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"code": 200, "msg": "SUCCESS", "data": map[string]any{
				"officialRank": map[string]any{"all": map[string]string{"sci": "Q1"}, "select": map[string]string{"sci": "Q1"}},
				"customRank": map[string]any{
					"rankInfo": []map[string]string{{"uuid": "u1", "abbName": "Custom", "threeRankText": "B"}},
					"rank":     []string{"u1&&&3"},
				},
			},
		})
	}))
	defer srv.Close()

	c := NewClient()
	c.secretKey = "test-key"
	c.rankURL = srv.URL
	c.crossrefURL = srv.URL
	c.httpClient = srv.Client()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	first, reason, err := c.Enrich(ctx, map[string]string{"journal": "Nature"}, "")
	if err != nil || reason != "matched" || !first.Found || first.Official["sci"] != "Q1" || len(first.Custom) != 1 {
		t.Fatalf("first enrich = %+v, %q, %v", first, reason, err)
	}
	second, reason, err := c.Enrich(ctx, map[string]string{"journal": " nature "}, "")
	if err != nil || reason != "cache_hit" || second.Publication != "Nature" || requests != 1 {
		t.Fatalf("cached enrich = %+v, %q, %v, requests=%d", second, reason, err, requests)
	}
}

func TestClientEnrichNotConfigured(t *testing.T) {
	c := NewClient()
	c.secretKey = ""
	_, reason, err := c.Enrich(context.Background(), nil, "Journal: Nature")
	if err != ErrNotConfigured || reason != "not_configured" {
		t.Fatalf("not configured = %q, %v", reason, err)
	}
	if !strings.Contains(ErrPublicationMissing.Error(), "publication") {
		t.Fatal("missing publication error should be descriptive")
	}
}

func TestClientEnrichResolvesPublicationFromTitle(t *testing.T) {
	const title = "Supply chain digital twin design and implementation at scale"
	requests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		switch r.URL.Path {
		case "/works":
			if got := r.URL.Query().Get("query.title"); got != title {
				t.Errorf("query.title = %q", got)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"message": map[string]any{"items": []map[string]any{{
					"title":           []string{title},
					"container-title": []string{"International Journal of Production Research"},
				}}},
			})
		case "/rank":
			if got := r.URL.Query().Get("publicationName"); got != "International Journal of Production Research" {
				t.Errorf("publicationName = %q", got)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code": 200, "msg": "SUCCESS", "data": map[string]any{
					"officialRank": map[string]any{
						"all":    map[string]string{"sci": "Q1"},
						"select": map[string]string{"sci": "Q1"},
					},
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	c := NewClient()
	c.secretKey = "test-key"
	c.rankURL = srv.URL + "/rank"
	c.crossrefURL = srv.URL + "/works/"
	c.httpClient = srv.Client()
	result, reason, err := c.Enrich(context.Background(), nil, "# "+title)
	if err != nil || reason != "matched" || !result.Found || result.Publication != "International Journal of Production Research" {
		t.Fatalf("title enrich = %+v, %q, %v", result, reason, err)
	}
	if requests != 2 {
		t.Fatalf("requests = %d", requests)
	}
}

func TestTitleResolutionRejectsLowConfidenceMatch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"message": map[string]any{"items": []map[string]any{{
				"title":           []string{"A completely unrelated paper"},
				"container-title": []string{"Wrong Journal"},
			}}},
		})
	}))
	defer srv.Close()

	c := NewClient()
	c.crossrefURL = srv.URL
	c.httpClient = srv.Client()
	_, err := c.resolveTitle(context.Background(), "Supply chain digital twin design and implementation at scale")
	if err != ErrPublicationMissing {
		t.Fatalf("resolveTitle error = %v", err)
	}
}
