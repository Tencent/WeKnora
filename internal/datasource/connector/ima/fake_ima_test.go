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
// are reachable. Production keeps the default strict SSRF policy.
func TestMain(m *testing.M) {
	_ = os.Setenv("SSRF_WHITELIST", "127.0.0.1,localhost")
	secutils.ResetSSRFWhitelistForTest()
	os.Exit(m.Run())
}

// fakeFile is one entry in a fake knowledge base listing.
type fakeFile struct {
	MediaID        string
	Title          string
	ParentFolderID string

	// MediaType is what get_media_info reports for this file.
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
	// downloadHeaders records the headers seen by the last download of a media.
	downloadHeaders map[string]http.Header
}

func newFakeIMA(t *testing.T) *fakeIMA {
	t.Helper()
	f := &fakeIMA{
		files:           map[string]map[string][]fakeFile{},
		folders:         map[string]map[string][]fakeFolder{},
		calls:           map[string]int{},
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
		f.mu.Lock()
		resp := getAddableKnowledgeBaseListResp{AddableKnowledgeBaseList: f.bases, IsEnd: true}
		f.mu.Unlock()
		writeEnvelope(w, 0, "", resp)

	case "search_knowledge_base":
		f.mu.Lock()
		resp := searchKnowledgeBaseResp{InfoList: f.searchBases, IsEnd: true}
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
		f.mu.Lock()
		files := f.files[kbID][folderID]
		folders := f.folders[kbID][folderID]
		f.mu.Unlock()

		var list []json.RawMessage
		for _, folder := range folders {
			b, _ := json.Marshal(folderInfo{
				FolderID:       folder.FolderID,
				Name:           folder.Name,
				ParentFolderID: folder.ParentFolderID,
			})
			list = append(list, b)
		}
		for _, file := range files {
			// IMA omits media_type in list responses.
			b, _ := json.Marshal(map[string]interface{}{
				"media_id":         file.MediaID,
				"title":            file.Title,
				"parent_folder_id": file.ParentFolderID,
			})
			list = append(list, b)
		}
		writeEnvelope(w, 0, "", getKnowledgeListResp{KnowledgeList: list, IsEnd: true})

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
