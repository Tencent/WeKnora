package sandbox

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// AGS serves the catalog only through GET /v2/templates with a capitalized
// paginated envelope; the legacy bare array answers 404. ListTemplates must
// load the catalog through v2 (#5637).
func TestE2BRemoteClientListTemplatesPrefersV2Surface(t *testing.T) {
	var legacyHits, v2Hits int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v2/templates":
			v2Hits++
			writeJSON(w, http.StatusOK, map[string]any{
				"Templates": []map[string]any{{
					"TemplateID":  "sdt-ags-1",
					"BuildID":     "build-1",
					"Names":       []string{"project/weknora"},
					"Aliases":     []string{"weknora"},
					"BuildStatus": "ready",
				}},
				"NextToken": "",
			})
		case "/templates":
			legacyHits++
			http.NotFound(w, r)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	client, err := NewE2BRemoteClient(&Config{
		E2BAPIKey: "key-test", E2BAPIURL: server.URL, E2BSandboxTTL: time.Minute,
	})
	require.NoError(t, err)

	templates, err := client.ListTemplates(context.Background())
	require.NoError(t, err)
	require.Len(t, templates, 1)
	require.Equal(t, "sdt-ags-1", templates[0].ID)
	require.True(t, templates[0].Standard)
	require.Equal(t, 1, v2Hits)
	require.Zero(t, legacyHits)
}

// Pagination: AGS pages the envelope with NextToken; every page must be
// walked and the token threaded as nextToken.
func TestE2BRemoteClientListTemplatesFollowsV2Pagination(t *testing.T) {
	var requests []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.URL.RawQuery)
		switch r.URL.RawQuery {
		case "":
			writeJSON(w, http.StatusOK, map[string]any{
				"Templates": []map[string]any{{
					"TemplateID": "sdt-page-1", "BuildStatus": "ready",
				}},
				"NextToken": "page-2",
			})
		case "nextToken=page-2":
			writeJSON(w, http.StatusOK, map[string]any{
				"Templates": []map[string]any{{
					"TemplateID": "sdt-page-2", "BuildStatus": "ready",
				}},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	client, err := NewE2BRemoteClient(&Config{
		E2BAPIKey: "key-test", E2BAPIURL: server.URL, E2BSandboxTTL: time.Minute,
	})
	require.NoError(t, err)

	templates, err := client.ListTemplates(context.Background())
	require.NoError(t, err)
	require.Len(t, templates, 2)
	require.Equal(t, "sdt-page-1", templates[0].ID)
	require.Equal(t, "sdt-page-2", templates[1].ID)
	require.Equal(t, []string{"", "nextToken=page-2"}, requests)
}

// The cursor is opaque and in practice base64, so it has to travel through the
// query encoder: a raw concat would send "+" as a space and the provider would
// resolve a different (or no) page.
func TestE2BRemoteClientListTemplatesEncodesV2Token(t *testing.T) {
	var rawQueries []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rawQueries = append(rawQueries, r.URL.RawQuery)
		switch r.URL.Query().Get("nextToken") {
		case "":
			writeJSON(w, http.StatusOK, map[string]any{
				"Templates": []map[string]any{{
					"TemplateID": "sdt-page-1", "BuildStatus": "ready",
				}},
				"NextToken": "YWJj+/==",
			})
		case "YWJj+/==":
			writeJSON(w, http.StatusOK, map[string]any{
				"Templates": []map[string]any{{
					"TemplateID": "sdt-page-2", "BuildStatus": "ready",
				}},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	client, err := NewE2BRemoteClient(&Config{
		E2BAPIKey: "key-test", E2BAPIURL: server.URL, E2BSandboxTTL: time.Minute,
	})
	require.NoError(t, err)

	templates, err := client.ListTemplates(context.Background())
	require.NoError(t, err)
	require.Len(t, templates, 2)
	require.Equal(t, "sdt-page-2", templates[1].ID)
	require.Equal(t, []string{"", "nextToken=YWJj%2B%2F%3D%3D"}, rawQueries)
}

// A provider that keeps returning the same cursor must fail loudly instead of
// paging until the cap.
func TestE2BRemoteClientListTemplatesStopsOnRepeatedV2Token(t *testing.T) {
	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests++
		writeJSON(w, http.StatusOK, map[string]any{
			"Templates": []map[string]any{{
				"TemplateID": "sdt-loop", "BuildStatus": "ready",
			}},
			"NextToken": "stuck",
		})
	}))
	t.Cleanup(server.Close)

	client, err := NewE2BRemoteClient(&Config{
		E2BAPIKey: "key-test", E2BAPIURL: server.URL, E2BSandboxTTL: time.Minute,
	})
	require.NoError(t, err)

	_, err = client.ListTemplates(context.Background())
	require.Error(t, err)
	require.Contains(t, err.Error(), "repeated pagination token")
	require.Equal(t, 2, requests)
}

// A provider that still serves only the legacy bare array must keep working:
// v2 answers 404 and the SDK call takes over.
func TestE2BRemoteClientListTemplatesFallsBackToLegacyOnV2NotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v2/templates" {
			http.NotFound(w, r)
			return
		}
		if r.URL.Path == "/templates" {
			writeJSON(w, http.StatusOK, []map[string]any{{
				"templateID":  "tpl-legacy",
				"names":       []string{"project/weknora"},
				"buildStatus": "ready",
			}})
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(server.Close)

	client, err := NewE2BRemoteClient(&Config{
		E2BAPIKey: "key-test", E2BAPIURL: server.URL, E2BSandboxTTL: time.Minute,
	})
	require.NoError(t, err)

	templates, err := client.ListTemplates(context.Background())
	require.NoError(t, err)
	require.Len(t, templates, 1)
	require.Equal(t, "tpl-legacy", templates[0].ID)
	require.True(t, templates[0].Standard)
}

// The capitalized-member shim folds one rune, not one bit: "_id" and keys that
// start with a non-ASCII letter have to survive it unchanged/folded.
func TestLowerFirstFoldsOnlyTheFirstRune(t *testing.T) {
	require.Equal(t, "templateID", lowerFirst("TemplateID"))
	require.Equal(t, "_id", lowerFirst("_id"))
	require.Equal(t, "\u00e9cole", lowerFirst("\u00c9cole"))
}
