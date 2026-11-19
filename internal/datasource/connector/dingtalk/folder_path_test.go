package dingtalk

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

// The fixture below reproduces the shape of the tenant that reported the
// problem: one workspace whose root holds five folders, a document that sits
// directly at the root, both formats inside the folders (native adoc FAQ pages
// and uploaded docx/pptx manuals), an empty folder, and one extra nesting level
// so the depth is not uniform. It goes through the public fetch entry points
// only, so every assertion describes what an operator sees in the knowledge
// base rather than how the connector is wired internally.
//
// Node names carry no file extension: DingTalk reports the extension in its own
// field, exactly as the rest of this package's fixtures do.
const (
	tenantWorkspaceID   = "ws-tianqiong"
	tenantWorkspaceName = "天穹安全能力中心-天系列使用FAQ"
	tenantRootNodeID    = "root-tianqiong"
	// tenantModifiedAt is the millisecond revision DingTalk reports for the
	// fixture's documents. It is what makes an incremental sync skip an
	// untouched document, so the relocation case below is about the file name
	// and not about a missing revision.
	tenantModifiedAt = int64(1760000000000)
)

// tenantUploadedFilesConfig is the state of the reported tenant before this
// change: uploaded manuals are ingested, the folder-path switch does not exist
// yet, so the settings bag has no key for it.
func tenantUploadedFilesConfig(resources ...string) *types.DataSourceConfig {
	cfg := testConfig(resources...)
	cfg.Settings = map[string]interface{}{"include_uploaded_files": true}
	return cfg
}

// tenantFolderPathTestConfig turns the folder-path switch on as well.
func tenantFolderPathTestConfig(resources ...string) *types.DataSourceConfig {
	cfg := tenantUploadedFilesConfig(resources...)
	cfg.Settings["preserve_folder_structure"] = true
	return cfg
}

// tenantFolderPathFixture builds the reported tenant tree.
func tenantFolderPathFixture() *fakeAPI {
	adoc := func(id, name string) node {
		return node{
			ID: id, WorkspaceID: tenantWorkspaceID, Type: "FILE", Category: "ALIDOC",
			Extension: "adoc", Name: name, ModifiedTimestamp: tenantModifiedAt,
		}
	}
	upload := func(id, name, extension string) node {
		return node{
			ID: id, WorkspaceID: tenantWorkspaceID, Type: "FILE", Category: "DOCUMENT",
			Extension: extension, Name: name, ModifiedTimestamp: tenantModifiedAt,
		}
	}
	folder := func(id, name string) node {
		return node{
			ID: id, WorkspaceID: tenantWorkspaceID, Type: "FOLDER", Name: name, HasChildren: true,
		}
	}
	return &fakeAPI{
		workspaces: []workspace{{
			ID: tenantWorkspaceID, RootNodeID: tenantRootNodeID, Name: tenantWorkspaceName,
		}},
		nodes: map[string][]node{
			tenantRootNodeID: {
				folder("f-easm", "EASM-外部攻击面管理模块"),
				adoc("d-cloud-faq", "天穹云端安全能力中枢常见问题 FAQ"),
				folder("f-tianxi", "天析应急分析"),
				folder("f-tianyu", "天禹漏洞情报"),
				folder("f-tianfu", "天伏攻防知识库"),
				folder("f-tianzhu", "天诛渗透攻防"),
			},
			"f-easm": {
				adoc("d-easm-faq", "EASM 模块使用 FAQ"),
				upload("d-easm-manual", "EASM 模块操作手册.docx", "docx"),
			},
			"f-tianxi": {
				adoc("d-tianxi-faq", "天析应急分析 FAQ"),
				upload("d-tianxi-deck", "天析应急分析介绍.pptx", "pptx"),
			},
			"f-tianyu": {
				upload("d-tianyu-manual", "天禹漏洞情报使用手册.docx", "docx"),
				upload("d-tianyu-deck", "天禹漏洞情报介绍.pptx", "pptx"),
			},
			// 天伏攻防知识库 is listed but holds nothing: an empty folder must
			// stay invisible in the knowledge base instead of producing a level
			// with no documents in it.
			"f-tianfu": {},
			"f-tianzhu": {
				upload("d-tianzhu-manual", "天诛渗透攻防手册.docx", "docx"),
				adoc("d-tianzhu-faq-1", "天诛渗透攻防 FAQ 一"),
				adoc("d-tianzhu-faq-2", "天诛渗透攻防 FAQ 二"),
				folder("f-tianzhu-2024", "2024 年度"),
			},
			"f-tianzhu-2024": {
				adoc("d-tianzhu-2024-faq", "2024 年度复盘 FAQ"),
			},
		},
		blocks: map[string][]json.RawMessage{
			"d-cloud-faq":     {rawJSON(`{"blockType":"paragraph","paragraph":{"text":"cloud"}}`)},
			"d-easm-faq":      {rawJSON(`{"blockType":"paragraph","paragraph":{"text":"easm"}}`)},
			"d-tianxi-faq":    {rawJSON(`{"blockType":"paragraph","paragraph":{"text":"tianxi"}}`)},
			"d-tianzhu-faq-1": {rawJSON(`{"blockType":"paragraph","paragraph":{"text":"one"}}`)},
			"d-tianzhu-faq-2": {rawJSON(`{"blockType":"paragraph","paragraph":{"text":"two"}}`)},
			"d-tianzhu-2024-faq": {
				rawJSON(`{"blockType":"paragraph","paragraph":{"text":"review"}}`),
			},
		},
		downloads: map[string][]byte{
			"d-easm-manual":    []byte("easm"),
			"d-tianxi-deck":    []byte("tianxi"),
			"d-tianyu-manual":  []byte("tianyu-manual"),
			"d-tianyu-deck":    []byte("tianyu-deck"),
			"d-tianzhu-manual": []byte("tianzhu"),
		},
		nodeErrors:  make(map[string]error),
		blockErrors: make(map[string]error),
	}
}

// fileNamesByDocument indexes the fetched items by node ID so an assertion can
// name the document it is talking about instead of an index.
func fileNamesByDocument(t *testing.T, items []types.FetchedItem) map[string]string {
	t.Helper()
	names := make(map[string]string, len(items))
	for _, item := range items {
		if _, seen := names[item.ExternalID]; seen {
			t.Fatalf("document %q was fetched more than once", item.ExternalID)
		}
		names[item.ExternalID] = item.FileName
	}
	return names
}

func assertFileNames(t *testing.T, got map[string]string, want map[string]string) {
	t.Helper()
	for id, wantName := range want {
		if got[id] != wantName {
			t.Errorf("document %s FileName = %q, want %q", id, got[id], wantName)
		}
	}
	if len(got) != len(want) {
		t.Fatalf("fetched %d documents, want %d: %#v", len(got), len(want), got)
	}
}

// Regression guard: a data source that never turned the switch on keeps the
// historical flat file names, byte for byte. Every document of the reported
// tenant is listed, in the same format it was ingested with before.
func TestDingTalkFolderPathsDefaultOffKeepsHistoricalFileNames(t *testing.T) {
	captureLogs(t)
	connector := testConnector(tenantFolderPathFixture())
	items, err := connector.FetchAll(
		context.Background(), tenantUploadedFilesConfig(tenantWorkspaceID),
		[]string{tenantWorkspaceID},
	)
	if err != nil {
		t.Fatalf("FetchAll() error = %v", err)
	}
	assertFileNames(t, fileNamesByDocument(t, items), map[string]string{
		"d-easm-faq":         "EASM 模块使用 FAQ.md",
		"d-easm-manual":      "EASM 模块操作手册.docx",
		"d-cloud-faq":        "天穹云端安全能力中枢常见问题 FAQ.md",
		"d-tianxi-faq":       "天析应急分析 FAQ.md",
		"d-tianxi-deck":      "天析应急分析介绍.pptx",
		"d-tianyu-manual":    "天禹漏洞情报使用手册.docx",
		"d-tianyu-deck":      "天禹漏洞情报介绍.pptx",
		"d-tianzhu-manual":   "天诛渗透攻防手册.docx",
		"d-tianzhu-faq-1":    "天诛渗透攻防 FAQ 一.md",
		"d-tianzhu-faq-2":    "天诛渗透攻防 FAQ 二.md",
		"d-tianzhu-2024-faq": "2024 年度复盘 FAQ.md",
	})
}

// The reported problem, end to end: with the switch on, every document encodes
// its source folder hierarchy relative to the workspace root, so the five
// top-level folders stay distinguishable in the knowledge base. The workspace
// name is the first segment, and a document sitting at the root carries no
// folder segment beyond it.
func TestDingTalkFolderPathsPreserveTheTenantShapeWhenEnabled(t *testing.T) {
	captureLogs(t)
	connector := testConnector(tenantFolderPathFixture())
	items, err := connector.FetchAll(
		context.Background(),
		tenantFolderPathTestConfig(tenantWorkspaceID),
		[]string{tenantWorkspaceID},
	)
	if err != nil {
		t.Fatalf("FetchAll() error = %v", err)
	}
	root := tenantWorkspaceName + "/"
	assertFileNames(t, fileNamesByDocument(t, items), map[string]string{
		"d-easm-faq":         root + "EASM-外部攻击面管理模块/EASM 模块使用 FAQ.md",
		"d-easm-manual":      root + "EASM-外部攻击面管理模块/EASM 模块操作手册.docx",
		"d-cloud-faq":        root + "天穹云端安全能力中枢常见问题 FAQ.md",
		"d-tianxi-faq":       root + "天析应急分析/天析应急分析 FAQ.md",
		"d-tianxi-deck":      root + "天析应急分析/天析应急分析介绍.pptx",
		"d-tianyu-manual":    root + "天禹漏洞情报/天禹漏洞情报使用手册.docx",
		"d-tianyu-deck":      root + "天禹漏洞情报/天禹漏洞情报介绍.pptx",
		"d-tianzhu-manual":   root + "天诛渗透攻防/天诛渗透攻防手册.docx",
		"d-tianzhu-faq-1":    root + "天诛渗透攻防/天诛渗透攻防 FAQ 一.md",
		"d-tianzhu-faq-2":    root + "天诛渗透攻防/天诛渗透攻防 FAQ 二.md",
		"d-tianzhu-2024-faq": root + "天诛渗透攻防/2024 年度/2024 年度复盘 FAQ.md",
	})
}

// The switch has to read the same string spellings the connector's other switch
// reads, and anything unrecognised — including an absent key — has to leave the
// historical behaviour alone.
func TestDingTalkFolderPathsReadsTheSameSettingsSpellings(t *testing.T) {
	captureLogs(t)
	for _, testCase := range []struct {
		label    string
		settings map[string]interface{}
		want     string
	}{
		{
			"key absent",
			map[string]interface{}{"include_uploaded_files": true},
			"天诛渗透攻防 FAQ 一.md",
		},
		{
			"bool true",
			map[string]interface{}{"preserve_folder_structure": true},
			tenantWorkspaceName + "/天诛渗透攻防/天诛渗透攻防 FAQ 一.md",
		},
		{
			"string yes",
			map[string]interface{}{"preserve_folder_structure": "yes"},
			tenantWorkspaceName + "/天诛渗透攻防/天诛渗透攻防 FAQ 一.md",
		},
		{
			"string ON with padding",
			map[string]interface{}{"preserve_folder_structure": " ON "},
			tenantWorkspaceName + "/天诛渗透攻防/天诛渗透攻防 FAQ 一.md",
		},
		{
			"bool false",
			map[string]interface{}{"preserve_folder_structure": false},
			"天诛渗透攻防 FAQ 一.md",
		},
		{
			"unknown spelling",
			map[string]interface{}{"preserve_folder_structure": "sometimes"},
			"天诛渗透攻防 FAQ 一.md",
		},
		{
			"unexpected type",
			map[string]interface{}{"preserve_folder_structure": 42},
			"天诛渗透攻防 FAQ 一.md",
		},
	} {
		t.Run(testCase.label, func(t *testing.T) {
			cfg := tenantUploadedFilesConfig(tenantWorkspaceID)
			cfg.Settings = testCase.settings
			connector := testConnector(tenantFolderPathFixture())
			items, err := connector.FetchAll(
				context.Background(), cfg, []string{tenantWorkspaceID},
			)
			if err != nil {
				t.Fatalf("FetchAll() error = %v", err)
			}
			names := fileNamesByDocument(t, items)
			if names["d-tianzhu-faq-1"] != testCase.want {
				t.Fatalf("FileName = %q, want %q", names["d-tianzhu-faq-1"], testCase.want)
			}
		})
	}
}

// Selecting a folder is the common case, and the path a document lands on must
// not depend on how deep the operator drilled into the picker: the same
// document selected through the whole workspace, through its own folder and as
// a single document has to end up in one folder, not three.
func TestDingTalkFolderPathsDoNotDependOnTheSelectedScope(t *testing.T) {
	captureLogs(t)
	connector := testConnector(tenantFolderPathFixture())
	wholeWorkspace, err := connector.FetchAll(
		context.Background(),
		tenantFolderPathTestConfig(tenantWorkspaceID),
		[]string{tenantWorkspaceID},
	)
	if err != nil {
		t.Fatalf("workspace scope error = %v", err)
	}
	folderID, err := encodeResourceReference(resourceReference{
		WorkspaceID: tenantWorkspaceID, NodeID: "f-tianzhu-2024", Ancestors: []string{"f-tianzhu"},
	})
	if err != nil {
		t.Fatal(err)
	}
	nestedFolder, err := connector.FetchAll(
		context.Background(),
		tenantFolderPathTestConfig(folderID),
		[]string{folderID},
	)
	if err != nil {
		t.Fatalf("folder scope error = %v", err)
	}
	documentID, err := encodeResourceReference(resourceReference{
		WorkspaceID: tenantWorkspaceID, NodeID: "d-tianzhu-2024-faq",
		Ancestors: []string{"f-tianzhu", "f-tianzhu-2024"},
	})
	if err != nil {
		t.Fatal(err)
	}
	singleDocument, err := connector.FetchAll(
		context.Background(),
		tenantFolderPathTestConfig(documentID),
		[]string{documentID},
	)
	if err != nil {
		t.Fatalf("document scope error = %v", err)
	}

	want := tenantWorkspaceName + "/天诛渗透攻防/2024 年度/2024 年度复盘 FAQ.md"
	for label, items := range map[string][]types.FetchedItem{
		"workspace": wholeWorkspace,
		"folder":    nestedFolder,
		"document":  singleDocument,
	} {
		if got := fileNamesByDocument(t, items)["d-tianzhu-2024-faq"]; got != want {
			t.Errorf("%s scope FileName = %q, want %q", label, got, want)
		}
	}
}

// Names are hostile but not tidy: a folder name may contain the path separator,
// punctuation DingTalk allows and file systems do not, and two sibling folders
// may share a name. Each level is sanitised on its own so a "/" inside one name
// cannot invent an extra level, and same-named siblings merge into one folder —
// the same trade-off the Yuque connector documents for same-named groups.
func TestDingTalkFolderPathsSanitizeEachLevelAndMergeSameNamedFolders(t *testing.T) {
	captureLogs(t)
	const workspaceName = "安全/中心"
	adoc := func(id, name string) node {
		return node{
			ID: id, WorkspaceID: "ws-odd", Type: "FILE", Category: "ALIDOC",
			Extension: "adoc", Name: name,
		}
	}
	folder := func(id, name string) node {
		return node{
			ID: id, WorkspaceID: "ws-odd", Type: "FOLDER", Name: name, HasChildren: true,
		}
	}
	api := &fakeAPI{
		workspaces: []workspace{{ID: "ws-odd", RootNodeID: "root-odd", Name: workspaceName}},
		nodes: map[string][]node{
			"root-odd": {
				folder("f-slash", "A/B"),
				folder("f-punct", `<>:"|?*`),
				// Two folders with the same title: DingTalk node IDs are
				// unique, titles are not.
				folder("f-dup-1", "重复"),
				folder("f-dup-2", "重复"),
				// A name of only whitespace falls back to the node ID, which
				// is what the resource picker shows for it too.
				folder("f-unnamed", "   "),
			},
			"f-slash":   {adoc("d-slash", "A/B 手册")},
			"f-punct":   {adoc("d-punct", "符号")},
			"f-dup-1":   {adoc("d-dup-1", "第一份")},
			"f-dup-2":   {adoc("d-dup-2", "第二份")},
			"f-unnamed": {adoc("d-unnamed", "无名目录")},
		},
		blocks: map[string][]json.RawMessage{
			"d-slash":   {rawJSON(`{"blockType":"paragraph","paragraph":{"text":"a"}}`)},
			"d-punct":   {rawJSON(`{"blockType":"paragraph","paragraph":{"text":"b"}}`)},
			"d-dup-1":   {rawJSON(`{"blockType":"paragraph","paragraph":{"text":"c"}}`)},
			"d-dup-2":   {rawJSON(`{"blockType":"paragraph","paragraph":{"text":"d"}}`)},
			"d-unnamed": {rawJSON(`{"blockType":"paragraph","paragraph":{"text":"e"}}`)},
		},
		nodeErrors:  make(map[string]error),
		blockErrors: make(map[string]error),
	}
	connector := testConnector(api)
	items, err := connector.FetchAll(
		context.Background(), tenantFolderPathTestConfig("ws-odd"), []string{"ws-odd"},
	)
	if err != nil {
		t.Fatalf("FetchAll() error = %v", err)
	}
	// "安全/中心" cannot invent a level either, and neither can "A/B" or the
	// document name: the file name keeps the sanitised form it already had.
	assertFileNames(t, fileNamesByDocument(t, items), map[string]string{
		"d-slash":   "安全_中心/A_B/A_B 手册.md",
		"d-punct":   "安全_中心/_______/符号.md",
		"d-dup-1":   "安全_中心/重复/第一份.md",
		"d-dup-2":   "安全_中心/重复/第二份.md",
		"d-unnamed": "安全_中心/f-unnamed/无名目录.md",
	})
}

// Turning the switch on in an existing data source is an ingest-path change,
// not a migration: an incremental sync compares content revisions, so the
// documents already in the knowledge base are not re-read and therefore keep
// the flat file names they were ingested with — and, above all, the folder
// dimension must never make the connector report them as deleted.
func TestDingTalkEnablingFolderPathsDoesNotRelocateOrDeleteExistingDocuments(t *testing.T) {
	captureLogs(t)
	connector := testConnector(tenantFolderPathFixture())
	items, cursor, err := connector.FetchIncremental(
		context.Background(), tenantUploadedFilesConfig(tenantWorkspaceID), nil,
	)
	if err != nil {
		t.Fatalf("first FetchIncremental() error = %v", err)
	}
	if names := fileNamesByDocument(t, items); names["d-tianzhu-faq-1"] != "天诛渗透攻防 FAQ 一.md" {
		t.Fatalf("first sync FileName = %q, want the flat name", names["d-tianzhu-faq-1"])
	}

	items, _, err = connector.FetchIncremental(
		context.Background(), tenantFolderPathTestConfig(tenantWorkspaceID), cursor,
	)
	if err != nil {
		t.Fatalf("second FetchIncremental() error = %v", err)
	}
	for _, item := range items {
		if item.IsDeleted {
			t.Fatalf("enabling folder paths reported document %q as deleted", item.ExternalID)
		}
	}
	if len(items) != 0 {
		t.Fatalf("second sync returned %d items, want none: unchanged documents are not "+
			"re-read, so they keep the file name they were ingested with", len(items))
	}
}
