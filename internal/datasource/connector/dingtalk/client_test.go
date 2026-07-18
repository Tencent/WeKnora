package dingtalk

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/datasource"
)

func newTestClient(server *httptest.Server) *client {
	cli := newClient(&Config{
		ClientID:        "ding-app-key",
		ClientSecret:    "app-secret",
		OperatorUnionID: "union-operator",
		baseURL:         server.URL,
	})
	cli.httpClient = server.Client()
	cli.sleep = func(context.Context, time.Duration) error { return nil }
	return cli
}

func writeJSON(t *testing.T, writer http.ResponseWriter, status int, value interface{}) {
	t.Helper()
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	if err := json.NewEncoder(writer).Encode(value); err != nil {
		t.Errorf("encode fake response: %v", err)
	}
}

func tokenHandler(t *testing.T, calls *int) http.HandlerFunc {
	return func(writer http.ResponseWriter, request *http.Request) {
		(*calls)++
		if request.Method != http.MethodPost {
			t.Errorf("token method = %s, want POST", request.Method)
		}
		var body map[string]string
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Errorf("decode token body: %v", err)
		}
		if body["appKey"] != "ding-app-key" || body["appSecret"] != "app-secret" {
			t.Errorf("unexpected token body: %#v", body)
		}
		writeJSON(t, writer, http.StatusOK, accessTokenResponse{AccessToken: "access-token", ExpireIn: 7200})
	}
}

func TestClientListSpacesPaginatesAndCachesToken(t *testing.T) {
	mux := http.NewServeMux()
	tokenCalls := 0
	mux.HandleFunc("/v1.0/oauth2/accessToken", tokenHandler(t, &tokenCalls))
	spaceCalls := 0
	mux.HandleFunc("/v2.0/doc/relations/spaces", func(writer http.ResponseWriter, request *http.Request) {
		spaceCalls++
		if got := request.Header.Get("x-acs-dingtalk-access-token"); got != "access-token" {
			t.Errorf("access token header = %q", got)
		}
		if got := request.URL.Query().Get("operatorId"); got != "union-operator" {
			t.Errorf("operatorId = %q", got)
		}
		switch request.URL.Query().Get("nextToken") {
		case "":
			writeJSON(t, writer, http.StatusOK, relatedSpacesResponse{
				HasMore: true, NextToken: "page-2",
				Items: []dingtalkSpace{{ID: "space-1", Name: "One"}},
			})
		case "page-2":
			writeJSON(t, writer, http.StatusOK, relatedSpacesResponse{
				Items: []dingtalkSpace{{ID: "space-2", Name: "Two"}},
			})
		default:
			t.Errorf("unexpected nextToken %q", request.URL.Query().Get("nextToken"))
		}
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	spaces, err := newTestClient(server).ListSpaces(context.Background())
	if err != nil {
		t.Fatalf("ListSpaces: %v", err)
	}
	if len(spaces) != 2 || spaces[0].ID != "space-1" || spaces[1].ID != "space-2" {
		t.Fatalf("spaces = %#v", spaces)
	}
	if tokenCalls != 1 {
		t.Errorf("token calls = %d, want 1", tokenCalls)
	}
	if spaceCalls != 2 {
		t.Errorf("space calls = %d, want 2", spaceCalls)
	}
}

func TestClientRefreshesTokenAfterUnauthorized(t *testing.T) {
	mux := http.NewServeMux()
	tokenCalls := 0
	mux.HandleFunc("/v1.0/oauth2/accessToken", func(writer http.ResponseWriter, _ *http.Request) {
		tokenCalls++
		writeJSON(t, writer, http.StatusOK, accessTokenResponse{
			AccessToken: fmt.Sprintf("token-%d", tokenCalls),
			ExpireIn:    7200,
		})
	})
	spaceCalls := 0
	mux.HandleFunc("/v2.0/doc/relations/spaces", func(writer http.ResponseWriter, request *http.Request) {
		spaceCalls++
		if request.Header.Get("x-acs-dingtalk-access-token") == "token-1" {
			writeJSON(t, writer, http.StatusUnauthorized, apiErrorBody{Code: "InvalidAuthentication"})
			return
		}
		writeJSON(t, writer, http.StatusOK, relatedSpacesResponse{})
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	if _, err := newTestClient(server).ListSpaces(context.Background()); err != nil {
		t.Fatalf("ListSpaces after token refresh: %v", err)
	}
	if tokenCalls != 2 || spaceCalls != 2 {
		t.Fatalf("token calls=%d space calls=%d, want 2 each", tokenCalls, spaceCalls)
	}
}

func TestClientTokenUnauthorizedWrapsInvalidCredentials(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1.0/oauth2/accessToken", func(writer http.ResponseWriter, _ *http.Request) {
		writeJSON(t, writer, http.StatusUnauthorized, apiErrorBody{Message: "invalid app credentials"})
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	err := newTestClient(server).Ping(context.Background())
	if !errors.Is(err, datasource.ErrInvalidCredentials) {
		t.Fatalf("error = %v, want ErrInvalidCredentials", err)
	}
}

func TestClientListSpaceEntriesRecursesAndPaginates(t *testing.T) {
	mux := http.NewServeMux()
	tokenCalls := 0
	mux.HandleFunc("/v1.0/oauth2/accessToken", tokenHandler(t, &tokenCalls))
	var callsMu sync.Mutex
	var directoryCalls []string
	mux.HandleFunc("/v2.0/doc/spaces/space-1/directories", func(writer http.ResponseWriter, request *http.Request) {
		parent := request.URL.Query().Get("dentryId")
		next := request.URL.Query().Get("nextToken")
		callsMu.Lock()
		directoryCalls = append(directoryCalls, parent+":"+next)
		callsMu.Unlock()
		switch parent + ":" + next {
		case ":":
			writeJSON(t, writer, http.StatusOK, directoriesResponse{
				HasMore: true, NextToken: "root-2",
				Children: []dentry{{DentryID: "folder-1", DentryType: "folder", HasChildren: true}},
			})
		case ":root-2":
			writeJSON(t, writer, http.StatusOK, directoriesResponse{
				Children: []dentry{{DentryUUID: "doc-root", ContentType: "alidoc"}},
			})
		case "folder-1:":
			writeJSON(t, writer, http.StatusOK, directoriesResponse{
				Children: []dentry{{DentryUUID: "doc-child", Extension: "alidoc"}},
			})
		default:
			t.Errorf("unexpected directory request %q", parent+":"+next)
		}
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	entries, err := newTestClient(server).ListSpaceEntries(context.Background(), "space-1")
	if err != nil {
		t.Fatalf("ListSpaceEntries: %v", err)
	}
	if len(entries) != 3 {
		t.Fatalf("entries = %#v, want 3", entries)
	}
	if len(directoryCalls) != 3 {
		t.Fatalf("directory calls = %#v", directoryCalls)
	}
}

func TestClientGetDocumentBlocksUsesIndexWindows(t *testing.T) {
	mux := http.NewServeMux()
	tokenCalls := 0
	mux.HandleFunc("/v1.0/oauth2/accessToken", tokenHandler(t, &tokenCalls))
	blockCalls := 0
	mux.HandleFunc("/v1.0/doc/suites/documents/doc-key/blocks", func(writer http.ResponseWriter, request *http.Request) {
		blockCalls++
		start, _ := strconv.Atoi(request.URL.Query().Get("startIndex"))
		var blocks []json.RawMessage
		count := 1
		if start == 0 {
			count = defaultBlockWindow
		} else if start != defaultBlockWindow {
			t.Errorf("unexpected startIndex %d", start)
		}
		for index := 0; index < count; index++ {
			blocks = append(blocks, json.RawMessage(fmt.Sprintf(`{"blockType":"paragraph","text":"%d"}`, start+index)))
		}
		response := blocksResponse{Success: true}
		response.Result.Data = blocks
		writeJSON(t, writer, http.StatusOK, response)
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	blocks, err := newTestClient(server).GetDocumentBlocks(context.Background(), "doc-key")
	if err != nil {
		t.Fatalf("GetDocumentBlocks: %v", err)
	}
	if len(blocks) != defaultBlockWindow+1 || blockCalls != 2 {
		t.Fatalf("blocks=%d calls=%d", len(blocks), blockCalls)
	}
}
