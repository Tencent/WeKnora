package outline

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/datasource"
	"github.com/Tencent/WeKnora/internal/types"
)

// The compile-time assertion in connector.go is the real guard; this makes the
// intent visible in the suite as well.
func TestConnector_ImplementsInterface(t *testing.T) {
	var c datasource.Connector = NewConnector()
	if c.Type() != types.ConnectorTypeOutline {
		t.Errorf("Type() = %q, want %q", c.Type(), types.ConnectorTypeOutline)
	}
}

func makeDSConfig(f *fakeOutline, resourceIDs []string) *types.DataSourceConfig {
	return &types.DataSourceConfig{
		Type: types.ConnectorTypeOutline,
		Credentials: map[string]interface{}{
			"api_token": f.cfg().APIToken,
			"base_url":  f.cfg().BaseURL,
		},
		ResourceIDs: resourceIDs,
	}
}

func TestConnector_Type(t *testing.T) {
	if got := NewConnector().Type(); got != types.ConnectorTypeOutline {
		t.Errorf("Type() = %q, want %q", got, types.ConnectorTypeOutline)
	}
}

func TestConnector_Validate_Success(t *testing.T) {
	f := newFakeOutline(nil)
	defer f.Close()

	if err := NewConnector().Validate(context.Background(), makeDSConfig(f, nil)); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestConnector_Validate_BadToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
	}))
	defer srv.Close()

	err := NewConnector().Validate(context.Background(), &types.DataSourceConfig{
		Credentials: map[string]interface{}{"api_token": "bad", "base_url": srv.URL},
	})
	if err == nil {
		t.Fatal("expected error for a rejected token")
	}
}

func TestConnector_Validate_MissingToken(t *testing.T) {
	err := NewConnector().Validate(context.Background(), &types.DataSourceConfig{
		Credentials: map[string]interface{}{"base_url": "http://localhost:3000"},
	})
	if err == nil {
		t.Fatal("expected error when api_token is missing")
	}
}

func TestConnector_ListResources_Collections(t *testing.T) {
	f := newFakeOutline(nil)
	defer f.Close()

	res, err := NewConnector().ListResources(context.Background(), makeDSConfig(f, nil), "")
	if err != nil {
		t.Fatalf("ListResources: %v", err)
	}
	if len(res) != 2 {
		t.Fatalf("len = %d, want 2", len(res))
	}
	// Deterministic order matters for UI rendering and response caching.
	if res[0].ExternalID != "col-1" || res[1].ExternalID != "col-2" {
		t.Errorf("unstable order: %q, %q", res[0].ExternalID, res[1].ExternalID)
	}
	if res[0].Type != "collection" {
		t.Errorf("Type = %q, want collection", res[0].Type)
	}
	if res[0].Name != "Handbook" {
		t.Errorf("Name = %q", res[0].Name)
	}
	// Outline returns a path; the resource URL must be openable in a browser.
	want := f.server.URL + "/collection/handbook-abc"
	if res[0].URL != want {
		t.Errorf("URL = %q, want %q", res[0].URL, want)
	}
	if res[0].ModifiedAt.IsZero() {
		t.Error("ModifiedAt was not parsed")
	}
}

// Collections are a flat list, so a lazy-load request for a child level has
// nothing to add.
func TestConnector_ListResources_NonEmptyParentIsEmpty(t *testing.T) {
	f := newFakeOutline(nil)
	defer f.Close()

	res, err := NewConnector().ListResources(context.Background(), makeDSConfig(f, nil), "col-1")
	if err != nil {
		t.Fatalf("ListResources: %v", err)
	}
	if len(res) != 0 {
		t.Errorf("len = %d, want 0 for a non-empty parentID", len(res))
	}
}

func TestConnector_ResolveResourceAncestors_Empty(t *testing.T) {
	f := newFakeOutline(nil)
	defer f.Close()

	got, err := NewConnector().ResolveResourceAncestors(
		context.Background(), makeDSConfig(f, nil), []string{"col-1"})
	if err != nil {
		t.Fatalf("ResolveResourceAncestors: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("len = %d, want 0 (a flat resource list has no ancestors)", len(got))
	}
}

func TestConnector_FetchAll(t *testing.T) {
	f := newFakeOutline([]document{
		{
			ID: "d1", Title: "Hướng dẫn đăng nhập", Text: "## Bước 1\n\nNội dung\n",
			URL: "/doc/huong-dan-dang-nhap-abc", CollectionID: "col-1", Revision: 3,
			CreatedAt: "2026-08-01T00:00:00.000Z", UpdatedAt: "2026-09-01T10:00:00.000Z",
		},
		{
			ID: "d2", Title: "Câu hỏi thường gặp", Text: "## FAQ\n",
			URL: "/doc/cau-hoi-thuong-gap-def", CollectionID: "col-1", Revision: 1,
			UpdatedAt: "2026-09-02T10:00:00.000Z",
		},
	})
	defer f.Close()

	items, err := NewConnector().FetchAll(context.Background(),
		makeDSConfig(f, []string{"col-1"}), []string{"col-1"})
	if err != nil {
		t.Fatalf("FetchAll: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("len = %d, want 2", len(items))
	}

	it := items[0]
	if it.ExternalID != "d1" {
		t.Errorf("ExternalID = %q", it.ExternalID)
	}
	if it.Title != "Hướng dẫn đăng nhập" {
		t.Errorf("Title = %q", it.Title)
	}
	if it.ContentType != "text/markdown" {
		t.Errorf("ContentType = %q, want text/markdown", it.ContentType)
	}
	if !strings.HasSuffix(it.FileName, ".md") {
		t.Errorf("FileName = %q, want a .md suffix", it.FileName)
	}
	if !strings.Contains(string(it.Content), "## Bước 1") {
		t.Errorf("source Markdown was not preserved: %q", it.Content)
	}
	if it.SourceResourceID != "col-1" {
		t.Errorf("SourceResourceID = %q", it.SourceResourceID)
	}
	if it.URL != f.server.URL+"/doc/huong-dan-dang-nhap-abc" {
		t.Errorf("URL = %q", it.URL)
	}
	if it.UpdatedAt.IsZero() || it.CreatedAt.IsZero() {
		t.Error("UpdatedAt/CreatedAt were not parsed")
	}
	if it.Metadata["channel"] != types.ChannelOutline {
		t.Errorf("metadata channel = %q, want %q", it.Metadata["channel"], types.ChannelOutline)
	}
	if it.Metadata["revision"] != "3" {
		t.Errorf("metadata revision = %q, want 3", it.Metadata["revision"])
	}
	if it.Metadata["collection_id"] != "col-1" {
		t.Errorf("metadata collection_id = %q", it.Metadata["collection_id"])
	}
	// No fan-out: images stay inline, so there is no subtree to reconcile.
	if it.ReplacesSubtree {
		t.Error("ReplacesSubtree must stay false: this connector emits no sub-items")
	}
}

// A document that Outline marks deleted or archived must not be ingested as
// content, even if a future API revision starts returning it in the list.
func TestConnector_FetchAll_SkipsGoneAndTemplates(t *testing.T) {
	f := newFakeOutline([]document{
		{ID: "d1", Title: "Bình thường", Text: "ok", CollectionID: "col-1", Revision: 1},
		{ID: "d2", Title: "Đã xoá", Text: "x", CollectionID: "col-1", Revision: 1, DeletedAt: "2026-09-01T00:00:00.000Z"},
		{ID: "d3", Title: "Đã lưu trữ", Text: "x", CollectionID: "col-1", Revision: 1, ArchivedAt: "2026-09-01T00:00:00.000Z"},
		{ID: "d4", Title: "Mẫu", Text: "x", CollectionID: "col-1", Revision: 1, TemplateID: "tpl-1"},
	})
	defer f.Close()

	items, err := NewConnector().FetchAll(context.Background(),
		makeDSConfig(f, []string{"col-1"}), []string{"col-1"})
	if err != nil {
		t.Fatalf("FetchAll: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("len = %d, want 1 (only the normal document)", len(items))
	}
	if items[0].ExternalID != "d1" {
		t.Errorf("kept the wrong document: %q", items[0].ExternalID)
	}
}

func TestConnector_FetchAll_StripsEscapeArtifacts(t *testing.T) {
	bs := "\\"
	f := newFakeOutline([]document{
		{ID: "d1", Title: "Có rác", Text: "## Mục\n\n   " + bs + "n" + bs + "n\n\n* Thật\n",
			CollectionID: "col-1", Revision: 1},
	})
	defer f.Close()

	items, err := NewConnector().FetchAll(context.Background(),
		makeDSConfig(f, []string{"col-1"}), []string{"col-1"})
	if err != nil {
		t.Fatalf("FetchAll: %v", err)
	}
	got := string(items[0].Content)
	if strings.Contains(got, bs+"n") {
		t.Errorf("escape artifact reached the ingested content:\n%q", got)
	}
	if !strings.Contains(got, "* Thật") {
		t.Errorf("real content was removed:\n%q", got)
	}
}

func TestConnector_FetchIncremental_SkipsUnchangedAndReportsDeletions(t *testing.T) {
	f := newFakeOutline([]document{
		{ID: "d1", Title: "Không đổi", Text: "a", CollectionID: "col-1", Revision: 5,
			UpdatedAt: "2026-09-01T00:00:00.000Z"},
		{ID: "d2", Title: "Đã sửa", Text: "b", CollectionID: "col-1", Revision: 9,
			UpdatedAt: "2026-09-02T00:00:00.000Z"},
	})
	defer f.Close()

	// Prior cursor: d1 at the same revision (unchanged), d2 older (changed),
	// d3 present last time but gone from the source now (deleted).
	prior := &types.SyncCursor{
		ConnectorCursor: map[string]interface{}{
			"collection_doc_revisions": map[string]interface{}{
				"col-1": map[string]interface{}{"d1": 5, "d2": 4, "d3": 1},
			},
		},
	}

	items, next, err := NewConnector().FetchIncremental(
		context.Background(), makeDSConfig(f, []string{"col-1"}), prior)
	if err != nil {
		t.Fatalf("FetchIncremental: %v", err)
	}

	byID := map[string]types.FetchedItem{}
	for _, it := range items {
		byID[it.ExternalID] = it
	}
	if _, ok := byID["d1"]; ok {
		t.Error("d1 is unchanged and must not be re-emitted")
	}
	changed, ok := byID["d2"]
	if !ok {
		t.Fatal("d2 changed and must be emitted")
	}
	if len(changed.Content) == 0 {
		t.Error("changed document has no content")
	}
	deleted, ok := byID["d3"]
	if !ok {
		t.Fatal("d3 vanished from the source and must be emitted as deleted")
	}
	if !deleted.IsDeleted {
		t.Error("d3 must carry IsDeleted")
	}
	if deleted.SourceResourceID != "col-1" {
		t.Errorf("deleted item SourceResourceID = %q", deleted.SourceResourceID)
	}

	// The new cursor must describe the current state, not the prior one.
	if next == nil {
		t.Fatal("FetchIncremental returned a nil cursor")
	}
	if next.LastSyncTime.IsZero() {
		t.Error("cursor LastSyncTime not set")
	}
	raw, ok := next.ConnectorCursor["collection_doc_revisions"]
	if !ok {
		t.Fatal("cursor is missing collection_doc_revisions")
	}
	encoded, err := json.Marshal(raw)
	if err != nil {
		t.Fatalf("marshal cursor: %v", err)
	}
	var revs map[string]map[string]int
	if err := json.Unmarshal(encoded, &revs); err != nil {
		t.Fatalf("decode cursor: %v", err)
	}
	if revs["col-1"]["d2"] != 9 {
		t.Errorf("cursor d2 revision = %d, want 9", revs["col-1"]["d2"])
	}
	if _, ok := revs["col-1"]["d3"]; ok {
		t.Error("cursor still lists d3, which no longer exists")
	}
}

func TestConnector_FetchIncremental_NoCursorFetchesEverything(t *testing.T) {
	f := newFakeOutline([]document{
		{ID: "d1", Title: "A", Text: "a", CollectionID: "col-1", Revision: 1},
		{ID: "d2", Title: "B", Text: "b", CollectionID: "col-1", Revision: 1},
	})
	defer f.Close()

	items, _, err := NewConnector().FetchIncremental(
		context.Background(), makeDSConfig(f, []string{"col-1"}), nil)
	if err != nil {
		t.Fatalf("FetchIncremental: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("len = %d, want 2 (a nil cursor means a first full pass)", len(items))
	}
}

func TestConnector_FetchIncremental_NoResourcesIsAnError(t *testing.T) {
	f := newFakeOutline(nil)
	defer f.Close()

	_, _, err := NewConnector().FetchIncremental(
		context.Background(), makeDSConfig(f, nil), nil)
	if err == nil {
		t.Fatal("expected an error when no collections are selected")
	}
}

func TestConnector_FetchAll_InlinesImages(t *testing.T) {
	f := newFakeOutline([]document{
		{ID: "d1", Title: "Có ảnh", CollectionID: "col-1", Revision: 1,
			Text: "Xem hình:\n\n![](/api/attachments.redirect?id=att-1 \"Ảnh minh hoạ\")\n"},
	})
	defer f.Close()
	f.serveAttachment("att-1", pngBytes(2048), "image/png")

	cfg := makeDSConfig(f, []string{"col-1"})
	cfg.MultimodalEnabled = true
	items, err := NewConnector().FetchAll(context.Background(), cfg, []string{"col-1"})
	if err != nil {
		t.Fatalf("FetchAll: %v", err)
	}
	body := string(items[0].Content)
	if !strings.Contains(body, "![Ảnh minh hoạ](data:image/png;base64,") {
		t.Errorf("image was not inlined with its title as alt text:\n%s", firstRunes(body, 120))
	}
	if strings.Contains(body, "attachments.redirect") {
		t.Error("the token-protected URL survived; ingestion would 401 on it")
	}
	if items[0].Metadata["images_inlined"] != "1" {
		t.Errorf("metadata images_inlined = %q, want 1", items[0].Metadata["images_inlined"])
	}
}

func TestConnector_FetchAll_SkipsImageDownloadWithoutMultimodal(t *testing.T) {
	f := newFakeOutline([]document{
		{ID: "d1", Title: "Có ảnh", CollectionID: "col-1", Revision: 1,
			Text: "![](/api/attachments.redirect?id=att-1)"},
	})
	defer f.Close()
	f.serveAttachment("att-1", pngBytes(2048), "image/png")

	// MultimodalEnabled is false: the KB would reject the image anyway, so the
	// bytes must not be downloaded or base64-inflated into the upload.
	items, err := NewConnector().FetchAll(context.Background(),
		makeDSConfig(f, []string{"col-1"}), []string{"col-1"})
	if err != nil {
		t.Fatalf("FetchAll: %v", err)
	}
	if strings.Contains(string(items[0].Content), "data:image/") {
		t.Error("image was inlined even though the KB has no VLM")
	}
	if n := f.hitCount("/api/attachments.redirect"); n != 0 {
		t.Errorf("attachment was downloaded %d times, want 0", n)
	}
	if items[0].Metadata["images_inlined"] != "0" {
		t.Errorf("images_inlined = %q, want 0", items[0].Metadata["images_inlined"])
	}
}
