package dingtalk

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

// --- settings ---

// The folder switch is read through the same helper as the uploaded-files
// switch, and an absent, malformed or unexpected value has to leave the data
// source on the historical flat layout.
func TestParseDocumentSettingsReadsTheFolderSwitch(t *testing.T) {
	for _, testCase := range []struct {
		label    string
		settings map[string]interface{}
		want     bool
	}{
		{"absent key", map[string]interface{}{}, false},
		{"null settings entry", map[string]interface{}{"preserve_folder_structure": nil}, false},
		{"bool true", map[string]interface{}{"preserve_folder_structure": true}, true},
		{"bool false", map[string]interface{}{"preserve_folder_structure": false}, false},
		{"numeric string", map[string]interface{}{"preserve_folder_structure": "1"}, true},
		{"padded on", map[string]interface{}{"preserve_folder_structure": " on "}, true},
		{"padded OFF", map[string]interface{}{"preserve_folder_structure": " OFF "}, false},
		{"unknown spelling", map[string]interface{}{"preserve_folder_structure": "maybe"}, false},
		{"unexpected type", map[string]interface{}{"preserve_folder_structure": 1}, false},
		{
			"independent of the uploaded-files switch",
			map[string]interface{}{"include_uploaded_files": true},
			false,
		},
	} {
		t.Run(testCase.label, func(t *testing.T) {
			dataSourceConfig := testConfig("ws")
			dataSourceConfig.Settings = testCase.settings
			got := parseDocumentSettings(dataSourceConfig)
			if got.PreserveFolderStructure != testCase.want {
				t.Fatalf("PreserveFolderStructure = %t, want %t", got.PreserveFolderStructure, testCase.want)
			}
			if got.IncludeUploadedFiles != settingBool(testCase.settings["include_uploaded_files"], false) {
				t.Fatalf("IncludeUploadedFiles = %t, the two switches are not independent",
					got.IncludeUploadedFiles)
			}
		})
	}
	if got := parseDocumentSettings(nil); got.PreserveFolderStructure {
		t.Fatal("parseDocumentSettings(nil).PreserveFolderStructure = true, want false")
	}
}

// documentFolders is the single place the switch is honoured, so the nil it
// returns when the switch is off is what keeps an existing data source
// byte-for-byte unchanged.
func TestDocumentFoldersIsNilUnlessTheSwitchIsOn(t *testing.T) {
	scope := []string{"天诛渗透攻防"}
	scan := []string{"2024 年度"}
	off := documentFolders(documentSettings{}, "工作区", scope, scan)
	if off != nil {
		t.Fatalf("documentFolders() with the switch off = %#v, want nil", off)
	}
	on := documentFolders(
		documentSettings{PreserveFolderStructure: true}, "工作区", scope, scan,
	)
	want := []string{"工作区", "天诛渗透攻防", "2024 年度"}
	if strings.Join(on, "|") != strings.Join(want, "|") {
		t.Fatalf("documentFolders() = %#v, want %#v", on, want)
	}
	// A workspace with no name of its own contributes no segment rather than an
	// empty level.
	unnamed := documentFolders(documentSettings{PreserveFolderStructure: true}, "", scope, nil)
	if strings.Join(unnamed, "|") != "天诛渗透攻防" {
		t.Fatalf("documentFolders() without a workspace name = %#v", unnamed)
	}
}

// --- file name assembly ---

// The folder path a synced document ends up in is derived downstream from
// FileName alone, so the encoding is only correct if that split recovers the
// hierarchy.
func TestBuildFolderFileNameSurvivesTheDownstreamFolderSplit(t *testing.T) {
	for _, testCase := range []struct {
		label      string
		folders    []string
		fileName   string
		wantFolder string
		wantName   string
	}{
		{
			"no folders keeps the historical shape",
			nil, "FAQ.md", "", "FAQ.md",
		},
		{
			"workspace only",
			[]string{"工作区"},
			"FAQ.md", "工作区", "FAQ.md",
		},
		{
			"workspace and nested folders",
			[]string{"天穹安全能力中心", "天诛渗透攻防", "2024 年度"},
			"2024 年度复盘 FAQ.md",
			"天穹安全能力中心/天诛渗透攻防/2024 年度",
			"2024 年度复盘 FAQ.md",
		},
		{
			"a separator inside a name cannot add a level",
			[]string{"安全/中心", "A/B"},
			"A_B 手册.md",
			"安全_中心/A_B",
			"A_B 手册.md",
		},
		{
			"a level the normaliser rejects is dropped, not fatal",
			[]string{"工作区", ".", "子目录"},
			"FAQ.md",
			"工作区/子目录",
			"FAQ.md",
		},
	} {
		t.Run(testCase.label, func(t *testing.T) {
			raw := buildFolderFileName(testCase.folders, testCase.fileName)
			folder, name := types.SplitKnowledgeRelativePath(raw)
			if folder != testCase.wantFolder || name != testCase.wantName {
				t.Fatalf("split(%q) = (%q, %q), want (%q, %q)",
					raw, folder, name, testCase.wantFolder, testCase.wantName)
			}
		})
	}
}

func TestBuildFolderFileNameWithoutFoldersIsTheIdentity(t *testing.T) {
	for _, name := range []string{"FAQ.md", "手册.docx", "untitled"} {
		if got := buildFolderFileName(nil, name); got != name {
			t.Fatalf("buildFolderFileName(nil, %q) = %q, want it unchanged", name, got)
		}
	}
}

// Sibling folders are recorded while the walk visits them, so a name must not
// be written into a slice another sibling is still holding.
func TestAppendFolderNeverSharesABackingArray(t *testing.T) {
	parent := make([]string, 1, 8)
	parent[0] = "工作区"
	first := appendFolder(parent, "天诛渗透攻防")
	second := appendFolder(parent, "天禹漏洞情报")
	if first[len(first)-1] != "天诛渗透攻防" {
		t.Fatalf("first sibling = %#v, its name was overwritten", first)
	}
	if second[len(second)-1] != "天禹漏洞情报" {
		t.Fatalf("second sibling = %#v, its name was overwritten", second)
	}
	if len(parent) != 1 || parent[0] != "工作区" {
		t.Fatalf("appendFolder() mutated its input: %#v", parent)
	}
}

// --- cursor safety ---

// The folder dimension is an ingest-path concern only. If it reached the sync
// cursor, turning the switch on would change what the next incremental sync
// considers unchanged — and, worse, which documents it considers deleted.
func TestFolderPathsNeverReachTheSyncCursor(t *testing.T) {
	_, off, err := testConnector(tenantFolderPathFixture()).FetchIncremental(
		context.Background(), tenantUploadedFilesConfig(tenantWorkspaceID), nil,
	)
	if err != nil {
		t.Fatalf("FetchIncremental() with the switch off error = %v", err)
	}
	_, on, err := testConnector(tenantFolderPathFixture()).FetchIncremental(
		context.Background(), tenantFolderPathTestConfig(tenantWorkspaceID), nil,
	)
	if err != nil {
		t.Fatalf("FetchIncremental() with the switch on error = %v", err)
	}
	encode := func(t *testing.T, cursor *types.SyncCursor) string {
		t.Helper()
		if cursor == nil {
			t.Fatal("FetchIncremental() returned no cursor")
		}
		raw, err := json.Marshal(cursor.ConnectorCursor["resources"])
		if err != nil {
			t.Fatal(err)
		}
		return string(raw)
	}
	offCursor, onCursor := encode(t, off), encode(t, on)
	if offCursor != onCursor {
		t.Fatalf("the cursor depends on the folder switch:\noff: %s\non:  %s", offCursor, onCursor)
	}
	if strings.Contains(onCursor, tenantWorkspaceName) {
		t.Fatalf("the cursor carries folder names: %s", onCursor)
	}
}
