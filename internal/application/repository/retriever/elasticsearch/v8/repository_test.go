package v8

import (
	"context"

	"io"
	"net/http"
	"net/http/httptest"
	"strings"

	"testing"

	"github.com/elastic/go-elasticsearch/v8"
	"github.com/stretchr/testify/require"
)

func newTestRepository(t *testing.T, handler http.HandlerFunc) (*elasticsearchRepository, *httptest.Server) {
	t.Helper()

	server := httptest.NewServer(handler)
	client, err := elasticsearch.NewTypedClient(elasticsearch.Config{
		Addresses: []string{server.URL},
	})
	require.NoError(t, err)

	return &elasticsearchRepository{
		client: client,
		index:  "test-index",
	}, server
}

func writeJSON(w http.ResponseWriter, body string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Elastic-Product", "Elasticsearch")
	_, _ = w.Write([]byte(body))
}

func TestDetectFieldTypesUsesFolderMappingIndependently(t *testing.T) {
	repository, server := newTestRepository(t, func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodGet, r.Method)
		require.Equal(t, "/test-index/_mapping", r.URL.Path)
		writeJSON(w, `{
			"test-index": {
				"mappings": {
					"properties": {
						"chunk_id": {"type": "keyword"},
						"folder_id": {
							"type": "text",
							"fields": {"keyword": {"type": "keyword"}}
						}
					}
				}
			}
		}`)
	})
	defer server.Close()

	require.NoError(t, repository.detectFieldTypes(context.Background()))

	require.Equal(t, "chunk_id", repository.idField("chunk_id"))
	require.Equal(t, "folder_id.keyword", repository.folderField())
	require.False(t, repository.folderIDMappingMissing)
}

func TestPrepareFolderMappingAddsMissingFolderKeyword(t *testing.T) {
	var mappingBody string
	folderMapped := false
	repository, server := newTestRepository(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			if folderMapped {
				writeJSON(w, `{
					"test-index": {
						"mappings": {
							"properties": {
								"chunk_id": {"type": "keyword"},
								"folder_id": {"type": "keyword"}
							}
						}
					}
				}`)
				return
			}
			writeJSON(w, `{
				"test-index": {
					"mappings": {
						"properties": {
							"chunk_id": {"type": "keyword"}
						}
					}
				}
			}`)
		case http.MethodPut:
			body, err := io.ReadAll(r.Body)
			require.NoError(t, err)
			mappingBody = string(body)
			folderMapped = true
			writeJSON(w, `{"acknowledged": true}`)
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	})
	defer server.Close()

	require.NoError(t, repository.prepareFolderMapping(context.Background()))

	require.Equal(t, "folder_id", repository.folderField())
	require.False(t, repository.folderIDMappingMissing)
	require.True(t, strings.Contains(mappingBody, `"folder_id"`), mappingBody)
	require.True(t, strings.Contains(mappingBody, `"type":"keyword"`), mappingBody)
}

func TestDetectFieldTypesRejectsNonExactFolderMapping(t *testing.T) {
	repository, server := newTestRepository(t, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, `{
			"test-index": {
				"mappings": {
					"properties": {
						"folder_id": {"type": "text"}
					}
				}
			}
		}`)
	})
	defer server.Close()

	err := repository.detectFieldTypes(context.Background())

	require.ErrorContains(t, err, "text field folder_id has no keyword multi-field")
}

func TestDetectFieldTypesFailsClosedWhenMappingRequestFails(t *testing.T) {
	repository, server := newTestRepository(t, func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	})
	defer server.Close()

	err := repository.detectFieldTypes(context.Background())

	require.ErrorContains(t, err, "get index mapping")
}
