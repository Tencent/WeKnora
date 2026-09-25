package yuque

import (
	"context"
	"encoding/json"
	"strconv"
	"testing"

	"github.com/Tencent/WeKnora/internal/datasource"
	"github.com/Tencent/WeKnora/internal/types"
)

// --- helpers ---

func makeDSConfigWithSettings(
	f *fakeYuque, resourceIDs []string, settings map[string]interface{},
) *types.DataSourceConfig {
	cfg := makeDSConfig(f, resourceIDs)
	cfg.Settings = settings
	return cfg
}

func tocTitle(uuid, parent, title string) v2TOCNode {
	return v2TOCNode{UUID: uuid, Type: tocNodeTypeTitle, Title: title, ParentUUID: parent}
}

func tocDoc(uuid, parent string, docID int64, title string) v2TOCNode {
	return v2TOCNode{
		UUID: uuid, Type: tocNodeTypeDoc, Title: title,
		ParentUUID: parent, DocID: flexibleDocID(docID),
	}
}

// --- buildTOCPaths ---

func TestBuildTOCPaths_EmptyAndTitleOnly(t *testing.T) {
	for name, nodes := range map[string][]v2TOCNode{
		"nil":        nil,
		"empty":      {},
		"title-only": {tocTitle("t1", "", "Group"), tocTitle("t2", "t1", "Nested")},
	} {
		segs, inTOC := buildTOCPaths(nodes)
		if len(segs) != 0 || len(inTOC) != 0 {
			t.Errorf("%s: want empty maps, got segs=%v inTOC=%v", name, segs, inTOC)
		}
	}
}

func TestBuildTOCPaths_SingleLevel(t *testing.T) {
	segs, inTOC := buildTOCPaths([]v2TOCNode{
		tocTitle("t1", "", "Group"),
		tocDoc("d1", "t1", 101, "Doc"),
	})
	if !inTOC[101] {
		t.Fatal("inTOC[101] should be true")
	}
	if got := segs[101]; len(got) != 1 || got[0] != "Group" {
		t.Errorf("segs[101] = %v, want [Group]", got)
	}
}

func TestBuildTOCPaths_MultiLevel(t *testing.T) {
	segs, _ := buildTOCPaths([]v2TOCNode{
		tocTitle("t1", "", "A"),
		tocTitle("t2", "t1", "B"),
		tocDoc("d1", "t2", 101, "Doc"),
	})
	got := segs[101]
	if len(got) != 2 || got[0] != "A" || got[1] != "B" {
		t.Errorf("segs[101] = %v, want [A B] (root first)", got)
	}
}

// A DOC nested under another DOC contributes the parent document's title as an
// extra path segment.
func TestBuildTOCPaths_DocNestedUnderDoc(t *testing.T) {
	segs, _ := buildTOCPaths([]v2TOCNode{
		tocTitle("t1", "", "A"),
		tocDoc("d1", "t1", 101, "Parent doc"),
		tocDoc("d2", "d1", 102, "Child doc"),
	})
	got := segs[102]
	if len(got) != 2 || got[0] != "A" || got[1] != "Parent doc" {
		t.Errorf("segs[102] = %v, want [A Parent doc]", got)
	}
}

// A document that appears in the TOC without any enclosing group is still "in
// the TOC" — the distinction that keeps toc_only from dropping it.
func TestBuildTOCPaths_InTOCWithoutGroup(t *testing.T) {
	segs, inTOC := buildTOCPaths([]v2TOCNode{
		tocDoc("d1", "", 101, "Loose doc"),
	})
	if !inTOC[101] {
		t.Error("inTOC[101] should be true: the doc is in the TOC node list")
	}
	if _, ok := segs[101]; ok {
		t.Errorf("segs[101] should be absent for a group-less doc, got %v", segs[101])
	}
}

func TestBuildTOCPaths_DanglingParent(t *testing.T) {
	segs, inTOC := buildTOCPaths([]v2TOCNode{
		tocDoc("d1", "does-not-exist", 101, "Doc"),
	})
	if !inTOC[101] {
		t.Error("inTOC[101] should be true")
	}
	if _, ok := segs[101]; ok {
		t.Errorf("segs[101] should be absent, got %v", segs[101])
	}
}

// A cyclic parent_uuid chain must terminate rather than loop forever.
func TestBuildTOCPaths_CycleTerminates(t *testing.T) {
	segs, inTOC := buildTOCPaths([]v2TOCNode{
		tocTitle("a", "b", "A"),
		tocTitle("b", "a", "B"),
		tocDoc("d", "a", 101, "Doc"),
	})
	if !inTOC[101] {
		t.Error("inTOC[101] should be true")
	}
	// Two titles before the cycle is detected; the exact length matters less
	// than the fact that this returns at all.
	if got := len(segs[101]); got != 2 {
		t.Errorf("len(segs[101]) = %d, want 2 (walk must stop at the cycle)", got)
	}
}

// A "/" in a group title must not become an extra nesting level: segments are
// sanitised individually, before they are ever joined.
func TestBuildTOCPaths_SanitizesEachSegment(t *testing.T) {
	segs, _ := buildTOCPaths([]v2TOCNode{
		tocTitle("t1", "", "A/B"),
		tocDoc("d1", "t1", 101, "Doc"),
	})
	got := segs[101]
	if len(got) != 1 || got[0] != "A_B" {
		t.Errorf("segs[101] = %v, want [A_B]", got)
	}
}

func TestBuildTOCPaths_SkipsNonDocAndEmptyID(t *testing.T) {
	segs, inTOC := buildTOCPaths([]v2TOCNode{
		tocTitle("t1", "", "Group"),
		{UUID: "d0", Type: tocNodeTypeDoc, Title: "No id"},            // doc_id absent → 0
		{UUID: "d1", Type: "SHEET", Title: "Sheet", ParentUUID: "t1"}, // not a DOC node
	})
	if len(segs) != 0 || len(inTOC) != 0 {
		t.Errorf("want empty maps, got segs=%v inTOC=%v", segs, inTOC)
	}
}

func TestBuildTOCPaths_DuplicateDocID(t *testing.T) {
	segs, inTOC := buildTOCPaths([]v2TOCNode{
		tocTitle("t1", "", "A"),
		tocTitle("t2", "", "B"),
		tocDoc("d1", "t1", 101, "Doc"),
		tocDoc("d2", "t2", 101, "Doc"),
	})
	if !inTOC[101] {
		t.Error("inTOC[101] should be true")
	}
	// Last write wins; what matters is that the map stays well-formed.
	if len(segs[101]) != 1 {
		t.Errorf("segs[101] = %v, want a single segment", segs[101])
	}
}

// --- flexibleDocID ---

func TestFlexibleDocID_Unmarshal(t *testing.T) {
	cases := []struct {
		name    string
		raw     string
		want    int64
		wantErr bool
	}{
		{"integer", `{"doc_id":259589930}`, 259589930, false},
		{"string", `{"doc_id":"259589930"}`, 259589930, false},
		{"empty string (TITLE node)", `{"doc_id":""}`, 0, false},
		{"null", `{"doc_id":null}`, 0, false},
		{"missing", `{}`, 0, false},
		{"padded string", `{"doc_id":" 42 "}`, 42, false},
		{"boolean", `{"doc_id":true}`, 0, true},
		{"non-numeric string", `{"doc_id":"abc"}`, 0, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var n v2TOCNode
			err := json.Unmarshal([]byte(tc.raw), &n)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error for %s", tc.raw)
				}
				return
			}
			if err != nil {
				t.Fatalf("unmarshal %s: %v", tc.raw, err)
			}
			if int64(n.DocID) != tc.want {
				t.Errorf("DocID = %d, want %d", int64(n.DocID), tc.want)
			}
		})
	}
}

// The real API mixes node shapes in one response; the whole list must decode.
func TestV2TOCResponse_RealisticPayload(t *testing.T) {
	raw := `{"data":[
		{"uuid":"t1","type":"TITLE","title":"Group","doc_id":"","parent_uuid":""},
		{"uuid":"d1","type":"DOC","title":"Doc","doc_id":259589930,"parent_uuid":"t1"},
		{"uuid":"d2","type":"DOC","title":"Loose","doc_id":"267830135","parent_uuid":""}
	]}`
	var resp v2TOCResponse
	if err := json.Unmarshal([]byte(raw), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	segs, inTOC := buildTOCPaths(resp.Data)
	if !inTOC[259589930] || !inTOC[267830135] {
		t.Errorf("inTOC = %v, want both docs", inTOC)
	}
	if got := segs[259589930]; len(got) != 1 || got[0] != "Group" {
		t.Errorf("segs[259589930] = %v, want [Group]", got)
	}
	if _, ok := segs[267830135]; ok {
		t.Errorf("segs[267830135] should be absent, got %v", segs[267830135])
	}
}

// --- buildFolderFileName ---

// The regression guard: with default settings the emitted FileName must be
// exactly what previous releases produced, byte for byte.
func TestBuildFolderFileName_DefaultIsUnchanged(t *testing.T) {
	got := buildFolderFileName(defaultFolderSettings(), true, "Book", []string{"Group"}, "Title")
	want := datasource.SanitizeFileName("Title") + ".md"
	if got != want {
		t.Errorf("FileName = %q, want %q", got, want)
	}
	for _, bookName := range []string{"", "Book"} {
		for _, segs := range [][]string{nil, {"A"}, {"A", "B"}} {
			if got := buildFolderFileName(folderSettings{}, true, bookName, segs, "T"); got != "T.md" {
				t.Errorf("zero-value settings: FileName = %q, want %q", got, "T.md")
			}
		}
	}
}

func TestBuildFolderFileName_TOCMode(t *testing.T) {
	tocMode := folderSettings{FolderMode: folderModeTOC}

	cases := []struct {
		name     string
		settings folderSettings
		tocOK    bool
		bookName string
		segs     []string
		want     string
	}{
		{"book + group", tocMode, true, "My Book", []string{"A", "B"}, "My Book/A/B/Doc.md"},
		{"book only (doc not in TOC)", tocMode, true, "My Book", nil, "My Book/Doc.md"},
		{"empty book name", tocMode, true, "", []string{"A"}, "A/Doc.md"},
		{"book name needs sanitising", tocMode, true, "A/B", nil, "A_B/Doc.md"},
		{"toc unavailable degrades to flat", tocMode, false, "My Book", []string{"A"}, "Doc.md"},
		{"default mode stays flat", defaultFolderSettings(), true, "My Book", []string{"A"}, "Doc.md"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := buildFolderFileName(tc.settings, tc.tocOK, tc.bookName, tc.segs, "Doc"); got != tc.want {
				t.Errorf("FileName = %q, want %q", got, tc.want)
			}
		})
	}
}

// --- parseFolderSettings ---

func TestParseFolderSettings_Defaults(t *testing.T) {
	got := parseFolderSettings(context.Background(), &types.DataSourceConfig{})
	want := defaultFolderSettings()
	if got != want {
		t.Errorf("got %+v, want %+v", got, want)
	}
	if got.FolderMode != folderModeNone {
		t.Errorf("FolderMode = %q, want %q (upgrades must not change behaviour)", got.FolderMode, folderModeNone)
	}
	if got := parseFolderSettings(context.Background(), nil); got != want {
		t.Errorf("nil config: got %+v, want %+v", got, want)
	}
}

func TestParseFolderSettings_ReadsValues(t *testing.T) {
	got := parseFolderSettings(context.Background(), &types.DataSourceConfig{
		Settings: map[string]interface{}{
			"folder_mode": "toc",
			"toc_only":    true,
		},
	})
	if got.FolderMode != folderModeTOC || !got.TOCOnly {
		t.Errorf("got %+v", got)
	}
}

func TestParseFolderSettings_TolerantOfBadInput(t *testing.T) {
	cases := []struct {
		name     string
		settings map[string]interface{}
		want     folderSettings
	}{
		{"unknown mode", map[string]interface{}{"folder_mode": "bogus"}, defaultFolderSettings()},
		{"wrong type for mode", map[string]interface{}{"folder_mode": 42}, defaultFolderSettings()},
		{
			"string bools",
			map[string]interface{}{"toc_only": "true"},
			folderSettings{FolderMode: folderModeNone, TOCOnly: true},
		},
		{"unrecognised bool", map[string]interface{}{"toc_only": "maybe"}, defaultFolderSettings()},
		{
			"mixed case mode",
			map[string]interface{}{"folder_mode": " TOC "},
			folderSettings{FolderMode: folderModeTOC},
		},
		{
			// Unknown keys are ignored rather than failing the sync — settings
			// arrive from JSON, and a typo must not take a data source down.
			"unknown key is ignored",
			map[string]interface{}{"folder_mode": "toc", "no_such_setting": false},
			folderSettings{FolderMode: folderModeTOC},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := parseFolderSettings(context.Background(), &types.DataSourceConfig{Settings: tc.settings})
			if got != tc.want {
				t.Errorf("got %+v, want %+v", got, tc.want)
			}
		})
	}
}

// --- FetchAll integration ---

const testTS = "2026-04-20T10:00:00Z"

// fakeBookDocs wires book 7 with three documents: 101 sits under a group, 102
// appears in the TOC without any group, and 103 is absent from the TOC.
//
// FileName is derived from the *list* endpoint's title, so the assertions below
// use those (Grouped / Loose / Unfiled) rather than the detail titles.
func fakeBookDocs(f *fakeYuque) {
	f.handleJSON("/api/v2/repos/7/docs", 200, v2DocListResponse{Data: []v2Doc{
		{ID: 101, Type: "Doc", Status: "1", Title: "Grouped", Slug: "g", BookID: 7, ContentUpdatedAt: testTS},
		{ID: 102, Type: "Doc", Status: "1", Title: "Loose", Slug: "l", BookID: 7, ContentUpdatedAt: testTS},
		{ID: 103, Type: "Doc", Status: "1", Title: "Unfiled", Slug: "u", BookID: 7, ContentUpdatedAt: testTS},
	}})
	for _, d := range []struct {
		id    int64
		title string
	}{{101, "Grouped"}, {102, "Loose"}, {103, "Unfiled"}} {
		f.handleJSON(docDetailPath(d.id), 200, v2DocDetailResponse{Data: v2DocDetail{
			ID: d.id, Title: d.title, Body: "# body", Format: "markdown", Status: "1",
			ContentUpdatedAt: testTS,
			Book:             v2Repo{ID: 7, Name: "My Book", Namespace: "alice/demo"},
		}})
	}
}

// fakeBookTOC registers the TOC endpoint for book 7. A ServeMux panics on a
// duplicate pattern, so this is registered exactly once per test.
func fakeBookTOC(f *fakeYuque, status int) {
	f.handleJSON("/api/v2/repos/7/toc", status, v2TOCResponse{Data: []v2TOCNode{
		tocTitle("t1", "", "Group"),
		tocDoc("d1", "t1", 101, "Grouped"),
		tocDoc("d2", "", 102, "Loose"),
	}})
}

func docDetailPath(id int64) string {
	return "/api/v2/repos/docs/" + strconv.FormatInt(id, 10)
}

func itoa(v int64) string { return strconv.FormatInt(v, 10) }

func fileNames(items []types.FetchedItem) map[string]string {
	out := make(map[string]string, len(items))
	for _, it := range items {
		out[it.ExternalID] = it.FileName
	}
	return out
}

func TestConnector_FetchAll_TOCPaths(t *testing.T) {
	f := newFakeYuque()
	defer f.Close()
	fakeBookDocs(f)
	fakeBookTOC(f, 200)

	items, err := NewConnector().FetchAll(context.Background(),
		makeDSConfigWithSettings(f, []string{"7"}, map[string]interface{}{"folder_mode": "toc"}),
		[]string{"7"})
	if err != nil {
		t.Fatalf("FetchAll: %v", err)
	}
	got := fileNames(items)
	want := map[string]string{
		"101": "My Book/Group/Grouped.md", // grouped document
		"102": "My Book/Loose.md",         // in the TOC, no group → book segment only
		"103": "My Book/Unfiled.md",       // not in the TOC → book segment only
	}
	for id, wantName := range want {
		if got[id] != wantName {
			t.Errorf("doc %s: FileName = %q, want %q", id, got[id], wantName)
		}
	}
}

// Without settings the connector must behave exactly as before: flat names.
func TestConnector_FetchAll_DefaultSettingsFlat(t *testing.T) {
	f := newFakeYuque()
	defer f.Close()
	fakeBookDocs(f)
	fakeBookTOC(f, 200) // present but must be ignored without folder_mode=toc

	items, err := NewConnector().FetchAll(context.Background(), makeDSConfig(f, []string{"7"}), []string{"7"})
	if err != nil {
		t.Fatalf("FetchAll: %v", err)
	}
	want := map[string]string{"101": "Grouped.md", "102": "Loose.md", "103": "Unfiled.md"}
	got := fileNames(items)
	for id, wantName := range want {
		if got[id] != wantName {
			t.Errorf("doc %s: FileName = %q, want %q", id, got[id], wantName)
		}
	}
}

// toc_only must drop documents absent from the TOC but keep a group-less
// document that is present in it. The latter is the case most likely to
// regress, since its derived path is empty.
func TestConnector_FetchAll_TOCOnly(t *testing.T) {
	f := newFakeYuque()
	defer f.Close()
	fakeBookDocs(f)
	fakeBookTOC(f, 200)

	items, err := NewConnector().FetchAll(context.Background(),
		makeDSConfigWithSettings(f, []string{"7"}, map[string]interface{}{
			"folder_mode": "toc", "toc_only": true,
		}), []string{"7"})
	if err != nil {
		t.Fatalf("FetchAll: %v", err)
	}
	got := fileNames(items)
	if len(got) != 2 {
		t.Fatalf("kept %d docs, want 2 (101 grouped + 102 group-less); got %v", len(got), got)
	}
	if _, ok := got["101"]; !ok {
		t.Error("doc 101 (grouped) should be kept")
	}
	if _, ok := got["102"]; !ok {
		t.Error("doc 102 (in TOC but group-less) must be kept — not filtered by an empty path")
	}
	if _, ok := got["103"]; ok {
		t.Error("doc 103 (absent from TOC) should be filtered out")
	}
}

// toc_only is an admission filter, not a reaper. A document that was already
// ingested and is then filtered out must not be reported as deleted: Yuque's
// own web UI cannot show documents created through the API that were never
// attached to the TOC, so removing them here would leave their content
// reachable from neither side.
func TestConnector_FetchIncremental_TOCOnlyDoesNotDelete(t *testing.T) {
	// First sync: folder paths on, no admission filter — all three land.
	f1 := newFakeYuque()
	fakeBookDocs(f1)
	fakeBookTOC(f1, 200)
	_, cursor1, err := NewConnector().FetchIncremental(context.Background(),
		makeDSConfigWithSettings(f1, []string{"7"}, map[string]interface{}{"folder_mode": "toc"}), nil)
	if err != nil {
		t.Fatalf("first sync: %v", err)
	}
	f1.Close()

	// Second sync: same source, now with toc_only. Doc 103 is absent from the
	// TOC so it is filtered, but it is still present in the book's document list.
	f2 := newFakeYuque()
	defer f2.Close()
	fakeBookDocs(f2)
	fakeBookTOC(f2, 200)

	items, _, err := NewConnector().FetchIncremental(context.Background(),
		makeDSConfigWithSettings(f2, []string{"7"}, map[string]interface{}{
			"folder_mode": "toc", "toc_only": true,
		}), cursor1)
	if err != nil {
		t.Fatalf("second sync: %v", err)
	}
	for _, it := range items {
		if it.ExternalID == "103" {
			t.Errorf("doc 103 was filtered by toc_only but is still reported "+
				"(IsDeleted=%t); an admission filter must not turn into a deletion", it.IsDeleted)
		}
	}
}

// Admitting a previously filtered document must ingest it. A filtered document
// is deliberately kept out of the cursor, so attaching it to the TOC later — or
// switching toc_only off — has to read as new rather than as unchanged.
func TestConnector_FetchIncremental_AdmitsPreviouslyFilteredDoc(t *testing.T) {
	// First sync with toc_only on: doc 103 is absent from the TOC and filtered.
	f1 := newFakeYuque()
	fakeBookDocs(f1)
	fakeBookTOC(f1, 200)
	items1, cursor1, err := NewConnector().FetchIncremental(context.Background(),
		makeDSConfigWithSettings(f1, []string{"7"}, map[string]interface{}{
			"folder_mode": "toc", "toc_only": true,
		}), nil)
	if err != nil {
		t.Fatalf("first sync: %v", err)
	}
	for _, it := range items1 {
		if it.ExternalID == "103" {
			t.Fatalf("doc 103 should have been filtered on the first sync: %+v", it)
		}
	}
	f1.Close()

	// Second sync: doc 103 has been attached to the TOC. Its content is
	// unchanged, so only the admission decision can bring it in.
	f2 := newFakeYuque()
	defer f2.Close()
	fakeBookDocs(f2)
	f2.handleJSON("/api/v2/repos/7/toc", 200, v2TOCResponse{Data: []v2TOCNode{
		tocTitle("t1", "", "Group"),
		tocDoc("d1", "t1", 101, "Grouped"),
		tocDoc("d2", "", 102, "Loose"),
		tocDoc("d3", "t1", 103, "Unfiled"),
	}})

	items2, _, err := NewConnector().FetchIncremental(context.Background(),
		makeDSConfigWithSettings(f2, []string{"7"}, map[string]interface{}{
			"folder_mode": "toc", "toc_only": true,
		}), cursor1)
	if err != nil {
		t.Fatalf("second sync: %v", err)
	}
	var admitted bool
	for _, it := range items2 {
		if it.ExternalID != "103" || it.IsDeleted {
			continue
		}
		admitted = true
		if want := "My Book/Group/Unfiled.md"; it.FileName != want {
			t.Errorf("doc 103: FileName = %q, want %q", it.FileName, want)
		}
	}
	if !admitted {
		t.Errorf("doc 103 was attached to the TOC but was not ingested; items=%+v", items2)
	}
}

// A document that really is gone from the source must still be reported — the
// admission filter must not have disabled deletion detection.
func TestConnector_FetchIncremental_StillDetectsRealDeletion(t *testing.T) {
	f1 := newFakeYuque()
	fakeBookDocs(f1)
	fakeBookTOC(f1, 200)
	_, cursor1, err := NewConnector().FetchIncremental(context.Background(),
		makeDSConfigWithSettings(f1, []string{"7"}, map[string]interface{}{
			"folder_mode": "toc", "toc_only": true,
		}), nil)
	if err != nil {
		t.Fatalf("first sync: %v", err)
	}
	f1.Close()

	// Doc 101 disappears from the source entirely.
	f2 := newFakeYuque()
	defer f2.Close()
	f2.handleJSON("/api/v2/repos/7/docs", 200, v2DocListResponse{Data: []v2Doc{
		{ID: 102, Type: "Doc", Status: "1", Title: "Loose", Slug: "l", BookID: 7, ContentUpdatedAt: testTS},
	}})
	fakeBookTOC(f2, 200)

	items, _, err := NewConnector().FetchIncremental(context.Background(),
		makeDSConfigWithSettings(f2, []string{"7"}, map[string]interface{}{
			"folder_mode": "toc", "toc_only": true,
		}), cursor1)
	if err != nil {
		t.Fatalf("second sync: %v", err)
	}
	deleted := 0
	for _, it := range items {
		if it.IsDeleted && it.ExternalID == "101" {
			deleted++
		}
	}
	if deleted != 1 {
		t.Errorf("expected doc 101 to be reported as deleted once, got %d; items=%+v", deleted, items)
	}
}

// If the TOC endpoint is unavailable, toc_only must not filter anything —
// otherwise an outage would silently drop the whole book.
func TestConnector_FetchAll_TOCUnavailableDegrades(t *testing.T) {
	f := newFakeYuque()
	defer f.Close()
	fakeBookDocs(f)
	// Older Yuque deployments have no TOC endpoint at all → 404.
	fakeBookTOC(f, 404)

	items, err := NewConnector().FetchAll(context.Background(),
		makeDSConfigWithSettings(f, []string{"7"}, map[string]interface{}{
			"folder_mode": "toc", "toc_only": true,
		}), []string{"7"})
	if err != nil {
		t.Fatalf("FetchAll must not fail when the TOC is unavailable: %v", err)
	}
	if len(items) != 3 {
		t.Fatalf("kept %d docs, want 3 (no filtering without a TOC)", len(items))
	}
	want := map[string]string{"101": "Grouped.md", "102": "Loose.md", "103": "Unfiled.md"}
	got := fileNames(items)
	for id, wantName := range want {
		if got[id] != wantName {
			t.Errorf("doc %s: FileName = %q, want flat %q", id, got[id], wantName)
		}
	}
}

// A multi-book sync must not merge identically named top-level groups.
func TestConnector_FetchAll_SeparatesBooksWithSameGroupName(t *testing.T) {
	f := newFakeYuque()
	defer f.Close()
	for _, book := range []struct {
		id   int64
		name string
	}{
		{7, "Book One"},
		{8, "Book Two"},
	} {
		docID := book.id * 10
		f.handleJSON("/api/v2/repos/"+itoa(book.id)+"/docs", 200, v2DocListResponse{Data: []v2Doc{
			{
				ID: docID, Type: "Doc", Status: "1", Title: "Doc", Slug: "d", BookID: book.id,
				ContentUpdatedAt: testTS,
			},
		}})
		f.handleJSON("/api/v2/repos/"+itoa(book.id)+"/toc", 200, v2TOCResponse{Data: []v2TOCNode{
			tocTitle("t1", "", "Shared"),
			tocDoc("d1", "t1", docID, "Doc"),
		}})
		f.handleJSON(docDetailPath(docID), 200, v2DocDetailResponse{Data: v2DocDetail{
			ID: docID, Title: "Doc", Body: "x", Format: "markdown", Status: "1",
			ContentUpdatedAt: testTS,
			Book:             v2Repo{ID: book.id, Name: book.name, Namespace: "alice/b"},
		}})
	}

	items, err := NewConnector().FetchAll(context.Background(),
		makeDSConfigWithSettings(f, []string{"7", "8"}, map[string]interface{}{"folder_mode": "toc"}),
		[]string{"7", "8"})
	if err != nil {
		t.Fatalf("FetchAll: %v", err)
	}
	got := fileNames(items)
	if got["70"] != "Book One/Shared/Doc.md" {
		t.Errorf("book 7: FileName = %q, want %q", got["70"], "Book One/Shared/Doc.md")
	}
	if got["80"] != "Book Two/Shared/Doc.md" {
		t.Errorf("book 8: FileName = %q, want %q", got["80"], "Book Two/Shared/Doc.md")
	}
}
