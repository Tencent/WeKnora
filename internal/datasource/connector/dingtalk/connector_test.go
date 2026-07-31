package dingtalk

import (
	"testing"
	"time"
)

func TestParseResourceID(t *testing.T) {
	kind, spaceID, fileID := parseResourceID(resourceID(resourceKindSpace, "space-1", ""))
	if kind != resourceKindSpace || spaceID != "space-1" || fileID != "" {
		t.Fatalf("unexpected space id parse: kind=%q spaceID=%q fileID=%q", kind, spaceID, fileID)
	}

	kind, spaceID, fileID = parseResourceID(resourceID(resourceKindFile, "space-1", "file-1"))
	if kind != resourceKindFile || spaceID != "space-1" || fileID != "file-1" {
		t.Fatalf("unexpected file id parse: kind=%q spaceID=%q fileID=%q", kind, spaceID, fileID)
	}

	kind, spaceID, fileID = parseResourceID("bad-id")
	if kind != "" || spaceID != "" || fileID != "" {
		t.Fatalf("invalid id should parse empty values, got kind=%q spaceID=%q fileID=%q", kind, spaceID, fileID)
	}
}

func TestSelectedEntries(t *testing.T) {
	entries := []driveEntry{
		{ID: "folder-a", ParentID: "0", Name: "A", Type: "folder"},
		{ID: "doc-root", ParentID: "0", Name: "root.md"},
		{ID: "doc-a", ParentID: "folder-a", Name: "a.md"},
		{ID: "folder-b", ParentID: "folder-a", Name: "B", Type: "folder"},
		{ID: "doc-b", ParentID: "folder-b", Name: "b.md"},
	}

	all := selectedEntries(entries, resourceKindSpace, "")
	if ids(all) != "doc-root,doc-a,doc-b" {
		t.Fatalf("space selection mismatch: %s", ids(all))
	}

	folder := selectedEntries(entries, resourceKindFile, "folder-a")
	if ids(folder) != "doc-a,doc-b" {
		t.Fatalf("folder selection mismatch: %s", ids(folder))
	}

	file := selectedEntries(entries, resourceKindFile, "doc-a")
	if ids(file) != "doc-a" {
		t.Fatalf("file selection mismatch: %s", ids(file))
	}
}

func TestParseDriveEntry(t *testing.T) {
	raw := map[string]interface{}{
		"dentryId":       "file-1",
		"parentDentryId": "folder-1",
		"name":           "计划.md",
		"dentryType":     "file",
		"mediaType":      "text/markdown",
		"size":           float64(123),
		"modifiedTime":   "2026-07-29T10:20:30Z",
		"version":        "v1",
		"webUrl":         "https://alidocs.dingtalk.com/i/drive/space/file",
		"documentId":     "doc-key",
	}

	entry := parseDriveEntry(raw)
	if entry.ID != "file-1" || entry.ParentID != "folder-1" || entry.Name != "计划.md" {
		t.Fatalf("unexpected entry identity: %+v", entry)
	}
	if entry.Size != 123 || entry.Version != "v1" || entry.DocKey != "doc-key" {
		t.Fatalf("unexpected entry details: %+v", entry)
	}
	if entry.ModifiedAt.IsZero() || !entry.ModifiedAt.Equal(time.Date(2026, 7, 29, 10, 20, 30, 0, time.UTC)) {
		t.Fatalf("unexpected modified time: %s", entry.ModifiedAt)
	}
}

func ids(entries []driveEntry) string {
	out := ""
	for i, entry := range entries {
		if i > 0 {
			out += ","
		}
		out += entry.ID
	}
	return out
}
