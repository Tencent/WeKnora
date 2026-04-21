package yuque

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

func makeDSConfig(f *fakeYuque, resourceIDs []string) *types.DataSourceConfig {
	return &types.DataSourceConfig{
		Type:        types.ConnectorTypeYuque,
		Credentials: map[string]interface{}{"api_token": f.cfg().APIToken, "base_url": f.cfg().BaseURL},
		ResourceIDs: resourceIDs,
	}
}

func TestConnector_Type(t *testing.T) {
	if NewConnector().Type() != types.ConnectorTypeYuque {
		t.Errorf("Type() = %q, want %q", NewConnector().Type(), types.ConnectorTypeYuque)
	}
}

func TestConnector_Validate_Success(t *testing.T) {
	f := newFakeYuque()
	defer f.Close()

	if err := NewConnector().Validate(context.Background(), makeDSConfig(f, nil)); err != nil {
		t.Fatalf("Validate error: %v", err)
	}
}

func TestConnector_Validate_Bad401(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
	}))
	defer srv.Close()

	err := NewConnector().Validate(context.Background(), &types.DataSourceConfig{
		Credentials: map[string]interface{}{"api_token": "bad", "base_url": srv.URL},
	})
	if err == nil {
		t.Fatal("expected error for 401")
	}
}

func TestConnector_ListResources_Aggregates(t *testing.T) {
	f := newFakeYuque()
	defer f.Close()
	// user = alice (id=1)
	// Override default /api/v2/user handler to return alice (f.handleJSON panics on duplicate path registration).
	f.mux = http.NewServeMux()
	f.server.Config.Handler = f.mux
	f.handleJSON("/api/v2/user", 200, v2UserResponse{Data: v2User{ID: 1, Login: "alice"}})
	// personal repos
	f.handleJSON("/api/v2/users/alice/repos", 200, v2RepoListResponse{Data: []v2Repo{
		{ID: 10, Slug: "personal", Name: "Personal", Type: "Book", Namespace: "alice/personal"},
	}})
	// 2 groups
	f.handleJSON("/api/v2/users/1/groups", 200, v2GroupListResponse{Data: []v2Group{
		{ID: 100, Login: "team-a"}, {ID: 101, Login: "team-b"},
	}})
	f.handleJSON("/api/v2/groups/team-a/repos", 200, v2RepoListResponse{Data: []v2Repo{
		{ID: 20, Slug: "ab", Name: "A Book", Type: "Book", Namespace: "team-a/ab"},
	}})
	f.handleJSON("/api/v2/groups/team-b/repos", 200, v2RepoListResponse{Data: []v2Repo{
		{ID: 30, Slug: "bb", Name: "B Book", Type: "Book", Namespace: "team-b/bb"},
	}})

	resources, err := NewConnector().ListResources(context.Background(), makeDSConfig(f, nil))
	if err != nil {
		t.Fatalf("ListResources: %v", err)
	}
	if len(resources) != 3 {
		t.Fatalf("len = %d, want 3", len(resources))
	}
	// Verify fields on one entry
	var ab *types.Resource
	for i := range resources {
		if resources[i].Name == "A Book" {
			ab = &resources[i]
		}
	}
	if ab == nil {
		t.Fatal("expected A Book resource")
	}
	if ab.ExternalID != "20" || ab.Description != "team-a/ab" || ab.Type != "book" {
		t.Errorf("got %+v", ab)
	}
}

func TestConnector_ListResources_DedupByID(t *testing.T) {
	f := newFakeYuque()
	defer f.Close()
	f.mux = http.NewServeMux()
	f.server.Config.Handler = f.mux
	f.handleJSON("/api/v2/user", 200, v2UserResponse{Data: v2User{ID: 1, Login: "alice"}})
	f.handleJSON("/api/v2/users/alice/repos", 200, v2RepoListResponse{Data: []v2Repo{
		{ID: 99, Slug: "shared", Type: "Book", Namespace: "alice/shared"},
	}})
	f.handleJSON("/api/v2/users/1/groups", 200, v2GroupListResponse{Data: []v2Group{
		{ID: 200, Login: "team-x"},
	}})
	// Same repo (ID 99) appears again via a team the user is in.
	f.handleJSON("/api/v2/groups/team-x/repos", 200, v2RepoListResponse{Data: []v2Repo{
		{ID: 99, Slug: "shared", Type: "Book", Namespace: "alice/shared"},
	}})

	resources, err := NewConnector().ListResources(context.Background(), makeDSConfig(f, nil))
	if err != nil {
		t.Fatalf("ListResources: %v", err)
	}
	if len(resources) != 1 {
		t.Errorf("expected dedup to 1, got %d", len(resources))
	}
}

func TestConnector_FetchAll_Markdown(t *testing.T) {
	f := newFakeYuque()
	defer f.Close()
	// Book 7 → 2 docs; one published Doc, one draft (filtered)
	f.handleJSON("/api/v2/repos/7/docs", 200, v2DocListResponse{Data: []v2Doc{
		{ID: 101, Type: "Doc", Status: "1", Title: "Hello", Slug: "hello", BookID: 7, ContentUpdatedAt: "2026-04-20T10:00:00Z", WordCount: 42},
		{ID: 102, Type: "Doc", Status: "0", Title: "Draft", Slug: "draft", BookID: 7, ContentUpdatedAt: "2026-04-20T11:00:00Z"}, // draft, skipped
	}})
	f.handleJSON("/api/v2/repos/docs/101", 200, v2DocDetailResponse{
		Data: v2DocDetail{ID: 101, Title: "Hello", Body: "# Hello\n\nworld", Format: "markdown", Status: "1",
			ContentUpdatedAt: "2026-04-20T10:00:00Z", Book: v2Repo{Namespace: "alice/demo"}},
	})

	items, err := NewConnector().FetchAll(context.Background(), makeDSConfig(f, []string{"7"}), []string{"7"})
	if err != nil {
		t.Fatalf("FetchAll: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("len = %d, want 1 (draft filtered)", len(items))
	}
	it := items[0]
	if it.ExternalID != "101" || it.Title != "Hello" {
		t.Errorf("got %+v", it)
	}
	if string(it.Content) != "# Hello\n\nworld" {
		t.Errorf("Content = %q", string(it.Content))
	}
	if it.ContentType != "text/markdown" {
		t.Errorf("ContentType = %q", it.ContentType)
	}
	if it.FileName != "Hello.md" {
		t.Errorf("FileName = %q", it.FileName)
	}
	if it.Metadata["channel"] != types.ChannelYuque {
		t.Errorf("channel = %q", it.Metadata["channel"])
	}
	if it.UpdatedAt.IsZero() {
		t.Error("UpdatedAt should be parsed")
	}
}

func TestConnector_FetchAll_DocDetailError_EmitsPlaceholder(t *testing.T) {
	f := newFakeYuque()
	defer f.Close()
	f.handleJSON("/api/v2/repos/9/docs", 200, v2DocListResponse{Data: []v2Doc{
		{ID: 301, Type: "Doc", Status: "1", Title: "Broken", Slug: "broken", ContentUpdatedAt: "2026-04-20T10:00:00Z"},
		{ID: 302, Type: "Doc", Status: "1", Title: "OK", Slug: "ok", ContentUpdatedAt: "2026-04-20T10:00:00Z"},
	}})
	f.mux.HandleFunc("/api/v2/repos/docs/301", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest) // 4xx is non-retriable, bubbles up as an error
		_, _ = w.Write([]byte(`{"message":"broken"}`))
	})
	f.handleJSON("/api/v2/repos/docs/302", 200, v2DocDetailResponse{
		Data: v2DocDetail{ID: 302, Title: "OK", Body: "ok", Status: "1", ContentUpdatedAt: "2026-04-20T10:00:00Z"},
	})

	items, err := NewConnector().FetchAll(context.Background(), makeDSConfig(f, []string{"9"}), []string{"9"})
	if err != nil {
		t.Fatalf("FetchAll must not abort the batch on a single detail error, got %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("want 2 items (placeholder + ok), got %d: %+v", len(items), items)
	}
	// Find the placeholder — the one with the "error" metadata key.
	var placeholder *types.FetchedItem
	for i := range items {
		if items[i].Metadata["error"] != "" {
			placeholder = &items[i]
		}
	}
	if placeholder == nil {
		t.Fatal("expected a placeholder item with Metadata[error] set")
	}
	if placeholder.ExternalID != "301" || placeholder.Title != "Broken" {
		t.Errorf("placeholder identity wrong: %+v", placeholder)
	}
	if placeholder.Metadata["channel"] != types.ChannelYuque {
		t.Errorf("placeholder channel = %q", placeholder.Metadata["channel"])
	}
	if placeholder.Metadata["book_id"] != "9" || placeholder.Metadata["doc_id"] != "301" || placeholder.Metadata["slug"] != "broken" {
		t.Errorf("placeholder missing traceability metadata: %+v", placeholder.Metadata)
	}
	if len(placeholder.Content) != 0 {
		t.Errorf("placeholder should have empty Content, got %q", string(placeholder.Content))
	}
}

func TestConnector_FetchAll_LakeFormatIngestedAsMarkdown(t *testing.T) {
	// Yuque's v2 doc-detail serves `body` as Markdown for type="Doc" regardless
	// of authoring format. format="lake" means the doc was edited in the Lake
	// editor, but `body` is still the Markdown representation (Lake XML lives
	// in `body_lake`). So both should be ingested identically.
	f := newFakeYuque()
	defer f.Close()
	f.handleJSON("/api/v2/repos/12/docs", 200, v2DocListResponse{Data: []v2Doc{
		{ID: 401, Type: "Doc", Status: "1", Title: "Lake Doc", Slug: "lake", ContentUpdatedAt: "2026-04-20T10:00:00Z"},
		{ID: 402, Type: "Doc", Status: "1", Title: "MD Doc", Slug: "md", ContentUpdatedAt: "2026-04-20T10:00:00Z"},
	}})
	f.handleJSON("/api/v2/repos/docs/401", 200, v2DocDetailResponse{
		Data: v2DocDetail{ID: 401, Title: "Lake Doc", Format: "lake", Body: "# lake body as markdown", Status: "1", ContentUpdatedAt: "2026-04-20T10:00:00Z"},
	})
	f.handleJSON("/api/v2/repos/docs/402", 200, v2DocDetailResponse{
		Data: v2DocDetail{ID: 402, Title: "MD Doc", Format: "markdown", Body: "# MD", Status: "1", ContentUpdatedAt: "2026-04-20T10:00:00Z"},
	})

	items, err := NewConnector().FetchAll(context.Background(), makeDSConfig(f, []string{"12"}), []string{"12"})
	if err != nil {
		t.Fatalf("FetchAll: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("want 2 items, got %d", len(items))
	}
	wantBody := map[string]string{
		"401": "# lake body as markdown",
		"402": "# MD",
	}
	for _, it := range items {
		if string(it.Content) != wantBody[it.ExternalID] {
			t.Errorf("item %s Content = %q, want %q", it.ExternalID, it.Content, wantBody[it.ExternalID])
		}
		if it.ContentType != "text/markdown" {
			t.Errorf("item %s ContentType = %q, want text/markdown", it.ExternalID, it.ContentType)
		}
	}
}

func TestConnector_FetchAll_SkipsUnsupportedFormats(t *testing.T) {
	f := newFakeYuque()
	defer f.Close()
	f.handleJSON("/api/v2/repos/13/docs", 200, v2DocListResponse{Data: []v2Doc{
		{ID: 501, Type: "Doc", Status: "1", Title: "HTML Doc", Slug: "h", ContentUpdatedAt: "2026-04-20T10:00:00Z"},
		{ID: 502, Type: "Doc", Status: "1", Title: "OK Doc", Slug: "ok", ContentUpdatedAt: "2026-04-20T10:00:00Z"},
	}})
	f.handleJSON("/api/v2/repos/docs/501", 200, v2DocDetailResponse{
		Data: v2DocDetail{ID: 501, Title: "HTML Doc", Format: "html", Body: "<p>raw html</p>", Status: "1", ContentUpdatedAt: "2026-04-20T10:00:00Z"},
	})
	f.handleJSON("/api/v2/repos/docs/502", 200, v2DocDetailResponse{
		Data: v2DocDetail{ID: 502, Title: "OK Doc", Format: "lake", Body: "# ok", Status: "1", ContentUpdatedAt: "2026-04-20T10:00:00Z"},
	})

	items, err := NewConnector().FetchAll(context.Background(), makeDSConfig(f, []string{"13"}), []string{"13"})
	if err != nil {
		t.Fatalf("FetchAll: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("want 2 items (placeholder + ok), got %d", len(items))
	}
	var htmlPlaceholder, okItem *types.FetchedItem
	for i := range items {
		if items[i].ExternalID == "501" {
			htmlPlaceholder = &items[i]
		}
		if items[i].ExternalID == "502" {
			okItem = &items[i]
		}
	}
	if htmlPlaceholder == nil || okItem == nil {
		t.Fatal("missing one of the items")
	}
	if htmlPlaceholder.Metadata["skip_reason"] == "" {
		t.Errorf("html doc should have skip_reason metadata, got %+v", htmlPlaceholder.Metadata)
	}
	if len(htmlPlaceholder.Content) != 0 {
		t.Errorf("html placeholder should have empty Content")
	}
	if string(okItem.Content) != "# ok" {
		t.Errorf("lake doc Content = %q, want %q", string(okItem.Content), "# ok")
	}
}

func TestConnector_FetchAll_SkipsNonDocTypes(t *testing.T) {
	f := newFakeYuque()
	defer f.Close()
	f.handleJSON("/api/v2/repos/8/docs", 200, v2DocListResponse{Data: []v2Doc{
		{ID: 201, Type: "Sheet", Status: "1", Title: "S"},
		{ID: 202, Type: "Board", Status: "1", Title: "B"},
		{ID: 203, Type: "Thread", Status: "1", Title: "T"},
		{ID: 204, Type: "Doc", Status: "1", Title: "D", ContentUpdatedAt: "2026-04-20T10:00:00Z"},
	}})
	f.handleJSON("/api/v2/repos/docs/204", 200, v2DocDetailResponse{
		Data: v2DocDetail{ID: 204, Title: "D", Body: "text", Status: "1", ContentUpdatedAt: "2026-04-20T10:00:00Z"},
	})

	items, err := NewConnector().FetchAll(context.Background(), makeDSConfig(f, []string{"8"}), []string{"8"})
	if err != nil {
		t.Fatalf("FetchAll: %v", err)
	}
	if len(items) != 1 || items[0].ExternalID != "204" {
		t.Errorf("expected only the Doc, got %+v", items)
	}
}

func TestConnector_FetchIncremental_FirstSync(t *testing.T) {
	f := newFakeYuque()
	defer f.Close()
	f.handleJSON("/api/v2/repos/10/docs", 200, v2DocListResponse{Data: []v2Doc{
		{ID: 1, Type: "Doc", Status: "1", Title: "A", ContentUpdatedAt: "2026-04-20T10:00:00Z"},
		{ID: 2, Type: "Doc", Status: "1", Title: "B", ContentUpdatedAt: "2026-04-20T11:00:00Z"},
	}})
	f.handleJSON("/api/v2/repos/docs/1", 200, v2DocDetailResponse{Data: v2DocDetail{ID: 1, Title: "A", Body: "a", Status: "1", ContentUpdatedAt: "2026-04-20T10:00:00Z"}})
	f.handleJSON("/api/v2/repos/docs/2", 200, v2DocDetailResponse{Data: v2DocDetail{ID: 2, Title: "B", Body: "b", Status: "1", ContentUpdatedAt: "2026-04-20T11:00:00Z"}})

	items, cursor, err := NewConnector().FetchIncremental(context.Background(), makeDSConfig(f, []string{"10"}), nil)
	if err != nil {
		t.Fatalf("FetchIncremental: %v", err)
	}
	if len(items) != 2 {
		t.Errorf("items = %d, want 2 on first sync", len(items))
	}
	if cursor == nil {
		t.Fatal("cursor must not be nil")
	}
}

func TestConnector_FetchIncremental_NoChanges(t *testing.T) {
	f := newFakeYuque()
	defer f.Close()
	f.handleJSON("/api/v2/repos/10/docs", 200, v2DocListResponse{Data: []v2Doc{
		{ID: 1, Type: "Doc", Status: "1", Title: "A", ContentUpdatedAt: "2026-04-20T10:00:00Z"},
	}})
	f.handleJSON("/api/v2/repos/docs/1", 200, v2DocDetailResponse{Data: v2DocDetail{ID: 1, Title: "A", Body: "a", Status: "1", ContentUpdatedAt: "2026-04-20T10:00:00Z"}})

	// First sync → establish cursor
	_, cursor1, err := NewConnector().FetchIncremental(context.Background(), makeDSConfig(f, []string{"10"}), nil)
	if err != nil {
		t.Fatalf("first sync: %v", err)
	}

	// Second sync with same cursor → 0 items
	items, _, err := NewConnector().FetchIncremental(context.Background(), makeDSConfig(f, []string{"10"}), cursor1)
	if err != nil {
		t.Fatalf("second sync: %v", err)
	}
	if len(items) != 0 {
		t.Errorf("expected 0 changed items, got %d", len(items))
	}
}

func TestConnector_FetchIncremental_ReturnsOnlyChanged(t *testing.T) {
	// First sync: 2 docs, both at t0.
	f1 := newFakeYuque()
	f1.handleJSON("/api/v2/repos/11/docs", 200, v2DocListResponse{Data: []v2Doc{
		{ID: 1, Type: "Doc", Status: "1", Title: "A", Slug: "a", ContentUpdatedAt: "2026-04-20T10:00:00Z"},
		{ID: 2, Type: "Doc", Status: "1", Title: "B", Slug: "b", ContentUpdatedAt: "2026-04-20T10:00:00Z"},
	}})
	f1.handleJSON("/api/v2/repos/docs/1", 200, v2DocDetailResponse{Data: v2DocDetail{ID: 1, Title: "A", Body: "a", Status: "1", ContentUpdatedAt: "2026-04-20T10:00:00Z"}})
	f1.handleJSON("/api/v2/repos/docs/2", 200, v2DocDetailResponse{Data: v2DocDetail{ID: 2, Title: "B", Body: "b", Status: "1", ContentUpdatedAt: "2026-04-20T10:00:00Z"}})

	_, cursor1, err := NewConnector().FetchIncremental(context.Background(), makeDSConfig(f1, []string{"11"}), nil)
	if err != nil {
		t.Fatalf("first sync: %v", err)
	}
	f1.Close()

	// Second sync: doc 1 unchanged, doc 2 has a newer ContentUpdatedAt.
	f2 := newFakeYuque()
	defer f2.Close()
	f2.handleJSON("/api/v2/repos/11/docs", 200, v2DocListResponse{Data: []v2Doc{
		{ID: 1, Type: "Doc", Status: "1", Title: "A", Slug: "a", ContentUpdatedAt: "2026-04-20T10:00:00Z"},
		{ID: 2, Type: "Doc", Status: "1", Title: "B2", Slug: "b", ContentUpdatedAt: "2026-04-20T12:00:00Z"},
	}})
	// Only doc 2's detail should be fetched; if the implementation incorrectly
	// fetches doc 1, the test will still pass metadata-wise — but this handler
	// makes the expected single call clear.
	f2.handleJSON("/api/v2/repos/docs/2", 200, v2DocDetailResponse{Data: v2DocDetail{ID: 2, Title: "B2", Body: "b2 updated", Status: "1", ContentUpdatedAt: "2026-04-20T12:00:00Z"}})

	items, _, err := NewConnector().FetchIncremental(context.Background(), makeDSConfig(f2, []string{"11"}), cursor1)
	if err != nil {
		t.Fatalf("second sync: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected exactly 1 changed item, got %d: %+v", len(items), items)
	}
	if items[0].ExternalID != "2" {
		t.Errorf("expected changed item ExternalID=2, got %q", items[0].ExternalID)
	}
	if string(items[0].Content) != "b2 updated" {
		t.Errorf("expected refreshed body, got %q", string(items[0].Content))
	}
}

func TestConnector_FetchIncremental_DetectsDeletion(t *testing.T) {
	// First sync: 2 docs
	f1 := newFakeYuque()
	f1.handleJSON("/api/v2/repos/10/docs", 200, v2DocListResponse{Data: []v2Doc{
		{ID: 1, Type: "Doc", Status: "1", Title: "A", ContentUpdatedAt: "2026-04-20T10:00:00Z"},
		{ID: 2, Type: "Doc", Status: "1", Title: "B", ContentUpdatedAt: "2026-04-20T11:00:00Z"},
	}})
	f1.handleJSON("/api/v2/repos/docs/1", 200, v2DocDetailResponse{Data: v2DocDetail{ID: 1, Title: "A", Body: "a", Status: "1", ContentUpdatedAt: "2026-04-20T10:00:00Z"}})
	f1.handleJSON("/api/v2/repos/docs/2", 200, v2DocDetailResponse{Data: v2DocDetail{ID: 2, Title: "B", Body: "b", Status: "1", ContentUpdatedAt: "2026-04-20T11:00:00Z"}})

	_, cursor1, err := NewConnector().FetchIncremental(context.Background(), makeDSConfig(f1, []string{"10"}), nil)
	if err != nil {
		t.Fatalf("first sync: %v", err)
	}
	f1.Close()

	// Second sync: doc 2 removed
	f2 := newFakeYuque()
	defer f2.Close()
	f2.handleJSON("/api/v2/repos/10/docs", 200, v2DocListResponse{Data: []v2Doc{
		{ID: 1, Type: "Doc", Status: "1", Title: "A", ContentUpdatedAt: "2026-04-20T10:00:00Z"},
	}})

	items, _, err := NewConnector().FetchIncremental(context.Background(), makeDSConfig(f2, []string{"10"}), cursor1)
	if err != nil {
		t.Fatalf("second sync: %v", err)
	}
	deleted := 0
	for _, it := range items {
		if it.IsDeleted && it.ExternalID == "2" {
			deleted++
		}
	}
	if deleted != 1 {
		t.Errorf("expected 1 deletion for doc 2, got %d; items=%+v", deleted, items)
	}
}

func TestConnector_ListResources_ContinuesOnGroupFailure(t *testing.T) {
	f := newFakeYuque()
	defer f.Close()
	f.mux = http.NewServeMux()
	f.server.Config.Handler = f.mux
	f.handleJSON("/api/v2/user", 200, v2UserResponse{Data: v2User{ID: 1, Login: "alice"}})
	f.handleJSON("/api/v2/users/alice/repos", 200, v2RepoListResponse{Data: []v2Repo{
		{ID: 10, Slug: "me", Name: "Mine", Type: "Book", Namespace: "alice/me"},
	}})
	f.handleJSON("/api/v2/users/1/groups", 200, v2GroupListResponse{Data: []v2Group{
		{ID: 100, Login: "team-ok"}, {ID: 101, Login: "team-forbidden"},
	}})
	f.handleJSON("/api/v2/groups/team-ok/repos", 200, v2RepoListResponse{Data: []v2Repo{
		{ID: 20, Slug: "ok", Name: "OK Book", Type: "Book", Namespace: "team-ok/ok"},
	}})
	// team-forbidden returns 403 — should be skipped, not abort the whole call.
	f.mux.HandleFunc("/api/v2/groups/team-forbidden/repos", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"message":"forbidden"}`))
	})

	resources, err := NewConnector().ListResources(context.Background(), makeDSConfig(f, nil))
	if err != nil {
		t.Fatalf("ListResources should not fail on per-group error, got %v", err)
	}
	// Expect the 2 successful repos (personal + team-ok). team-forbidden is skipped.
	if len(resources) != 2 {
		t.Fatalf("expected 2 resources (personal + team-ok), got %d: %+v", len(resources), resources)
	}
	names := map[string]bool{}
	for _, r := range resources {
		names[r.Name] = true
	}
	if !names["Mine"] || !names["OK Book"] {
		t.Errorf("expected Mine + OK Book, got %v", names)
	}
}
