package dingtalk

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/datasource"
	"github.com/Tencent/WeKnora/internal/types"
)

func testDataSourceConfig(resourceIDs ...string) *types.DataSourceConfig {
	return &types.DataSourceConfig{
		Type: types.ConnectorTypeDingTalk,
		Credentials: map[string]interface{}{
			"client_id":         "ding-app-key",
			"client_secret":     "app-secret",
			"operator_union_id": "union-operator",
		},
		ResourceIDs: resourceIDs,
	}
}

func connectorForServer(server *httptest.Server) *Connector {
	connector := NewConnector()
	connector.newClient = func(cfg *Config) *client {
		copyOfConfig := *cfg
		copyOfConfig.baseURL = server.URL
		cli := newClient(&copyOfConfig)
		cli.httpClient = server.Client()
		cli.sleep = func(context.Context, time.Duration) error { return nil }
		return cli
	}
	return connector
}

func addDefaultTokenRoute(t *testing.T, mux *http.ServeMux) {
	mux.HandleFunc("/v1.0/oauth2/accessToken", func(writer http.ResponseWriter, _ *http.Request) {
		writeJSON(t, writer, http.StatusOK, accessTokenResponse{AccessToken: "access-token", ExpireIn: 7200})
	})
}

func blockResponse(t *testing.T, writer http.ResponseWriter, text string) {
	response := blocksResponse{Success: true}
	response.Result.Data = []json.RawMessage{
		json.RawMessage(`{"blockType":"paragraph","children":[{"text":` + mustJSONString(t, text) + `}]}`),
	}
	writeJSON(t, writer, http.StatusOK, response)
}

func mustJSONString(t *testing.T, value string) string {
	t.Helper()
	b, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal test string: %v", err)
	}
	return string(b)
}

func TestConnectorValidateAndListResources(t *testing.T) {
	mux := http.NewServeMux()
	addDefaultTokenRoute(t, mux)
	mux.HandleFunc("/v2.0/doc/relations/spaces", func(writer http.ResponseWriter, _ *http.Request) {
		writeJSON(t, writer, http.StatusOK, relatedSpacesResponse{Items: []dingtalkSpace{
			{ID: "space-z", Name: "Zeta", Description: "Z", URL: "https://alidocs.dingtalk.com/i/spaces/z"},
			{ID: "space-a", Name: "Alpha", Description: "A", URL: "https://alidocs.dingtalk.com/i/spaces/a"},
		}})
	})
	server := httptest.NewServer(mux)
	defer server.Close()
	connector := connectorForServer(server)
	config := testDataSourceConfig()

	if err := connector.Validate(context.Background(), config); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	resources, err := connector.ListResources(context.Background(), config, "")
	if err != nil {
		t.Fatalf("ListResources: %v", err)
	}
	if len(resources) != 2 || resources[0].ExternalID != "space-a" || resources[1].ExternalID != "space-z" {
		t.Fatalf("resources = %#v", resources)
	}
	if resources[0].Type != "wiki_space" {
		t.Errorf("resource type = %q", resources[0].Type)
	}
	children, err := connector.ListResources(context.Background(), config, "space-a")
	if err != nil || len(children) != 0 {
		t.Fatalf("nested ListResources = %#v, %v", children, err)
	}
}

func TestConnectorFetchAllWalksFoldersAndFiltersNonDocs(t *testing.T) {
	mux := http.NewServeMux()
	addDefaultTokenRoute(t, mux)
	mux.HandleFunc("/v2.0/doc/spaces/space-1/directories", func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Query().Get("dentryId") == "folder-1" {
			writeJSON(t, writer, http.StatusOK, directoriesResponse{Children: []dentry{
				{DentryUUID: "doc-child", ContentType: "alidoc", Name: "Child/Doc", UpdatedTime: 200, URL: "https://alidocs.dingtalk.com/i/nodes/doc-child"},
			}})
			return
		}
		writeJSON(t, writer, http.StatusOK, directoriesResponse{Children: []dentry{
			{DentryID: "folder-1", DentryType: "folder", HasChildren: true, Name: "Folder"},
			{DentryUUID: "doc-root", DocKey: "doc-root-key", Extension: "alidoc", Name: "Root Doc", UpdatedTime: 100},
			{DentryUUID: "sheet-1", ContentType: "ALIDOC", Extension: "axls", Name: "Sheet"},
		}})
	})
	mux.HandleFunc("/v1.0/doc/suites/documents/doc-root-key/blocks", func(writer http.ResponseWriter, _ *http.Request) {
		blockResponse(t, writer, "root content")
	})
	mux.HandleFunc("/v1.0/doc/suites/documents/doc-child/blocks", func(writer http.ResponseWriter, _ *http.Request) {
		blockResponse(t, writer, "child content")
	})
	mux.HandleFunc("/v1.0/doc/suites/documents/sheet-1/blocks", func(writer http.ResponseWriter, _ *http.Request) {
		t.Error("spreadsheet should not be fetched")
		writer.WriteHeader(http.StatusInternalServerError)
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	items, err := connectorForServer(server).FetchAll(context.Background(), testDataSourceConfig(), []string{"space-1"})
	if err != nil {
		t.Fatalf("FetchAll: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("items = %#v", items)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].ExternalID < items[j].ExternalID })
	if items[0].ExternalID != "doc-child" || string(items[0].Content) != "child content" {
		t.Errorf("child item = %#v", items[0])
	}
	if items[0].FileName != "Child_Doc.md" || items[0].Metadata["channel"] != types.ChannelDingtalk {
		t.Errorf("child filename/metadata = %q %#v", items[0].FileName, items[0].Metadata)
	}
	if items[1].ExternalID != "doc-root" || string(items[1].Content) != "root content" {
		t.Errorf("root item = %#v", items[1])
	}
}

func TestConnectorFetchIncrementalChangedNewAndDeleted(t *testing.T) {
	mux := http.NewServeMux()
	addDefaultTokenRoute(t, mux)
	mux.HandleFunc("/v2.0/doc/spaces/space-1/directories", func(writer http.ResponseWriter, _ *http.Request) {
		writeJSON(t, writer, http.StatusOK, directoriesResponse{Children: []dentry{
			{DentryUUID: "unchanged", ContentType: "alidoc", Name: "Unchanged", UpdatedTime: 100},
			{DentryUUID: "changed", ContentType: "alidoc", Name: "Changed", UpdatedTime: 300},
			{DentryUUID: "new", ContentType: "alidoc", Name: "New", UpdatedTime: 400},
		}})
	})
	var fetchedDocuments []string
	for _, documentID := range []string{"changed", "new"} {
		documentID := documentID
		mux.HandleFunc("/v1.0/doc/suites/documents/"+documentID+"/blocks", func(writer http.ResponseWriter, _ *http.Request) {
			fetchedDocuments = append(fetchedDocuments, documentID)
			blockResponse(t, writer, documentID+" content")
		})
	}
	mux.HandleFunc("/v1.0/doc/suites/documents/unchanged/blocks", func(writer http.ResponseWriter, _ *http.Request) {
		t.Error("unchanged document should not be fetched")
		writer.WriteHeader(http.StatusInternalServerError)
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	stored, err := encodeCursor(&cursor{Version: 1, SpaceDocTimes: map[string]map[string]int64{
		"space-1": {"unchanged": 100, "changed": 200, "deleted": 50},
	}})
	if err != nil {
		t.Fatalf("encodeCursor: %v", err)
	}
	items, nextSyncCursor, err := connectorForServer(server).FetchIncremental(
		context.Background(),
		testDataSourceConfig("space-1"),
		&types.SyncCursor{ConnectorCursor: stored},
	)
	if err != nil {
		t.Fatalf("FetchIncremental: %v", err)
	}
	if strings.Join(fetchedDocuments, ",") != "changed,new" {
		t.Errorf("fetched documents = %#v", fetchedDocuments)
	}
	itemByID := make(map[string]types.FetchedItem)
	for _, item := range items {
		itemByID[item.ExternalID] = item
	}
	if len(itemByID) != 3 || !itemByID["deleted"].IsDeleted {
		t.Fatalf("items = %#v", items)
	}
	next, err := decodeCursor(nextSyncCursor)
	if err != nil {
		t.Fatalf("decode next cursor: %v", err)
	}
	wantTimes := map[string]int64{"unchanged": 100, "changed": 300, "new": 400}
	for documentID, want := range wantTimes {
		if got := next.SpaceDocTimes["space-1"][documentID]; got != want {
			t.Errorf("cursor[%s] = %d, want %d", documentID, got, want)
		}
	}
	if _, exists := next.SpaceDocTimes["space-1"]["deleted"]; exists {
		t.Error("deleted document remained in next cursor")
	}
}

func TestConnectorFailedDocumentKeepsPreviousCursorForRetry(t *testing.T) {
	mux := http.NewServeMux()
	addDefaultTokenRoute(t, mux)
	mux.HandleFunc("/v2.0/doc/spaces/space-1/directories", func(writer http.ResponseWriter, _ *http.Request) {
		writeJSON(t, writer, http.StatusOK, directoriesResponse{Children: []dentry{
			{DentryUUID: "doc-1", ContentType: "alidoc", Name: "Doc", UpdatedTime: 200},
		}})
	})
	mux.HandleFunc("/v1.0/doc/suites/documents/doc-1/blocks", func(writer http.ResponseWriter, _ *http.Request) {
		writeJSON(t, writer, http.StatusInternalServerError, apiErrorBody{Message: "temporary"})
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	stored, _ := encodeCursor(&cursor{SpaceDocTimes: map[string]map[string]int64{
		"space-1": {"doc-1": 100},
	}})
	items, nextSyncCursor, err := connectorForServer(server).FetchIncremental(
		context.Background(), testDataSourceConfig("space-1"), &types.SyncCursor{ConnectorCursor: stored},
	)
	if err != nil {
		t.Fatalf("FetchIncremental: %v", err)
	}
	if len(items) != 1 || items[0].Metadata["error"] == "" {
		t.Fatalf("failure item = %#v", items)
	}
	next, _ := decodeCursor(nextSyncCursor)
	if got := next.SpaceDocTimes["space-1"]["doc-1"]; got != 100 {
		t.Fatalf("failed document cursor = %d, want previous 100", got)
	}
}

func TestConnectorPartialSpaceFailurePreservesCursor(t *testing.T) {
	mux := http.NewServeMux()
	addDefaultTokenRoute(t, mux)
	mux.HandleFunc("/v2.0/doc/spaces/good/directories", func(writer http.ResponseWriter, _ *http.Request) {
		writeJSON(t, writer, http.StatusOK, directoriesResponse{})
	})
	mux.HandleFunc("/v2.0/doc/spaces/bad/directories", func(writer http.ResponseWriter, _ *http.Request) {
		writeJSON(t, writer, http.StatusInternalServerError, apiErrorBody{Message: "unavailable"})
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	stored, _ := encodeCursor(&cursor{SpaceDocTimes: map[string]map[string]int64{
		"bad": {"old-doc": 123},
	}})
	_, nextSyncCursor, err := connectorForServer(server).FetchIncremental(
		context.Background(), testDataSourceConfig("good", "bad"), &types.SyncCursor{ConnectorCursor: stored},
	)
	var partial *datasource.PartialFetchError
	if !errors.As(err, &partial) {
		t.Fatalf("error = %v, want PartialFetchError", err)
	}
	next, _ := decodeCursor(nextSyncCursor)
	if got := next.SpaceDocTimes["bad"]["old-doc"]; got != 123 {
		t.Fatalf("failed space cursor = %d, want 123", got)
	}
}

func TestParseConfigRequiresAllCredentials(t *testing.T) {
	tests := []map[string]interface{}{
		nil,
		{"client_id": "key"},
		{"client_id": "key", "client_secret": "secret"},
	}
	for _, credentials := range tests {
		_, err := parseConfig(&types.DataSourceConfig{Credentials: credentials})
		if !errors.Is(err, datasource.ErrInvalidCredentials) {
			t.Errorf("credentials %#v: error = %v", credentials, err)
		}
	}
}
