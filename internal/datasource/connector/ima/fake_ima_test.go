package ima

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

// TestMain whitelists loopback for SSRF so the httptest servers (127.0.0.1)
// are reachable, and the default base host so parseIMAConfig does not resolve
// ima.qq.com over live DNS during a unit test. Production keeps the default
// strict SSRF policy.
func TestMain(m *testing.M) {
	_ = os.Setenv("SSRF_WHITELIST", "127.0.0.1,localhost,ima.qq.com")
	secutils.ResetSSRFWhitelistForTest()
	os.Exit(m.Run())
}

// fakeFile is one entry in a fake knowledge base listing.
type fakeFile struct {
	MediaID        string
	Title          string
	ParentFolderID string

	// MediaType is reported by both get_knowledge_list and get_media_info.
	MediaType int32
	// Body is what the download URL serves.
	Body string
	// ContentType is the download response's Content-Type ("" omits the header).
	ContentType string
	// URLHeaders are the auth headers IMA attaches to url_info.
	URLHeaders map[string]string
	// NoURL makes get_media_info return an empty url_info.url.
	NoURL bool

	// InfoFails makes get_media_info return a 500 for this media_id.
	InfoFails bool
	// DownloadFails makes the download URL return a 500.
	DownloadFails bool

	// NotebookID is reported under notebook_ext_info for notes.
	NotebookID string
	// NoteBody is what get_doc_content serves for NotebookID.
	NoteBody string
	// NoteFails makes get_doc_content return a business error.
	NoteFails bool
}

// fakeFolder is a folder entry in a fake knowledge base listing.
type fakeFolder struct {
	FolderID       string
	Name           string
	ParentFolderID string
}

// fakeIMA is an in-process stand-in for the IMA OpenAPI. Only the endpoints
// the connector calls are implemented.
type fakeIMA struct {
	server *httptest.Server

	mu sync.Mutex
	// files and folders are keyed by knowledge base id, then by parent folder
	// id ("" for the KB root).
	files   map[string]map[string][]fakeFile
	folders map[string]map[string][]fakeFolder
	// bases is what get_addable_knowledge_base_list returns.
	bases []addableKnowledgeBaseInfo
	// searchBases is what search_knowledge_base returns.
	searchBases []searchedKnowledgeBaseInfo

	// calls counts requests per action, plus "download:<media_id>".
	calls map[string]int
	// paging overrides an action's paging envelope; see fakePaging.
	paging map[string]fakePaging
	// downloadHeaders records the headers seen by the last download of a media.
	downloadHeaders map[string]http.Header
}

// fakePaging forces the paging envelope of one action so a test can reproduce
// a vendor that never stops paging. maxRequests bounds the fake: once the
// action has been called more times than that it answers 400, so a run without
// the connector's guard fails instead of spinning forever.
type fakePaging struct {
	// nextCursor is answered as next_cursor; with advanceCursor the request
	// number is appended so every page hands back a cursor never seen before.
	nextCursor    string
	advanceCursor bool
	isEnd         bool
	// stopAfter ends the listing (is_end=true, empty cursor) once the action
	// has been called this many times, so a test can model a finite listing.
	stopAfter int
	// filesByCall replaces get_knowledge_list's payload for one request
	// number (1-based), so a test can prove several pages are merged.
	filesByCall map[int][]fakeFile
	// maxRequests answers 400 once the action has been called more than this.
	maxRequests int
}

func newFakeIMA(t *testing.T) *fakeIMA {
	t.Helper()
	f := &fakeIMA{
		files:           map[string]map[string][]fakeFile{},
		folders:         map[string]map[string][]fakeFolder{},
		calls:           map[string]int{},
		paging:          map[string]fakePaging{},
		downloadHeaders: map[string]http.Header{},
	}
	mux := http.NewServeMux()
	mux.HandleFunc(apiBasePath+"/", f.handleAPI)
	mux.HandleFunc(noteBasePath+"/", f.handleNoteAPI)
	mux.HandleFunc("/dl/", f.handleDownload)
	f.server = httptest.NewServer(mux)
	t.Cleanup(f.server.Close)
	return f
}

// setKB replaces the listing of a knowledge base and registers it as addable.
func (f *fakeIMA) setKB(kbID string, files []fakeFile, folders ...fakeFolder) {
	f.mu.Lock()
	defer f.mu.Unlock()

	byParent := map[string][]fakeFile{}
	for _, file := range files {
		byParent[file.ParentFolderID] = append(byParent[file.ParentFolderID], file)
	}
	f.files[kbID] = byParent

	foldersByParent := map[string][]fakeFolder{}
	for _, folder := range folders {
		foldersByParent[folder.ParentFolderID] = append(foldersByParent[folder.ParentFolderID], folder)
	}
	f.folders[kbID] = foldersByParent

	for _, b := range f.bases {
		if b.ID == kbID {
			return
		}
	}
	f.bases = append(f.bases, addableKnowledgeBaseInfo{ID: kbID, Name: "KB " + kbID})
}

func (f *fakeIMA) callCount(key string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls[key]
}

func (f *fakeIMA) record(key string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls[key]++
}

// pagingFields answers the is_end/next_cursor pair for an action, applying the
// test's paging override when one is installed.
func (f *fakeIMA) pagingFields(action string) (isEnd bool, nextCursor string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	override, ok := f.paging[action]
	if !ok {
		return true, ""
	}
	if override.stopAfter > 0 && f.calls[action] >= override.stopAfter {
		return true, ""
	}
	if override.advanceCursor {
		return override.isEnd, fmt.Sprintf("%s-%d", override.nextCursor, f.calls[action])
	}
	return override.isEnd, override.nextCursor
}

// pagingFiles answers the file payload for one get_knowledge_list request,
// falling back to the knowledge base's own listing.
func (f *fakeIMA) pagingFiles(kbID, folderID string, call int) []fakeFile {
	f.mu.Lock()
	defer f.mu.Unlock()
	if override, ok := f.paging["get_knowledge_list"]; ok {
		if files, ok := override.filesByCall[call]; ok {
			return files
		}
	}
	return f.files[kbID][folderID]
}

// requestBudgetExceeded reports whether the action has used up the request
// budget a test installed, so the fake can answer 400 instead of letting an
// unguarded connector spin.
func (f *fakeIMA) requestBudgetExceeded(action string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	limit := f.paging[action].maxRequests
	return limit > 0 && f.calls[action] > limit
}

// findFile locates a file by media_id across every knowledge base.
func (f *fakeIMA) findFile(mediaID string) (fakeFile, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, byParent := range f.files {
		for _, files := range byParent {
			for _, file := range files {
				if file.MediaID == mediaID {
					return file, true
				}
			}
		}
	}
	return fakeFile{}, false
}

func (f *fakeIMA) handleAPI(w http.ResponseWriter, r *http.Request) {
	action := strings.TrimPrefix(r.URL.Path, apiBasePath+"/")
	f.record(action)
	if f.requestBudgetExceeded(action) {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	var req map[string]interface{}
	_ = json.NewDecoder(r.Body).Decode(&req)
	str := func(k string) string {
		if v, ok := req[k].(string); ok {
			return v
		}
		return ""
	}

	switch action {
	case "get_addable_knowledge_base_list":
		isEnd, nextCursor := f.pagingFields(action)
		f.mu.Lock()
		resp := getAddableKnowledgeBaseListResp{
			AddableKnowledgeBaseList: f.bases,
			IsEnd:                    isEnd,
			NextCursor:               nextCursor,
		}
		f.mu.Unlock()
		writeEnvelope(w, 0, "", resp)

	case "search_knowledge_base":
		isEnd, nextCursor := f.pagingFields(action)
		f.mu.Lock()
		resp := searchKnowledgeBaseResp{InfoList: f.searchBases, IsEnd: isEnd, NextCursor: nextCursor}
		f.mu.Unlock()
		writeEnvelope(w, 0, "", resp)

	case "get_knowledge_base":
		infos := map[string]knowledgeBaseInfo{}
		if ids, ok := req["ids"].([]interface{}); ok {
			for _, raw := range ids {
				id, _ := raw.(string)
				infos[id] = knowledgeBaseInfo{ID: id, Name: "KB " + id, Description: "desc " + id}
			}
		}
		writeEnvelope(w, 0, "", getKnowledgeBaseResp{Infos: infos})

	case "get_knowledge_list":
		kbID, folderID := str("knowledge_base_id"), str("folder_id")
		f.record("get_knowledge_list:" + folderID)
		isEnd, nextCursor := f.pagingFields("get_knowledge_list")
		files := f.pagingFiles(kbID, folderID, f.callCount("get_knowledge_list"))
		f.mu.Lock()
		folders := f.folders[kbID][folderID]
		f.mu.Unlock()

		var list []json.RawMessage
		for _, folder := range folders {
			// Folder entries use the same fields as files in list responses.
			b, _ := json.Marshal(map[string]interface{}{
				"media_id":         folder.FolderID,
				"title":            folder.Name,
				"parent_folder_id": folder.ParentFolderID,
				"media_type":       99,
			})
			list = append(list, b)
		}
		for _, file := range files {
			b, _ := json.Marshal(map[string]interface{}{
				"media_id":         file.MediaID,
				"title":            file.Title,
				"parent_folder_id": file.ParentFolderID,
				"media_type":       file.MediaType,
			})
			list = append(list, b)
		}
		writeEnvelope(w, 0, "", getKnowledgeListResp{KnowledgeList: list, IsEnd: isEnd, NextCursor: nextCursor})

	case "get_media_info":
		mediaID := str("media_id")
		f.record("get_media_info:" + mediaID)
		file, ok := f.findFile(mediaID)
		if !ok {
			writeEnvelope(w, 110001, "unknown media", nil)
			return
		}
		if file.InfoFails {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		resp := getMediaInfoResp{MediaType: file.MediaType}
		resp.NotebookExtInfo = notebookExtInfo{NotebookID: file.NotebookID}
		// Notes carry no url_info; the body lives in the note namespace.
		if !file.NoURL && file.MediaType != mediaTypeNote {
			resp.URLInfo = urlInfo{
				URL:     f.server.URL + "/dl/" + mediaID,
				Headers: file.URLHeaders,
			}
		}
		writeEnvelope(w, 0, "", resp)

	default:
		writeEnvelope(w, 110001, "unsupported action "+action, nil)
	}
}

// handleNoteAPI serves the /openapi/note/v1 namespace.
func (f *fakeIMA) handleNoteAPI(w http.ResponseWriter, r *http.Request) {
	action := strings.TrimPrefix(r.URL.Path, noteBasePath+"/")
	f.record("note/" + action)

	var req map[string]interface{}
	_ = json.NewDecoder(r.Body).Decode(&req)

	if action != "get_doc_content" {
		writeEnvelope(w, 110012, "unsupported note action "+action, nil)
		return
	}

	noteID, _ := req["note_id"].(string)
	f.record("get_doc_content:" + noteID)

	f.mu.Lock()
	defer f.mu.Unlock()
	for _, byParent := range f.files {
		for _, files := range byParent {
			for _, file := range files {
				if file.NotebookID != noteID || noteID == "" {
					continue
				}
				if file.NoteFails {
					writeEnvelope(w, 110011, "note read failed", nil)
					return
				}
				writeEnvelope(w, 0, "", getDocContentResp{Content: file.NoteBody})
				return
			}
		}
	}
	writeEnvelope(w, 110001, "unknown note", nil)
}

func (f *fakeIMA) handleDownload(w http.ResponseWriter, r *http.Request) {
	mediaID := strings.TrimPrefix(r.URL.Path, "/dl/")
	f.record("download:" + mediaID)

	f.mu.Lock()
	f.downloadHeaders[mediaID] = r.Header.Clone()
	f.mu.Unlock()

	file, ok := f.findFile(mediaID)
	if !ok || file.DownloadFails {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	if file.ContentType != "" {
		w.Header().Set("Content-Type", file.ContentType)
	}
	_, _ = w.Write([]byte(file.Body))
}

func writeEnvelope(w http.ResponseWriter, code int, msg string, data interface{}) {
	raw, _ := json.Marshal(data)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(apiEnvelope{Code: code, Msg: msg, Data: raw})
}

func (f *fakeIMA) config(resourceIDs ...string) *types.DataSourceConfig {
	return &types.DataSourceConfig{
		Type: types.ConnectorTypeIMA,
		Credentials: map[string]interface{}{
			"client_id": "cid",
			"api_key":   "key",
			"base_url":  f.server.URL,
		},
		ResourceIDs: resourceIDs,
	}
}

// decodeCursor converts the connector cursor back into its typed form.
func decodeCursor(t *testing.T, cur *types.SyncCursor) *imaCursor {
	t.Helper()
	if cur == nil {
		t.Fatal("cursor is nil")
	}
	b, err := json.Marshal(cur.ConnectorCursor)
	if err != nil {
		t.Fatalf("marshal cursor: %v", err)
	}
	var out imaCursor
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("unmarshal cursor: %v", err)
	}
	return &out
}

// findItem returns the fetched item with the given external id.
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
	var parts []string
	for _, it := range items {
		parts = append(parts, fmt.Sprintf("{id=%s title=%q deleted=%t}", it.ExternalID, it.Title, it.IsDeleted))
	}
	return "[" + strings.Join(parts, " ") + "]"
}
