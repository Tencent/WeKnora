package dingtalk

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/datasource"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
)

// captureBaseLogs redirects the process logger into a buffer for one test. A
// capped table and a per-scope summary are log-only surfaces, so they are
// asserted on directly rather than through an internal counter.
func captureBaseLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buffer bytes.Buffer
	logger.SetOutput(&buffer)
	t.Cleanup(logger.ConfigureFromEnv)
	return &buffer
}

// baseReference encodes the third reference form the way a saved selection
// carries it.
func baseReference(t *testing.T, baseID string) string {
	t.Helper()
	resourceID, err := encodeResourceReference(resourceReference{BaseID: baseID})
	if err != nil {
		t.Fatal(err)
	}
	return resourceID
}

// notableBaseFixture is one two-table Base. Records are keyed by field name in
// map order that deliberately differs from the field listing, because only the
// listing order may reach the rendered table.
func notableBaseFixture() *fakeAPI {
	return &fakeAPI{
		notableTables: map[string][]notableTable{
			"base-1": {
				{ID: "tbl-1", Name: "清单"},
				{ID: "tbl-2", Name: "明细"},
			},
		},
		notableFields: map[string][]notableField{
			notableTableKey("base-1", "tbl-1"): {
				{ID: "f1", Name: "名称", Type: "text"},
				{ID: "f2", Name: "负责人", Type: "user"},
				{ID: "f3", Name: "优先级", Type: "singleSelect"},
				{ID: "f4", Name: "开始日期", Type: "date"},
				{ID: "f5", Name: "工时占比", Type: "progress"},
				{ID: "f6", Name: "关联任务", Type: "bidirectionalLink"},
			},
			notableTableKey("base-1", "tbl-2"): {
				{ID: "g1", Name: "名称", Type: "text"},
			},
		},
		notablePages: map[string]notableRecordPage{
			notablePageKey("base-1", "tbl-1", ""): {
				Records: []notableRecord{{
					ID: "r1",
					Fields: map[string]any{
						// A dense map is not enough: the API also returns a
						// missing member for an empty cell.
						"优先级":  map[string]any{"name": "高", "id": "opt-1"},
						"名称":   "示例条目",
						"负责人":  []any{map[string]any{"name": "张三"}},
						"开始日期": float64(1_761_321_600_000),
						"工时占比": "0.8",
						"关联任务": map[string]any{"linkedRecordIds": []any{"rec-1", "rec-2"}},
					},
				}},
				NextToken: "",
				HasMore:   false,
			},
			notablePageKey("base-1", "tbl-2", ""): {
				Records: []notableRecord{
					{ID: "r2", Fields: map[string]any{"名称": "另一条目"}},
					// A record whose field is absent renders as an empty cell.
					{ID: "r3", Fields: map[string]any{}},
				},
			},
		},
	}
}

// A Base is one document: one item carrying a heading per table, a markdown
// table per table with columns in field-listing order, and cell values reduced
// to what a reader sees in DingTalk rather than Go formatting.
func TestFetchAllRendersBaseAsOneDocument(t *testing.T) {
	api := notableBaseFixture()
	resourceID := baseReference(t, "base-1")

	items, err := testConnector(api).FetchAll(
		context.Background(), testConfig(resourceID), []string{resourceID},
	)
	if err != nil {
		t.Fatalf("FetchAll() error = %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("FetchAll() returned %d items, want the Base as one item: %#v", len(items), items)
	}

	item := items[0]
	// Cell and heading text is escaped like every other rendered value, so the
	// hyphens in the Base id, the date and the record ids carry backslashes.
	want := "# base\\-1\n\n" +
		"## 清单\n\n" +
		"| 名称 | 负责人 | 优先级 | 开始日期 | 工时占比 | 关联任务 |\n" +
		"| --- | --- | --- | --- | --- | --- |\n" +
		"| 示例条目 | 张三 | 高 | 2025\\-10\\-25 | 0.8 | rec\\-1, rec\\-2 |\n\n" +
		"## 明细\n\n" +
		"| 名称 |\n" +
		"| --- |\n" +
		"| 另一条目 |\n" +
		"|  |\n"
	if string(item.Content) != want {
		t.Fatalf("Base markdown =\n%q\nwant\n%q", item.Content, want)
	}
	if strings.Count(string(item.Content), "## ") != 2 {
		t.Fatalf("Base markdown does not hold one heading per table:\n%s", item.Content)
	}
	if strings.Contains(string(item.Content), "map[") {
		t.Fatalf("a raw Go value reached the document:\n%s", item.Content)
	}
	if item.ExternalID != "base-1" || item.Title != "base-1" ||
		item.ContentType != "text/markdown" || item.FileName != "base-1.md" ||
		item.SourceResourceID != resourceID || item.Metadata["base_id"] != "base-1" ||
		item.Metadata["extension"] != "able" || item.Metadata["channel"] != types.ChannelDingtalk {
		t.Fatalf("Base item = %#v", item)
	}
	wantCalls := []string{
		notableTableKey("base-1", "tbl-1") + "/",
		notableTableKey("base-1", "tbl-2") + "/",
	}
	if !reflect.DeepEqual(api.notableRecordCalls, wantCalls) {
		t.Fatalf("records requests = %#v, want %#v", api.notableRecordCalls, wantCalls)
	}
}

// A table larger than one page is read to the end: every nextToken is followed
// until the API reports no more records.
func TestBaseFollowsRecordPagination(t *testing.T) {
	api := notableBaseFixture()
	api.notableRecordsFunc = func(_, tableID, nextToken string) (notableRecordPage, error) {
		if tableID != "tbl-1" {
			return notableRecordPage{}, nil
		}
		switch nextToken {
		case "":
			return notableRecordPage{
				Records:   []notableRecord{{ID: "p1", Fields: map[string]any{"名称": "first"}}},
				NextToken: "page-2",
				HasMore:   true,
			}, nil
		case "page-2":
			return notableRecordPage{
				Records:   []notableRecord{{ID: "p2", Fields: map[string]any{"名称": "second"}}},
				NextToken: "page-3",
				HasMore:   true,
			}, nil
		case "page-3":
			return notableRecordPage{
				Records: []notableRecord{{ID: "p3", Fields: map[string]any{"名称": "third"}}},
			}, nil
		default:
			return notableRecordPage{}, fmt.Errorf("unexpected page token %q", nextToken)
		}
	}
	resourceID := baseReference(t, "base-1")

	items, err := testConnector(api).FetchAll(
		context.Background(), testConfig(resourceID), []string{resourceID},
	)
	if err != nil || len(items) != 1 {
		t.Fatalf("FetchAll() = %#v, %v", items, err)
	}
	content := string(items[0].Content)
	for _, want := range []string{"| first |", "| second |", "| third |"} {
		if !strings.Contains(content, want) {
			t.Fatalf("page %q is missing from the Base:\n%s", want, content)
		}
	}
	wantCalls := []string{
		notableTableKey("base-1", "tbl-1") + "/",
		notableTableKey("base-1", "tbl-1") + "/page-2",
		notableTableKey("base-1", "tbl-1") + "/page-3",
		notableTableKey("base-1", "tbl-2") + "/",
	}
	if !reflect.DeepEqual(api.notableRecordCalls, wantCalls) {
		t.Fatalf("records requests = %#v, want %#v", api.notableRecordCalls, wantCalls)
	}
}

// A repeated pagination token would loop forever, so it fails the table instead
// of spinning or stopping early.
func TestBaseRejectsRepeatedPaginationToken(t *testing.T) {
	api := notableBaseFixture()
	api.notableRecordsFunc = func(_, _, _ string) (notableRecordPage, error) {
		return notableRecordPage{
			Records:   []notableRecord{{ID: "p1", Fields: map[string]any{"名称": "row"}}},
			NextToken: "same",
			HasMore:   true,
		}, nil
	}
	resourceID := baseReference(t, "base-1")

	items, err := testConnector(api).FetchAll(
		context.Background(), testConfig(resourceID), []string{resourceID},
	)
	var partial *datasource.PartialFetchError
	if !errors.As(err, &partial) || len(items) != 1 ||
		!strings.Contains(items[0].Metadata["error"], "repeated nextToken") {
		t.Fatalf("FetchAll() = %#v, %v; want a repeated-token failure", items, err)
	}
}

// The cap is not allowed to shorten a table quietly: the Base still syncs up to
// the cap, but the sync log names the table and the extent that was dropped.
func TestBaseRowCapIsLoggedWithTheDroppedExtent(t *testing.T) {
	api := notableBaseFixture()
	// Two tables is noise here; one table of endless pages is the point, so
	// every page carries 100 synthetic records and always claims more.
	api.notableTables["base-1"] = []notableTable{{ID: "tbl-big", Name: "Big"}}
	api.notableFields = map[string][]notableField{
		notableTableKey("base-1", "tbl-big"): {{ID: "f1", Name: "名称", Type: "text"}},
	}
	api.notableRecordsFunc = func(_, _, nextToken string) (notableRecordPage, error) {
		page := 0
		if nextToken != "" {
			parsed, err := strconv.Atoi(strings.TrimPrefix(nextToken, "page-"))
			if err != nil {
				return notableRecordPage{}, err
			}
			page = parsed
		}
		records := make([]notableRecord, 0, notableRecordPageSize)
		for index := 0; index < notableRecordPageSize; index++ {
			number := page*notableRecordPageSize + index + 1
			records = append(records, notableRecord{
				ID:     fmt.Sprintf("r%d", number),
				Fields: map[string]any{"名称": fmt.Sprintf("row%d", number)},
			})
		}
		return notableRecordPage{
			Records:   records,
			NextToken: fmt.Sprintf("page-%d", page+1),
			HasMore:   true,
		}, nil
	}
	resourceID := baseReference(t, "base-1")

	logs := captureBaseLogs(t)
	items, err := testConnector(api).FetchAll(
		context.Background(), testConfig(resourceID), []string{resourceID},
	)
	if err != nil || len(items) != 1 {
		t.Fatalf("FetchAll() = %#v, %v; want the capped Base to sync", items, err)
	}
	content := string(items[0].Content)
	if !strings.Contains(content, fmt.Sprintf("| row%d |", maxBaseRecords)) ||
		strings.Contains(content, fmt.Sprintf("| row%d |", maxBaseRecords+1)) {
		t.Fatalf("capped table did not stop at record %d", maxBaseRecords)
	}
	if dataRows := strings.Count(content, "| row"); dataRows != maxBaseRecords {
		t.Fatalf("rendered %d records, want %d", dataRows, maxBaseRecords)
	}
	if pages := len(api.notableRecordCalls); pages != maxBaseRecords/notableRecordPageSize {
		t.Fatalf("read %d pages, want %d", pages, maxBaseRecords/notableRecordPageSize)
	}
	want := fmt.Sprintf(
		`[DingTalk] notable table "Big" in base base-1 exceeds the %d record ingest cap: dropped records %d and beyond`,
		maxBaseRecords, maxBaseRecords+1)
	if !strings.Contains(logs.String(), want) {
		t.Fatalf("sync log is missing:\n%s\ngot:\n%s", want, logs.String())
	}
}

// A notable read that fails — network, rate limit, revoked access — is a
// transient Base failure: it surfaces as a failed item, is logged as retryable,
// stays out of the cursor and syncs once the provider recovers.
func TestBaseReadFailureIsReportedAndRetried(t *testing.T) {
	api := notableBaseFixture()
	failing := errors.New("DingTalk API status=503")
	api.notableError = failing
	resourceID := baseReference(t, "base-1")

	cursorMap, err := encodeCursor(&cursorState{
		Version: cursorVersion, Resources: map[string]map[string]string{resourceID: {}},
	})
	if err != nil {
		t.Fatal(err)
	}

	logs := captureBaseLogs(t)
	items, next, syncErr := testConnector(api).FetchIncremental(
		context.Background(), testConfig(resourceID), &types.SyncCursor{ConnectorCursor: cursorMap},
	)
	var partial *datasource.PartialFetchError
	if !errors.As(syncErr, &partial) {
		t.Fatalf("FetchIncremental() error = %v, want PartialFetchError", syncErr)
	}
	if len(items) != 1 || items[0].ExternalID != "base-1" ||
		items[0].Metadata["error_reason_code"] != "dingtalk_document_failed" ||
		!strings.Contains(items[0].Metadata["error"], "status=503") || len(items[0].Content) != 0 {
		t.Fatalf("failure item = %#v", items)
	}
	output := logs.String()
	for _, want := range []string{
		"[DingTalk] read base base-1 failed, will retry next sync: DingTalk API status=503",
		"[DingTalk] scope base/base-1: total=1 synced=0 skipped=0 failed=1",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("sync log is missing %q:\n%s", want, output)
		}
	}
	state, err := decodeCursor(next)
	if err != nil {
		t.Fatal(err)
	}
	if _, stored := state.Resources[resourceID]["base-1"]; stored {
		t.Fatalf("failed Base advanced the cursor: %#v", state.Resources[resourceID])
	}

	// The same Base succeeds once the provider recovers.
	api.notableError = nil
	logs = captureBaseLogs(t)
	items, next, err = testConnector(api).FetchIncremental(
		context.Background(), testConfig(resourceID), &types.SyncCursor{ConnectorCursor: cursorMap},
	)
	if err != nil || len(items) != 1 || !strings.Contains(string(items[0].Content), "## 清单") {
		t.Fatalf("retry result = %#v, %v", items, err)
	}
	if !strings.Contains(logs.String(), "[DingTalk] scope base/base-1: total=1 synced=1 skipped=0 failed=0") {
		t.Fatalf("recovered Base was not summarised:\n%s", logs.String())
	}
	state, err = decodeCursor(next)
	if err != nil {
		t.Fatal(err)
	}
	if _, stored := state.Resources[resourceID]["base-1"]; !stored {
		t.Fatalf("synced Base is missing from the cursor: %#v", state.Resources[resourceID])
	}
}

// Two Bases share no workspace, so neither may be mistaken for a scope that
// covers the other.
func TestFetchAllKeepsTwoBaseSelectionsApart(t *testing.T) {
	api := notableBaseFixture()
	api.notableTables["base-2"] = []notableTable{{ID: "tbl-9", Name: "数据表 1"}}
	api.notableFields[notableTableKey("base-2", "tbl-9")] = []notableField{
		{ID: "h1", Name: "标题", Type: "primaryDoc"},
	}
	api.notablePages[notablePageKey("base-2", "tbl-9", "")] = notableRecordPage{
		Records: []notableRecord{{ID: "r9", Fields: map[string]any{"标题": "示例记录"}}},
	}
	first, second := baseReference(t, "base-1"), baseReference(t, "base-2")

	logs := captureBaseLogs(t)
	items, err := testConnector(api).FetchAll(
		context.Background(), testConfig(first, second), []string{first, second},
	)
	if err != nil || len(items) != 2 {
		t.Fatalf("FetchAll() = %#v, %v; want both Bases", items, err)
	}
	byID := make(map[string]types.FetchedItem, len(items))
	for _, item := range items {
		byID[item.ExternalID] = item
	}
	if !strings.Contains(string(byID["base-1"].Content), "示例条目") ||
		!strings.Contains(string(byID["base-2"].Content), "| 示例记录 |") {
		t.Fatalf("Base items = %#v", byID)
	}
	output := logs.String()
	for _, want := range []string{
		"[DingTalk] scope base/base-1: total=1 synced=1 skipped=0 failed=0",
		"[DingTalk] scope base/base-2: total=1 synced=1 skipped=0 failed=0",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("sync log is missing %q:\n%s", want, output)
		}
	}
}

// A Base lives outside the workspace tree, so a base-only selection neither
// lists workspaces nor needs one to expand or to be named. A Base is a wiki
// node all the same: the name comes from getNode and the children from the
// wiki-nodes API, both reached through the id the reference already carries.
func TestBaseSelectionDoesNotConsultTheWorkspaceTree(t *testing.T) {
	api := notableBaseFixture()
	api.getNodes = map[string]node{
		"base-1": {
			ID: "base-1", WorkspaceID: "personal-space", Name: "项目台账.able",
			Type: "FILE", Category: "ALIDOC", Extension: "able", HasChildren: true,
		},
	}
	api.nodes = map[string][]node{
		"base-1": {
			{
				ID: "base-child", WorkspaceID: "personal-space", Name: "说明.adoc",
				Type: "FILE", Category: "ALIDOC", Extension: "adoc",
			},
			// A Base is not offered as a tree child: only folders and wiki
			// documents are selectable here, and a Base is added through its
			// own base= reference.
			{
				ID: "nested-able", WorkspaceID: "personal-space", Name: "嵌套.able",
				Type: "FILE", Category: "ALIDOC", Extension: "able",
			},
		},
	}
	resourceID := baseReference(t, "base-1")
	connector := testConnector(api)

	items, err := connector.FetchAll(
		context.Background(), testConfig(resourceID), []string{resourceID},
	)
	if err != nil || len(items) != 1 {
		t.Fatalf("FetchAll() = %#v, %v", items, err)
	}
	// The title is the node's real name rather than the id: an id-titled entry
	// is unreadable in the knowledge base.
	if items[0].Title != "项目台账.able" || items[0].FileName != "项目台账.able.md" {
		t.Fatalf("Base item = %#v, want the node's name as title and file name", items[0])
	}

	resources, err := connector.ListResources(context.Background(), testConfig(), resourceID)
	if err != nil {
		t.Fatalf("ListResources() error = %v", err)
	}
	// Expanding a Base lists the wiki documents under it through the picker's
	// normal lazy tree. They are labelled as a distinct kind because the tables,
	// not these documents, are what a base= reference syncs.
	if len(resources) != 1 || resources[0].Name != "说明.adoc" ||
		resources[0].Type != resourceTypeBaseChild || resources[0].ParentID != resourceID {
		t.Fatalf("ListResources() = %#v, want the Base's wiki child", resources)
	}
	if api.workspaceCalls != 0 {
		t.Fatalf("workspace was listed %d times for a base-only selection", api.workspaceCalls)
	}
	// One node read, and it is the name lookup: the Base's content is never
	// resolved through the wiki tree.
	if api.getNodeCalls["base-1"] != 1 {
		t.Fatalf("node reads = %#v, want only the name lookup", api.getNodeCalls)
	}
}

// A saved base reference proves the credentials on its own: an unreadable Base
// is reported, and a readable one passes even when no workspace exposes a
// document.
func TestValidateTreatsABaseReferenceAsReadableContent(t *testing.T) {
	api := notableBaseFixture()
	resourceID := baseReference(t, "base-1")

	if err := testConnector(api).Validate(context.Background(), testConfig(resourceID)); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if api.notableTableCalls["base-1"] != 1 {
		t.Fatalf("table list calls = %#v, want one probe of the Base", api.notableTableCalls)
	}
	if api.workspaceCalls != 0 {
		t.Fatalf("base validation consulted the workspace listing %d times", api.workspaceCalls)
	}

	api.notableTableErrors = map[string]error{
		"base-1": errors.New("invalidRequest.document.notFound"),
	}
	err := testConnector(api).Validate(context.Background(), testConfig(resourceID))
	if err == nil || !strings.Contains(err.Error(), "invalidRequest.document.notFound") {
		t.Fatalf("Validate() error = %v, want the provider refusal", err)
	}
}

// The third reference form is a Base and nothing else: encoding and decoding
// keep it distinct from the workspace and node forms, and the invalid mixes are
// rejected rather than silently reinterpreted.
func TestResourceReferenceRoundTripsTheBaseForm(t *testing.T) {
	resourceID, err := encodeResourceReference(resourceReference{BaseID: "base-1"})
	if err != nil {
		t.Fatal(err)
	}
	if resourceID != resourceIDPrefix+"base=base-1" {
		t.Fatalf("encoded base reference = %q", resourceID)
	}
	decoded, err := decodeResourceReference(resourceID)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.BaseID != "base-1" || decoded.WorkspaceID != "" || decoded.NodeID != "" ||
		len(decoded.Ancestors) != 0 {
		t.Fatalf("decoded base reference = %#v", decoded)
	}
	ancestors, err := resourceAncestorIDs(decoded)
	if err != nil || len(ancestors) != 1 || ancestors[0] != resourceID {
		t.Fatalf("Base ancestors = %#v, %v; want the Base itself", ancestors, err)
	}

	for _, raw := range []string{
		resourceIDPrefix + "base=base-1&node=node-1",
		resourceIDPrefix + "base=base-1&workspace=space-1",
		resourceIDPrefix + "base=base-1&ancestor=node-1",
		resourceIDPrefix + "base=base-1&base=base-2",
		resourceIDPrefix + "base=",
		resourceIDPrefix + "base=%20",
	} {
		if _, err := decodeResourceReference(raw); err == nil {
			t.Fatalf("decodeResourceReference(%q) error = nil", raw)
		}
	}
	for _, ref := range []resourceReference{
		{BaseID: "base-1", NodeID: "node-1"},
		{BaseID: "base-1", WorkspaceID: "space-1"},
		{BaseID: "base-1", Ancestors: []string{"node-1"}},
	} {
		if _, err := encodeResourceReference(ref); err == nil {
			t.Fatalf("encodeResourceReference(%#v) error = nil", ref)
		}
	}
}

// Dates are the one cell type whose meaning depends on its definition: the API
// sends epoch milliseconds at DingTalk-local midnight, which must render as the
// day the user sees.
func TestNotableFieldTextRendersDatesAndScalars(t *testing.T) {
	date := notableField{Name: "开始日期", Type: "date"}
	for _, testCase := range []struct {
		label string
		field notableField
		value any
		want  string
	}{
		{"date number", date, float64(1_761_321_600_000), "2025-10-25"},
		{"date string", date, "1769788800000", "2026-01-31"},
		{"empty date", date, nil, ""},
		{"text", notableField{Type: "text"}, "示例条目", "示例条目"},
		{"progress", notableField{Type: "progress"}, "0.803921568627451", "0.803921568627451"},
		{"number", notableField{Type: "number"}, float64(26), "26"},
		{"boolean", notableField{Type: "checkbox"}, true, "true"},
		{
			"select",
			notableField{Type: "singleSelect"},
			map[string]any{"name": "高", "id": "opt-1"},
			"高",
		},
		{
			"user list",
			notableField{Type: "user"},
			[]any{map[string]any{"name": "张一"}, map[string]any{"name": "李二"}},
			"张一, 李二",
		},
		{
			"link ids",
			notableField{Type: "bidirectionalLink"},
			map[string]any{"linkedRecordIds": []any{"rec-2", "rec-1"}},
			"rec-2, rec-1",
		},
		{"unknown shape", notableField{Type: "text"}, struct{ A int }{1}, ""},
	} {
		t.Run(testCase.label, func(t *testing.T) {
			if got := notableFieldText(testCase.field, testCase.value); got != testCase.want {
				t.Fatalf("notableFieldText() = %q, want %q", got, testCase.want)
			}
		})
	}

	// The same instant rendered in UTC would be the previous day, which is
	// exactly what the fixed offset exists to prevent.
	if got := notableFieldText(date, float64(1_761_321_600_000)); got != "2025-10-25" ||
		got == "2025-10-24" {
		t.Fatalf("date rendering = %q", got)
	}
}
