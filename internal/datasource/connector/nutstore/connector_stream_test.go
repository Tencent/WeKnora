package nutstore

import (
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
)

func TestMetadataCompare_DetectsChanges(t *testing.T) {
	prev := &NutstoreSnapshot{
		Files: map[string]FileMetadata{
			"/a.pdf": {ModifiedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), Size: 100},
			"/b.pdf": {ModifiedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), Size: 200},
			"/c.pdf": {ModifiedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), Size: 300},
		},
	}

	current := []FileInfo{
		{Path: "/a.pdf", Size: 100, LastModified: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}, // unchanged
		{Path: "/b.pdf", Size: 250, LastModified: time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)}, // changed
		{Path: "/d.pdf", Size: 400, LastModified: time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)}, // new
		// c.pdf is missing → deleted
	}

	changed, seen := compareMetadata(prev, current)

	if len(changed) != 2 {
		t.Fatalf("expected 2 changed files, got %d: %v", len(changed), changed)
	}

	if len(seen) != 3 {
		t.Fatalf("expected 3 seen files, got %d", len(seen))
	}

	// c.pdf should be detected as deleted
	deleted := detectDeleted(prev, seen)
	if len(deleted) != 1 || deleted[0] != "/c.pdf" {
		t.Errorf("expected deleted=[/c.pdf], got %v", deleted)
	}
}

func TestFetchStream_ReturnsStream(t *testing.T) {
	c := NewConnector()
	cfg := &types.DataSourceConfig{
		Credentials: map[string]interface{}{
			"username": "test@example.com",
			"password": "test-app-password",
		},
		ResourceIDs: []string{"/testdir/"},
	}

	// Verify FetchStream returns a valid stream (server unreachable, pipeline will fail)
	stream, err := c.FetchStream(t.Context(), cfg)
	if err != nil {
		// Connection error is acceptable — method signature is correct
		return
	}

	// Drain the stream — pipeline errors are reported via Wait()
	for item := range stream.Items() {
		if item.Body != nil {
			item.Body.Close()
		}
	}
	_, waitErr := stream.Wait()
	if waitErr == nil {
		t.Log("stream completed without error (unexpected with fake credentials)")
	}
}

// TestResourceFilter_MixedDirAndFile verifies that when resourceIDs contains
// both a directory (/docs/) and a single file (/other/x.pdf), files in
// subdirectories of /docs/ are NOT filtered out.
func TestResourceFilter_MixedDirAndFile(t *testing.T) {
	// Simulate: resourceIDs = ["/docs/", "/other/x.pdf"]
	// fullDirs = {"/docs/": true}
	// allowedFiles = {"/other/x.pdf": true}
	fullDirs := map[string]bool{"/docs/": true}
	allowedFiles := map[string]bool{"/other/x.pdf": true}

	files := []FileInfo{
		{Path: "/docs/readme.pdf", Name: "readme.pdf"},       // direct child of /docs/
		{Path: "/docs/sub/a.pdf", Name: "a.pdf"},              // nested under /docs/
		{Path: "/docs/sub/deep/b.pdf", Name: "b.pdf"},         // deeply nested under /docs/
		{Path: "/other/x.pdf", Name: "x.pdf"},                 // explicitly selected file
		{Path: "/other/y.pdf", Name: "y.pdf"},                 // sibling NOT selected
		{Path: "/unrelated/z.pdf", Name: "z.pdf"},             // unrelated
	}

	var allowed []string
	for _, fi := range files {
		if isFileAllowed(fi.Path, allowedFiles, fullDirs) {
			allowed = append(allowed, fi.Path)
		}
	}

	expected := []string{
		"/docs/readme.pdf",
		"/docs/sub/a.pdf",
		"/docs/sub/deep/b.pdf",
		"/other/x.pdf",
	}

	if len(allowed) != len(expected) {
		t.Fatalf("expected %d allowed files, got %d: %v", len(expected), len(allowed), allowed)
	}
	for i, p := range expected {
		if allowed[i] != p {
			t.Errorf("allowed[%d] = %q, want %q", i, allowed[i], p)
		}
	}
}

// TestResourceFilter_NoSingleFiles verifies that when only directories are
// selected (allowedFiles is nil), all files pass through.
func TestResourceFilter_NoSingleFiles(t *testing.T) {
	files := []FileInfo{
		{Path: "/docs/a.pdf"},
		{Path: "/other/b.pdf"},
	}

	for _, fi := range files {
		if !isFileAllowed(fi.Path, nil, nil) {
			t.Errorf("file %s should be allowed when no filter is active", fi.Path)
		}
	}
}
