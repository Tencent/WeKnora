package paperless

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/utils"
)

// TestMain whitelists loopback for httptest servers. Production keeps the
// default strict SSRF policy.
func TestMain(m *testing.M) {
	_ = os.Setenv("SSRF_WHITELIST", "127.0.0.1,::1")
	utils.ResetSSRFWhitelistForTest()
	os.Exit(m.Run())
}

func TestConnectorValidateSendsToken(t *testing.T) {
	server := paperlessServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/documents/" {
			t.Errorf("path = %s", r.URL.Path)
		}
		writeJSON(t, w, listResponse[document]{})
	})
	defer server.Close()

	if err := NewConnector().Validate(context.Background(), makeConfig(server.URL)); err != nil {
		t.Fatalf("Validate error: %v", err)
	}
}

func TestConnectorListResourcesListsMetadataFilters(t *testing.T) {
	server := paperlessServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/correspondents/":
			writeJSON(t, w, listResponse[correspondent]{Results: []correspondent{{ID: 7, Name: "ICAVI"}}})
		case "/api/document_types/":
			writeJSON(t, w, listResponse[documentType]{Results: []documentType{{ID: 8, Name: "Manual de operação"}}})
		case "/api/custom_fields/":
			writeJSON(t, w, listResponse[customField]{Results: []customField{{ID: 9, Name: "Cliente", DataType: "string"}}})
		default:
			t.Errorf("unexpected path %q", r.URL.Path)
		}
	})
	defer server.Close()

	resources, err := NewConnector().ListResources(context.Background(), makeConfig(server.URL), "")
	if err != nil {
		t.Fatalf("ListResources error: %v", err)
	}
	if len(resources) != 3 {
		t.Fatalf("resources len = %d", len(resources))
	}
	want := map[string]string{
		"correspondent:7": "paperless_correspondent",
		"document_type:8": "paperless_document_type",
		"custom_field:9":  "paperless_custom_field",
	}
	for _, resource := range resources {
		if expectedType, ok := want[resource.ExternalID]; !ok || resource.Type != expectedType {
			t.Fatalf("unexpected resource: %+v", resource)
		}
	}
}

func TestConnectorFetchAllFiltersMetadataAndMapsCustomFields(t *testing.T) {
	modified := time.Date(2026, 9, 25, 10, 0, 0, 0, time.UTC)
	server := paperlessServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/custom_fields/":
			writeJSON(t, w, listResponse[customField]{Results: []customField{{ID: 10, Name: "Cliente", DataType: "string"}}})
		case "/api/documents/":
			if got := r.URL.Query().Get("correspondent__id__in"); got != "7,8" {
				t.Errorf("correspondent__id__in = %q", got)
			}
			if got := r.URL.Query().Get("document_type__id__in"); got != "9" {
				t.Errorf("document_type__id__in = %q", got)
			}
			if got := r.URL.Query().Get("custom_field_query"); got != `["AND",[[10,"exact","Souza Cruz"],[11,"exact","Utilidades"]]]` {
				t.Errorf("custom_field_query = %q", got)
			}
			if got := r.URL.Query().Get("tags__id__all"); got != "" {
				t.Errorf("unexpected tag filter = %q", got)
			}
			writeJSON(t, w, listResponse[document]{Results: []document{{
				ID: 42, Title: "Invoice", Content: "OCR text", Modified: paperlessTime{Time: modified},
				OriginalFileName: "invoice.pdf", Correspondent: 7, DocumentType: 9,
				CustomFields: []customFieldValue{{Field: 10, Value: "Souza Cruz"}},
			}}})
		default:
			t.Errorf("unexpected path %q", r.URL.Path)
		}
	})
	defer server.Close()

	cfg := makeConfig(server.URL)
	cfg.Settings["custom_field_filters"] = []interface{}{
		map[string]interface{}{"field_id": 10, "operator": "exact", "value": "Souza Cruz"},
		map[string]interface{}{"field_id": 11, "operator": "exact", "value": "Utilidades"},
	}
	items, err := NewConnector().FetchAll(context.Background(), cfg, []string{"correspondent:7", "correspondent:8", "document_type:9"})
	if err != nil {
		t.Fatalf("FetchAll error: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("items len = %d", len(items))
	}
	item := items[0]
	if item.ExternalID != "paperless:42" || item.Title != "Invoice" || string(item.Content) != "OCR text" {
		t.Fatalf("unexpected item: %+v", item)
	}
	if item.ContentType != "text/plain" || item.FileName != "invoice.txt" {
		t.Fatalf("ContentType = %q, FileName = %q", item.ContentType, item.FileName)
	}
	if item.Metadata["custom_field_Cliente"] != "Souza Cruz" {
		t.Fatalf("custom field metadata = %v", item.Metadata)
	}
	if !strings.Contains(item.URL, "/documents/42/details") {
		t.Fatalf("URL = %s", item.URL)
	}
}

func TestConnectorFetchAllAcceptsDateOnlyCreatedValue(t *testing.T) {
	server := paperlessServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/documents/" {
			t.Errorf("path = %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"results":[{"id":44,"title":"Legacy document","content":"OCR text","original_file_name":"legacy.pdf","created":"2017-03-06","modified":"2026-09-25T10:00:00Z"}]}`))
	})
	defer server.Close()

	items, err := NewConnector().FetchAll(context.Background(), makeConfig(server.URL), []string{"all"})
	if err != nil {
		t.Fatalf("FetchAll error: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("items len = %d", len(items))
	}
	want := time.Date(2017, 3, 6, 0, 0, 0, 0, time.UTC)
	if !items[0].CreatedAt.Equal(want) {
		t.Fatalf("CreatedAt = %v, want %v", items[0].CreatedAt, want)
	}
}

func TestConnectorFetchAllFallsBackToOriginalWhenOCRIsEmpty(t *testing.T) {
	server := paperlessServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/documents/":
			writeJSON(t, w, listResponse[document]{Results: []document{{
				ID: 45, Title: "Scanned document", OriginalFileName: "scanned.pdf",
			}}})
		case "/api/documents/45/":
			writeJSON(t, w, document{ID: 45, Title: "Scanned document", OriginalFileName: "scanned.pdf"})
		case "/api/documents/45/download/":
			w.Header().Set("Content-Type", "application/pdf")
			_, _ = w.Write([]byte("%PDF-1.7 original"))
		default:
			t.Errorf("unexpected path %q", r.URL.Path)
		}
	})
	defer server.Close()

	items, err := NewConnector().FetchAll(context.Background(), makeConfig(server.URL), []string{"all"})
	if err != nil {
		t.Fatalf("FetchAll error: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("items len = %d", len(items))
	}
	item := items[0]
	if string(item.Content) != "%PDF-1.7 original" || item.ContentType != "application/pdf" || item.FileName != "scanned.pdf" {
		t.Fatalf("unexpected original-file fallback: %+v", item)
	}
}

func TestConnectorFetchIncrementalUsesModifiedCursor(t *testing.T) {
	lastSync := time.Date(2026, 9, 25, 9, 0, 0, 0, time.UTC)
	server := paperlessServer(t, func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("correspondent__id__in"); got != "7" {
			t.Errorf("correspondent__id__in = %q", got)
		}
		if got := r.URL.Query().Get("modified__gt"); got != lastSync.Format(time.RFC3339) {
			t.Errorf("modified__gt = %q", got)
		}
		writeJSON(t, w, listResponse[document]{Results: []document{{
			ID: 43, Title: "Updated", Content: "OCR text", OriginalFileName: "updated.pdf",
			Modified: paperlessTime{Time: lastSync.Add(time.Hour)},
		}}})
	})
	defer server.Close()

	cfg := makeConfig(server.URL)
	cfg.ResourceIDs = []string{"correspondent:7"}
	items, cursor, err := NewConnector().FetchIncremental(context.Background(), cfg, &types.SyncCursor{LastSyncTime: lastSync})
	if err != nil {
		t.Fatalf("FetchIncremental error: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("items len = %d", len(items))
	}
	if cursor == nil || !cursor.LastSyncTime.Equal(lastSync.Add(time.Hour)) {
		t.Fatalf("cursor = %+v", cursor)
	}
}

func TestClientFollowsPagination(t *testing.T) {
	var calls int
	server := paperlessServer(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			writeJSON(t, w, listResponse[document]{Next: "http://" + r.Host + "/api/documents/?page=2", Results: []document{{ID: 1}}})
			return
		}
		writeJSON(t, w, listResponse[document]{Results: []document{{ID: 2}}})
	})
	defer server.Close()

	docs, err := newClient(server.URL, "secret-token").listDocuments(context.Background(), nil)
	if err != nil {
		t.Fatalf("listDocuments error: %v", err)
	}
	if len(docs) != 2 || calls != 2 {
		t.Fatalf("docs=%+v calls=%d", docs, calls)
	}
}

func TestParseConfigRejectsInvalidCustomFieldFilter(t *testing.T) {
	cfg := makeConfig("https://paperless.example.com")
	cfg.Settings["custom_field_filters"] = []interface{}{
		map[string]interface{}{"field_id": 1, "operator": "drop_table", "value": "x"},
	}
	if _, err := parseConfig(cfg); err == nil || !strings.Contains(err.Error(), "unsupported custom field operator") {
		t.Fatalf("parseConfig error = %v, want unsupported operator error", err)
	}
}

func makeConfig(baseURL string) *types.DataSourceConfig {
	return &types.DataSourceConfig{
		Credentials: map[string]interface{}{"base_url": baseURL, "api_token": "secret-token"},
		Settings:    map[string]interface{}{},
	}
}

func paperlessServer(t *testing.T, handler http.HandlerFunc) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Token secret-token" {
			t.Errorf("Authorization = %q", r.Header.Get("Authorization"))
		}
		handler(w, r)
	}))
}

func writeJSON(t *testing.T, w http.ResponseWriter, value interface{}) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(value); err != nil {
		t.Errorf("encode response: %v", err)
	}
}
