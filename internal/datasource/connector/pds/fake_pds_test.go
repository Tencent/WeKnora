package pds

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	secutils "github.com/Tencent/WeKnora/internal/utils"
)

// TestMain whitelists loopback for SSRF so the httptest servers
// (127.0.0.1) are reachable, plus a couple of placeholder hostnames used
// by the parseConfig unit tests (which don't actually open a connection).
// Production keeps the default strict SSRF policy.
func TestMain(m *testing.M) {
	_ = os.Setenv("SSRF_WHITELIST",
		"127.0.0.1,localhost,pds.aliyuncs.com,env.example.com")
	secutils.ResetSSRFWhitelistForTest()
	os.Exit(m.Run())
}

// fakePDS is an in-process stand-in for the PDS OpenAPI. It speaks the
// REAL wire format: flat JSON bodies ({"items": [...], "next_marker":
// "..."}), no envelope. Only the endpoints the connector calls are
// implemented; everything else returns 404.
type fakePDS struct {
	server *httptest.Server

	mu sync.Mutex
	// drives are what drive/list returns.
	drives []pdsDrive
	// files[parentID] are the immediate children of the drive root
	// (parentID == "root") or that folder.
	files map[string][]pdsFile
	// downloads[fileID] are the bytes served by /dl/<fileID>.
	downloads map[string]fakeFileBody
	// deltas: input cursor -> page of delta items + has_more.
	deltas map[string]fakeDelta
	// lastCursor is returned by file/get_last_cursor.
	lastCursor string

	// Test hooks.
	expireNextDeltaFlag   bool        // next list_delta returns InvalidCursor
	forceUnauthorizedOnce bool        // next API call returns 401, then succeeds
	tokenExchanges        int         // oauth/token call count
	lastAction            string      // most recent action seen
	lastHeaders           http.Header // headers of the most recent request
	actionLog             []string    // every action seen, in order

	// calls counter, keyed by action.
	calls map[string]int
}

type fakeFileBody struct {
	bytes       []byte
	contentType string
}

type fakeDelta struct {
	items   []pdsDeltaItem
	hasMore bool
}

// newFakePDS starts a fresh fakePDS on a random local port.
func newFakePDS(t *testing.T) *fakePDS {
	t.Helper()
	f := &fakePDS{
		files:     map[string][]pdsFile{},
		downloads: map[string]fakeFileBody{},
		deltas:    map[string]fakeDelta{},
		calls:     map[string]int{},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/v2/", f.handleAPI)
	mux.HandleFunc("/dl/", f.handleDownload)
	f.server = httptest.NewServer(mux)
	t.Cleanup(f.server.Close)
	return f
}

func (f *fakePDS) setDrives(drives ...pdsDrive) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.drives = drives
}

// setFiles configures the children of a folder. parentID == "root" is the
// drive root (PDS's own convention for parent_file_id).
func (f *fakePDS) setFiles(parentID string, files ...pdsFile) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.files[parentID] = files
}

func (f *fakePDS) setDownload(fileID string, body []byte, contentType string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.downloads[fileID] = fakeFileBody{bytes: body, contentType: contentType}
}

// setDelta configures what file/list_delta returns for a given input
// cursor. Items carry their op, exactly like the real feed.
func (f *fakePDS) setDelta(cursor string, items []pdsDeltaItem, hasMore bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deltas[cursor] = fakeDelta{items: items, hasMore: hasMore}
}

func (f *fakePDS) setLastCursor(c string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lastCursor = c
}

// expireNextDelta causes the next list_delta call to return InvalidCursor;
// subsequent calls succeed normally.
func (f *fakePDS) expireNextDelta() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.expireNextDeltaFlag = true
}

// forceUnauthorized causes the next API call (not oauth/token) to return
// 401 once, exercising the client's token refresh + retry.
func (f *fakePDS) forceUnauthorized() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.forceUnauthorizedOnce = true
}

// actionIndex returns the position of the first occurrence of action in
// the request log, or -1 when it was never called. Used to assert call
// ORDERING (e.g. cursor capture must precede the bootstrap walk).
func (f *fakePDS) actionIndex(action string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i, a := range f.actionLog {
		if a == action {
			return i
		}
	}
	return -1
}

func (f *fakePDS) callCount(action string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls[action]
}

// config returns a DataSourceConfig wired to the fake server with a
// pre-set access_token. driveID is what Settings.drive_id will hold. The
// fake server URL (e.g. http://127.0.0.1:1234) is passed verbatim so the
// connector honors the http:// scheme instead of forcing https://.
func (f *fakePDS) config(driveID string) *types.DataSourceConfig {
	return &types.DataSourceConfig{
		Type: types.ConnectorTypePDS,
		Credentials: map[string]interface{}{
			"endpoint":     f.server.URL,
			"access_token": "test-token",
			"domain_id":    "test-domain",
		},
		ResourceIDs: []string{},
		Settings: map[string]interface{}{
			"drive_id": driveID,
		},
	}
}

// configRefresh is the refresh_token variant: no access_token is preset,
// so the client must exchange the refresh token via /v2/oauth/token
// before its first API call.
func (f *fakePDS) configRefresh(driveID string) *types.DataSourceConfig {
	return &types.DataSourceConfig{
		Type: types.ConnectorTypePDS,
		Credentials: map[string]interface{}{
			"endpoint":      f.server.URL,
			"refresh_token": "refresh-me",
			"domain_id":     "test-domain",
		},
		ResourceIDs: []string{},
		Settings: map[string]interface{}{
			"drive_id": driveID,
		},
	}
}

// configAKSK returns a config with static AK/SK credentials, which route
// through the SDK client. lastHeaders-style assertions don't apply; the
// fake simply validates the bearer-less request reached it.
func (f *fakePDS) configAKSK(driveID string) *types.DataSourceConfig {
	return &types.DataSourceConfig{
		Type: types.ConnectorTypePDS,
		Credentials: map[string]interface{}{
			"endpoint":          f.server.URL,
			"domain_id":         "test-domain",
			"access_key_id":     "AKID-test",
			"access_key_secret": "test-secret",
		},
		ResourceIDs: []string{},
		Settings: map[string]interface{}{
			"drive_id": driveID,
		},
	}
}

func (f *fakePDS) handleAPI(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.lastHeaders = r.Header.Clone()
	f.mu.Unlock()
	action := strings.TrimPrefix(r.URL.Path, "/v2/")

	// OAuth token exchange — flat {access_token,...} body, no auth needed.
	if action == "oauth/token" {
		f.mu.Lock()
		f.tokenExchanges++
		f.mu.Unlock()
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"access_token": "exchanged-token",
			"token_type":   "Bearer",
			"expires_in":   7200,
		})
		return
	}

	f.mu.Lock()
	f.calls[action]++
	f.lastAction = action
	f.actionLog = append(f.actionLog, action)
	forceUnauthorized := f.forceUnauthorizedOnce
	f.forceUnauthorizedOnce = false
	f.mu.Unlock()

	// Bearer auth check (the token-auth paths). The SDK AK/SK path signs
	// with ACS3 instead — accept any non-empty Authorization there.
	auth := r.Header.Get("Authorization")
	if forceUnauthorized {
		writeJSON(w, http.StatusUnauthorized,
			map[string]string{"code": "InvalidAccessToken", "message": "token expired"})
		return
	}
	if strings.HasPrefix(auth, "Bearer ") {
		tok := strings.TrimPrefix(auth, "Bearer ")
		if tok != "test-token" && tok != "exchanged-token" {
			writeJSON(w, http.StatusUnauthorized,
				map[string]string{"code": "InvalidAccessToken", "message": "bad token"})
			return
		}
	} else if auth == "" {
		writeJSON(w, http.StatusUnauthorized,
			map[string]string{"code": "MissingAuthorization", "message": "no auth header"})
		return
	}

	var body map[string]interface{}
	_ = json.NewDecoder(r.Body).Decode(&body)

	switch action {
	case "drive/list":
		f.mu.Lock()
		drives := append([]pdsDrive(nil), f.drives...)
		f.mu.Unlock()
		writeJSON(w, http.StatusOK, map[string]interface{}{"items": drives})

	case "file/list":
		parentID, _ := body["parent_file_id"].(string)
		f.mu.Lock()
		files := append([]pdsFile(nil), f.files[parentID]...)
		f.mu.Unlock()
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"items":       files,
			"next_marker": "",
		})

	case "file/get":
		fileID, _ := body["file_id"].(string)
		f.mu.Lock()
		var found *pdsFile
		for _, bucket := range f.files {
			for i := range bucket {
				if bucket[i].FileID == fileID {
					found = &bucket[i]
					break
				}
			}
			if found != nil {
				break
			}
		}
		snapshot := pdsFile{}
		if found != nil {
			snapshot = *found
		}
		f.mu.Unlock()
		if found == nil {
			writeJSON(w, http.StatusNotFound,
				map[string]string{"code": "NotFound", "message": "file not found: " + fileID})
			return
		}
		writeJSON(w, http.StatusOK, snapshot)

	case "file/get_download_url":
		fileID, _ := body["file_id"].(string)
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"url": f.server.URL + "/dl/" + fileID,
		})

	case "file/get_last_cursor":
		f.mu.Lock()
		c := f.lastCursor
		f.mu.Unlock()
		writeJSON(w, http.StatusOK, map[string]interface{}{"cursor": c})

	case "file/list_delta":
		f.mu.Lock()
		if f.expireNextDeltaFlag {
			f.expireNextDeltaFlag = false
			f.mu.Unlock()
			writeJSON(w, http.StatusBadRequest,
				map[string]string{"code": "InvalidCursor", "message": "cursor expired"})
			return
		}
		cursor, _ := body["cursor"].(string)
		d, ok := f.deltas[cursor]
		f.mu.Unlock()
		if !ok {
			writeJSON(w, http.StatusOK, map[string]interface{}{
				"items":    []pdsDeltaItem{},
				"cursor":   cursor,
				"has_more": false,
			})
			return
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"items":    d.items,
			"cursor":   cursor + "_next",
			"has_more": d.hasMore,
		})

	default:
		writeJSON(w, http.StatusNotFound,
			map[string]string{"code": "NotFound", "message": "unsupported action " + action})
	}
}

func (f *fakePDS) handleDownload(w http.ResponseWriter, r *http.Request) {
	fileID := strings.TrimPrefix(r.URL.Path, "/dl/")
	f.mu.Lock()
	body, ok := f.downloads[fileID]
	f.mu.Unlock()
	if !ok {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	if body.contentType != "" {
		w.Header().Set("Content-Type", body.contentType)
	}
	_, _ = w.Write(body.bytes)
}

// writeJSON serializes a flat PDS response body (the real API has no
// envelope on success).
func writeJSON(w http.ResponseWriter, status int, payload interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func findItem(items []types.FetchedItem, externalID string) (types.FetchedItem, bool) {
	for _, it := range items {
		if it.ExternalID == externalID {
			return it, true
		}
	}
	return types.FetchedItem{}, false
}

func mustFindItem(t *testing.T, items []types.FetchedItem, externalID string) types.FetchedItem {
	t.Helper()
	it, ok := findItem(items, externalID)
	if !ok {
		t.Fatalf("item %s not found in %s", externalID, describeItems(items))
	}
	return it
}

func describeItems(items []types.FetchedItem) string {
	parts := make([]string, 0, len(items))
	for _, it := range items {
		parts = append(parts, fmt.Sprintf("{id=%s title=%q deleted=%t}", it.ExternalID, it.Title, it.IsDeleted))
	}
	return "[" + strings.Join(parts, " ") + "]"
}
