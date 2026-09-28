package dingtalk

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/datasource"
	"github.com/Tencent/WeKnora/internal/types"
)

// sheetNode is a native DingTalk spreadsheet as the wiki API lists it: the
// ALIDOC category of adoc, but the axls extension read through the workbooks
// API.
func sheetNode(id, name string) node {
	return node{
		ID: id, WorkspaceID: "space", Name: name,
		Type: "FILE", Category: "ALIDOC", Extension: "axls",
		ModifiedTime: "2026-07-25T08:00:00Z",
	}
}

// workbookFixture is one axls node at the root of a one-workspace tree.
func workbookFixture(document node) *fakeAPI {
	return &fakeAPI{
		workspaces: []workspace{{ID: "space", RootNodeID: "root", Name: "Space"}},
		nodes:      map[string][]node{"root": {document}},
	}
}

// A spreadsheet has to be recognised as an ingestible document, not only as a
// sheet: the workspace scan and the picker both select nodes by isDocument(), so
// an unrecognised sheet would never be enumerated at all. The other native types
// and media stay out.
func TestSheetNodeClassification(t *testing.T) {
	for _, testCase := range []struct {
		label    string
		node     node
		sheet    bool
		document bool
	}{
		{"axls", sheetNode("book", "Book.axls"), true, true},
		{"uppercase", node{Type: "file", Category: "alidoc", Extension: "AXLS"}, true, true},
		{"adoc", node{Type: "FILE", Category: "ALIDOC", Extension: "adoc"}, false, true},
		{"able", node{Type: "FILE", Category: "ALIDOC", Extension: "able"}, false, false},
		{"amind", node{Type: "FILE", Category: "ALIDOC", Extension: "amind"}, false, false},
		{"adoc category only", node{Type: "FILE", Category: "ALIDOC"}, false, false},
		{"sheet in a folder", node{Type: "FOLDER", Category: "ALIDOC", Extension: "axls"}, false, false},
		{"media", node{Type: "FILE", Category: "VIDEO", Extension: "mp4"}, false, false},
	} {
		t.Run(testCase.label, func(t *testing.T) {
			if got := testCase.node.isSheet(); got != testCase.sheet {
				t.Fatalf("isSheet() = %v, want %v", got, testCase.sheet)
			}
			if got := testCase.node.isDocument(); got != testCase.document {
				t.Fatalf("isDocument() = %v, want %v", got, testCase.document)
			}
		})
	}
}

// One axls node is one document to its users and one knowledge entry here:
// every non-empty sheet is rendered into a single markdown item, each sheet
// under its own heading and followed by the shared table renderer's output.
func TestFetchAllRendersWorkbookAsOneDocument(t *testing.T) {
	api := workbookFixture(sheetNode("book-1", "Checklist.axls"))
	api.sheets = map[string][]sheet{
		"book-1": {
			{ID: "sheet-1", Name: "Summary"},
			{ID: "sheet-2", Name: "Details"},
		},
	}
	api.sheetInfos = map[string]sheet{
		sheetKey("book-1", "sheet-1"): {
			ID: "sheet-1", Name: "Summary", LastNonEmptyRow: 2, LastNonEmptyColumn: 2,
		},
		sheetKey("book-1", "sheet-2"): {
			ID: "sheet-2", Name: "Details", LastNonEmptyRow: 1, LastNonEmptyColumn: 1,
		},
	}
	api.sheetValues = map[string][][]string{
		sheetRangeKey("book-1", "sheet-1", "A1:B2"): {
			{"Item", "Value"},
			{"alpha", "ok"},
		},
		sheetRangeKey("book-1", "sheet-2", "A1:A1"): {{"detail"}},
	}

	items, err := testConnector(api).FetchAll(
		context.Background(), testConfig("space"), []string{"space"},
	)
	if err != nil {
		t.Fatalf("FetchAll() error = %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("FetchAll() returned %d items, want the workbook as one item: %#v", len(items), items)
	}

	item := items[0]
	want := "# Checklist.axls\n\n" +
		"## Summary\n\n" +
		"| Item | Value |\n" +
		"| --- | --- |\n" +
		"| alpha | ok |\n\n" +
		"## Details\n\n" +
		"| detail |\n" +
		"| --- |\n"
	if string(item.Content) != want {
		t.Fatalf("workbook markdown =\n%q\nwant\n%q", item.Content, want)
	}
	if item.ExternalID != "book-1" || item.Title != "Checklist.axls" ||
		item.ContentType != "text/markdown" || item.FileName != "Checklist.axls.md" ||
		item.SourceResourceID != "space" || item.Metadata["extension"] != "axls" ||
		item.Metadata["channel"] != types.ChannelDingtalk || item.URL == "" || item.UpdatedAt.IsZero() {
		t.Fatalf("workbook item = %#v", item)
	}
}

// Empty sheets must contribute no heading and no empty table. A sheet whose
// extent says it holds nothing is never requested at all; a sheet whose cells
// all come back blank is dropped once read.
func TestWorkbookOmitsEmptySheets(t *testing.T) {
	api := workbookFixture(sheetNode("book-1", "Book.axls"))
	api.sheets = map[string][]sheet{
		"book-1": {
			{ID: "sheet-void", Name: "Void"},
			{ID: "sheet-blank", Name: "Blank"},
			{ID: "sheet-data", Name: "Data"},
		},
	}
	api.sheetInfos = map[string]sheet{
		sheetKey("book-1", "sheet-void"): {
			ID: "sheet-void", Name: "Void", LastNonEmptyRow: -1, LastNonEmptyColumn: -1,
		},
		sheetKey("book-1", "sheet-blank"): {
			ID: "sheet-blank", Name: "Blank", LastNonEmptyRow: 3, LastNonEmptyColumn: 2,
		},
		sheetKey("book-1", "sheet-data"): {
			ID: "sheet-data", Name: "Data", LastNonEmptyRow: 2, LastNonEmptyColumn: 1,
		},
	}
	api.sheetValues = map[string][][]string{
		sheetRangeKey("book-1", "sheet-blank", "A1:B3"): {{"", ""}, {"", ""}, {"", ""}},
		sheetRangeKey("book-1", "sheet-data", "A1:A2"):  {{"name"}, {"alpha"}},
	}

	items, err := testConnector(api).FetchAll(
		context.Background(), testConfig("space"), []string{"space"},
	)
	if err != nil || len(items) != 1 {
		t.Fatalf("FetchAll() = %#v, %v; want one workbook item", items, err)
	}
	want := "# Book.axls\n\n## Data\n\n| name |\n| --- |\n| alpha |\n"
	if string(items[0].Content) != want {
		t.Fatalf("workbook markdown = %q, want %q", items[0].Content, want)
	}
	if strings.Count(string(items[0].Content), "| --- |") != 1 {
		t.Fatalf("empty sheet produced a table:\n%s", items[0].Content)
	}

	// The empty-extent sheet was never requested; the all-blank sheet was
	// requested because its extent claimed data, and then dropped.
	wantCalls := []string{
		sheetRangeKey("book-1", "sheet-blank", "A1:B3"),
		sheetRangeKey("book-1", "sheet-data", "A1:A2"),
	}
	if !reflect.DeepEqual(api.sheetRangeCalls, wantCalls) {
		t.Fatalf("ranges requests = %#v, want %#v", api.sheetRangeCalls, wantCalls)
	}
}

var sheetRangePattern = regexp.MustCompile(`^A(\d+):([A-Z]+)(\d+)$`)

// syntheticSheetRows answers a ranges window with one marker row per row, so a
// chunking test can see which window every row came from.
func syntheticSheetRows(_ string, _ string, ranges string) ([][]string, error) {
	match := sheetRangePattern.FindStringSubmatch(ranges)
	if match == nil {
		return nil, fmt.Errorf("unexpected ranges %q", ranges)
	}
	start, _ := strconv.Atoi(match[1])
	end, _ := strconv.Atoi(match[3])
	rows := make([][]string, 0, end-start+1)
	for row := start; row <= end; row++ {
		rows = append(rows, []string{fmt.Sprintf("r%d", row), "值"})
	}
	return rows, nil
}

// Cells are read in row windows bounded by lastNonEmptyRow/lastNonEmptyColumn:
// one request per window, never one giant range and never a row past the data.
func TestWorkbookReadsCellsInRowChunks(t *testing.T) {
	for _, testCase := range []struct {
		label      string
		lastRow    int
		lastColumn int
		wantRanges []string
	}{
		{"single window", 2, 2, []string{"A1:B2"}},
		{"exact multiple of the chunk", 400, 3, []string{"A1:C200", "A201:C400"}},
		{"partial last window", 450, 3, []string{"A1:C200", "A201:C400", "A401:C450"}},
		{"wider than column Z", 6, 40, []string{"A1:AN6"}},
	} {
		t.Run(testCase.label, func(t *testing.T) {
			api := workbookFixture(sheetNode("book-1", "Book.axls"))
			api.sheets = map[string][]sheet{"book-1": {{ID: "sheet-1", Name: "Data"}}}
			api.sheetInfos = map[string]sheet{
				sheetKey("book-1", "sheet-1"): {
					ID:                 "sheet-1",
					LastNonEmptyRow:    testCase.lastRow,
					LastNonEmptyColumn: testCase.lastColumn,
				},
			}
			api.sheetRangeFunc = syntheticSheetRows

			items, err := testConnector(api).FetchAll(
				context.Background(), testConfig("space"), []string{"space"},
			)
			if err != nil || len(items) != 1 {
				t.Fatalf("FetchAll() = %#v, %v", items, err)
			}

			wantCalls := make([]string, len(testCase.wantRanges))
			for i, ranges := range testCase.wantRanges {
				wantCalls[i] = sheetRangeKey("book-1", "sheet-1", ranges)
			}
			if !reflect.DeepEqual(api.sheetRangeCalls, wantCalls) {
				t.Fatalf("ranges requests = %#v, want %#v", api.sheetRangeCalls, wantCalls)
			}
			content := string(items[0].Content)
			for _, row := range []int{1, testCase.lastRow} {
				if !strings.Contains(content, fmt.Sprintf("| r%d | 值 |", row)) {
					t.Fatalf("row %d missing from the workbook:\n%s", row, content)
				}
			}
		})
	}
}

// The caps are not allowed to shorten a sheet quietly: the capped sheet is
// still ingested up to the cap — truncation is deterministic, not a failure —
// no window is ever requested past the cap, and the exact extent left unread is
// reported for the sync log.
func TestWorkbookCapsReportTheDroppedExtent(t *testing.T) {
	api := workbookFixture(sheetNode("book-1", "Big.axls"))
	api.sheets = map[string][]sheet{
		"book-1": {
			{ID: "sheet-rows", Name: "Big"},
			{ID: "sheet-cols", Name: "Wide"},
		},
	}
	api.sheetInfos = map[string]sheet{
		sheetKey("book-1", "sheet-rows"): {
			ID: "sheet-rows", LastNonEmptyRow: maxSheetRows + 50, LastNonEmptyColumn: 2,
		},
		sheetKey("book-1", "sheet-cols"): {
			ID: "sheet-cols", LastNonEmptyRow: 1, LastNonEmptyColumn: maxSheetColumns + 4,
		},
	}
	api.sheetRangeFunc = syntheticSheetRows

	items, err := testConnector(api).FetchAll(
		context.Background(), testConfig("space"), []string{"space"},
	)
	if err != nil || len(items) != 1 {
		t.Fatalf("FetchAll() = %#v, %v; want the capped workbook to sync", items, err)
	}
	content := string(items[0].Content)
	if !strings.Contains(content, "| r10000 | 值 |") || strings.Contains(content, "r10001") {
		t.Fatalf("capped sheet did not stop at row %d", maxSheetRows)
	}

	// The window sequence stops at the cap: no request asks for row 10001.
	wantCalls := make([]string, 0, maxSheetRows/sheetRowChunk+1)
	for start := 1; start <= maxSheetRows; start += sheetRowChunk {
		end := min(start+sheetRowChunk-1, maxSheetRows)
		wantCalls = append(wantCalls,
			sheetRangeKey("book-1", "sheet-rows", fmt.Sprintf("A%d:B%d", start, end)))
	}
	wantCalls = append(wantCalls, sheetRangeKey("book-1", "sheet-cols", "A1:IV1"))
	if !reflect.DeepEqual(api.sheetRangeCalls, wantCalls) {
		t.Fatalf("ranges requests = %#v, want %#v", api.sheetRangeCalls, wantCalls)
	}

	// The dropped extent is named exactly as the sync log has to carry it.
	rowCapped, err := readSheet(context.Background(), api, "book-1", "sheet-rows",
		api.sheetInfos[sheetKey("book-1", "sheet-rows")])
	if err != nil || !rowCapped.truncated() ||
		rowCapped.droppedExtents() != fmt.Sprintf("rows %d-%d", maxSheetRows+1, maxSheetRows+50) {
		t.Fatalf("row-capped read = truncated %t, dropped %q, %v",
			rowCapped.truncated(), rowCapped.droppedExtents(), err)
	}
	columnCapped, err := readSheet(context.Background(), api, "book-1", "sheet-cols",
		api.sheetInfos[sheetKey("book-1", "sheet-cols")])
	if err != nil || !columnCapped.truncated() ||
		columnCapped.droppedExtents() !=
			fmt.Sprintf("columns %d-%d", maxSheetColumns+1, maxSheetColumns+4) {
		t.Fatalf("column-capped read = truncated %t, dropped %q, %v",
			columnCapped.truncated(), columnCapped.droppedExtents(), err)
	}
}

// A sheet whose data all sits past a cap is still truncated data loss, even
// when everything inside the cap is blank: the read window comes back empty,
// the sheet contributes no heading, and the sync log is the only surface left
// that can tell an operator the table was cut. A connector that looks for
// content first and warns second drops such a sheet in complete silence.
func TestWorkbookWarnsAboutTruncatedSheetWithABlankReadWindow(t *testing.T) {
	for _, testCase := range []struct {
		label       string
		info        sheet
		wantDropped string
	}{
		{
			"rows past the cap",
			sheet{
				ID: "sheet-late", Name: "Late",
				LastNonEmptyRow: maxSheetRows + 50, LastNonEmptyColumn: 2,
			},
			fmt.Sprintf("rows %d-%d", maxSheetRows+1, maxSheetRows+50),
		},
		{
			"columns past the cap",
			sheet{
				ID: "sheet-late", Name: "Late",
				LastNonEmptyRow: 1, LastNonEmptyColumn: maxSheetColumns + 4,
			},
			fmt.Sprintf("columns %d-%d", maxSheetColumns+1, maxSheetColumns+4),
		},
	} {
		t.Run(testCase.label, func(t *testing.T) {
			logs := captureLogs(t)

			api := workbookFixture(sheetNode("book-1", "Book.axls"))
			api.sheets = map[string][]sheet{
				"book-1": {
					{ID: "sheet-late", Name: "Late"},
					{ID: "sheet-blank", Name: "Blank"},
				},
			}
			api.sheetInfos = map[string]sheet{
				sheetKey("book-1", "sheet-late"): testCase.info,
				// A sheet that is merely empty, with nothing past a cap, must
				// stay quiet: the warning is about the cap, not about blanks.
				sheetKey("book-1", "sheet-blank"): {
					ID: "sheet-blank", Name: "Blank", LastNonEmptyRow: 3, LastNonEmptyColumn: 2,
				},
			}
			// Every window inside the cap comes back blank: all the data is
			// past it.
			api.sheetRangeFunc = func(string, string, string) ([][]string, error) {
				return nil, nil
			}

			items, err := testConnector(api).FetchAll(
				context.Background(), testConfig("space"), []string{"space"},
			)
			if err != nil || len(items) != 1 {
				t.Fatalf("FetchAll() = %#v, %v; want the workbook item", items, err)
			}
			if content := string(items[0].Content); strings.Contains(content, "## Late") {
				t.Fatalf("a blank read window rendered a table:\n%s", content)
			}

			output := logs.String()
			want := fmt.Sprintf(
				"sheet %q in workbook book-1 exceeds the %d row x %d column ingest cap: dropped %s",
				"Late", maxSheetRows, maxSheetColumns, testCase.wantDropped,
			)
			if !strings.Contains(output, want) {
				t.Fatalf("sync log is missing the truncation warning %q:\n%s", want, output)
			}
			if strings.Contains(output, `sheet "Blank"`) {
				t.Fatalf("an empty sheet inside the cap was reported as truncated:\n%s", output)
			}
		})
	}
}

// A sheet read that fails — network, rate limit, revoked permission — is a
// transient workbook failure: the workbook surfaces as a failed item, stays out
// of the cursor, and syncs once the provider recovers.
func TestWorkbookReadFailureIsReportedAndRetried(t *testing.T) {
	api := workbookFixture(sheetNode("book-1", "Book.axls"))
	api.sheets = map[string][]sheet{"book-1": {{ID: "sheet-1", Name: "Data"}}}
	api.sheetInfos = map[string]sheet{
		sheetKey("book-1", "sheet-1"): {ID: "sheet-1", LastNonEmptyRow: 2, LastNonEmptyColumn: 1},
	}
	failingRange := sheetRangeKey("book-1", "sheet-1", "A1:A2")
	api.sheetRangeErrors = map[string]error{failingRange: errors.New("DingTalk API status=503")}

	cursorMap, err := encodeCursor(&cursorState{
		Version: cursorVersion, Resources: map[string]map[string]string{"space": {}},
	})
	if err != nil {
		t.Fatal(err)
	}

	items, next, syncErr := testConnector(api).FetchIncremental(
		context.Background(), testConfig("space"), &types.SyncCursor{ConnectorCursor: cursorMap},
	)
	var partial *datasource.PartialFetchError
	if !errors.As(syncErr, &partial) {
		t.Fatalf("FetchIncremental() error = %v, want PartialFetchError", syncErr)
	}
	if len(items) != 1 || items[0].ExternalID != "book-1" ||
		items[0].Metadata["error_reason_code"] != "dingtalk_document_failed" ||
		!strings.Contains(items[0].Metadata["error"], "status=503") || len(items[0].Content) != 0 {
		t.Fatalf("failure item = %#v", items)
	}
	state, err := decodeCursor(next)
	if err != nil {
		t.Fatal(err)
	}
	if revision, stored := state.Resources["space"]["book-1"]; stored && revision != "" {
		t.Fatalf("failed workbook advanced the cursor: %#v", state.Resources["space"])
	}

	// The same workbook succeeds once the provider recovers.
	delete(api.sheetRangeErrors, failingRange)
	api.sheetValues = map[string][][]string{failingRange: {{"name"}, {"alpha"}}}
	items, _, err = testConnector(api).FetchIncremental(
		context.Background(), testConfig("space"), &types.SyncCursor{ConnectorCursor: cursorMap},
	)
	if err != nil || len(items) != 1 ||
		!strings.Contains(string(items[0].Content), "| name |") {
		t.Fatalf("retry result = %#v, %v", items, err)
	}
}

// Validate proves a workspace that exposes spreadsheets, through the same
// sheets API the sync uses — and still reports a workbook it cannot read.
func TestValidateProbesSheetNodesThroughTheSheetsAPI(t *testing.T) {
	api := workbookFixture(sheetNode("book-1", "Book.axls"))
	api.sheets = map[string][]sheet{"book-1": {{ID: "sheet-1", Name: "Data"}}}

	if err := testConnector(api).Validate(context.Background(), testConfig()); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if api.sheetListCalls["book-1"] != 1 || len(api.blockCalls) != 0 {
		t.Fatalf("sheet list calls = %#v, block calls = %#v", api.sheetListCalls, api.blockCalls)
	}

	api.sheetListErrors = map[string]error{"book-1": errors.New("forbidden.operationIllegal")}
	err := testConnector(api).Validate(context.Background(), testConfig())
	if err == nil || !strings.Contains(err.Error(), "forbidden.operationIllegal") {
		t.Fatalf("Validate() error = %v, want the provider refusal", err)
	}
}

// The ranges API names columns with A1 letters, so a sheet wider than Z must be
// addressed with AA..IV instead of being cut at the first window.
func TestColumnNameCoversSheetsWiderThanColumnZ(t *testing.T) {
	for _, testCase := range []struct {
		index int
		want  string
	}{
		{1, "A"},
		{2, "B"},
		{26, "Z"},
		{27, "AA"},
		{28, "AB"},
		{40, "AN"},
		{52, "AZ"},
		{53, "BA"},
		{256, "IV"},
		{0, "A"},
		{-3, "A"},
	} {
		if got := columnName(testCase.index); got != testCase.want {
			t.Fatalf("columnName(%d) = %q, want %q", testCase.index, got, testCase.want)
		}
	}
}
