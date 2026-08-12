package ima

import (
	"context"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/datasource"
	"github.com/Tencent/WeKnora/internal/types"
)

func TestConnector_Type(t *testing.T) {
	if got := NewConnector().Type(); got != types.ConnectorTypeIMA {
		t.Errorf("Type() = %q, want %q", got, types.ConnectorTypeIMA)
	}
}

// TestFetchIncremental_TransientFailureIsRetried is the regression test for the
// cursor bug: the cursor used to be built from the raw listing before any
// content was fetched, so an item whose download failed was recorded as
// "seen with this media_id" and every later incremental sync skipped it as
// unchanged. The document was then silently missing from the KB forever.
func TestFetchIncremental_TransientFailureIsRetried(t *testing.T) {
	f := newFakeIMA(t)
	good := fakeFile{MediaID: "m-good", Title: "Good", MediaType: mediaTypeMarkdown, Body: "# ok"}
	flaky := fakeFile{
		MediaID: "m-flaky", Title: "Flaky", MediaType: mediaTypeMarkdown,
		Body: "# later", DownloadFails: true,
	}
	f.setKB("kb1", []fakeFile{good, flaky})

	c := NewConnector()
	cfg := f.config("kb1")
	goodKey := logicalKey("kb1", "", "Good")
	flakyKey := logicalKey("kb1", "", "Flaky")

	items, cursor, err := c.FetchIncremental(context.Background(), cfg, nil)
	if err != nil {
		t.Fatalf("first FetchIncremental: %v", err)
	}
	if _, ok := findItem(items, flakyKey); ok {
		t.Fatalf("failed download must not be emitted, got %s", describeItems(items))
	}
	mustFindItem(t, items, goodKey)

	state := decodeCursor(t, cursor)
	if _, recorded := state.KBLogical["kb1"][flakyKey]; recorded {
		t.Fatal("a failed item must not be recorded in the cursor, otherwise it is never retried")
	}
	if state.KBLogical["kb1"][goodKey] != "m-good" {
		t.Fatalf("successful item missing from cursor: %#v", state.KBLogical["kb1"])
	}

	// Second pass: the download now succeeds and the item must be re-attempted.
	flaky.DownloadFails = false
	f.setKB("kb1", []fakeFile{good, flaky})

	items2, cursor2, err := c.FetchIncremental(context.Background(), cfg, cursor)
	if err != nil {
		t.Fatalf("second FetchIncremental: %v", err)
	}
	retried := mustFindItem(t, items2, flakyKey)
	if string(retried.Content) != "# later" {
		t.Errorf("retried content = %q, want %q", retried.Content, "# later")
	}
	if _, ok := findItem(items2, goodKey); ok {
		t.Errorf("unchanged item must be skipped on the second pass, got %s", describeItems(items2))
	}
	if decodeCursor(t, cursor2).KBLogical["kb1"][flakyKey] != "m-flaky" {
		t.Error("the retried item should now be recorded in the cursor")
	}
}

// TestFetchIncremental_FailureIsNotADeletion guards the other half of the fix:
// keeping a failed item out of the cursor must not make the next sync mistake
// it for a document that vanished from IMA.
func TestFetchIncremental_FailureIsNotADeletion(t *testing.T) {
	f := newFakeIMA(t)
	good := fakeFile{MediaID: "m-good", Title: "Good", MediaType: mediaTypeMarkdown, Body: "# ok"}
	flaky := fakeFile{MediaID: "m-flaky", Title: "Flaky", MediaType: mediaTypeMarkdown, DownloadFails: true}
	f.setKB("kb1", []fakeFile{good, flaky})

	c := NewConnector()
	cfg := f.config("kb1")
	flakyKey := logicalKey("kb1", "", "Flaky")

	_, cursor, err := c.FetchIncremental(context.Background(), cfg, nil)
	if err != nil {
		t.Fatalf("first FetchIncremental: %v", err)
	}

	// Still failing, still listed by IMA: no tombstone may be emitted.
	items, _, err := c.FetchIncremental(context.Background(), cfg, cursor)
	if err != nil {
		t.Fatalf("second FetchIncremental: %v", err)
	}
	for _, it := range items {
		if it.IsDeleted && it.ExternalID == flakyKey {
			t.Fatalf("a still-listed item that merely failed to download must not be tombstoned: %s",
				describeItems(items))
		}
	}
}

func TestFetchIncremental_RemovedItemIsTombstoned(t *testing.T) {
	f := newFakeIMA(t)
	keep := fakeFile{MediaID: "m-keep", Title: "Keep", MediaType: mediaTypeMarkdown, Body: "# keep"}
	drop := fakeFile{MediaID: "m-drop", Title: "Drop", MediaType: mediaTypeMarkdown, Body: "# drop"}
	f.setKB("kb1", []fakeFile{keep, drop})

	c := NewConnector()
	cfg := f.config("kb1")
	dropKey := logicalKey("kb1", "", "Drop")

	_, cursor, err := c.FetchIncremental(context.Background(), cfg, nil)
	if err != nil {
		t.Fatalf("first FetchIncremental: %v", err)
	}

	f.setKB("kb1", []fakeFile{keep})
	items, _, err := c.FetchIncremental(context.Background(), cfg, cursor)
	if err != nil {
		t.Fatalf("second FetchIncremental: %v", err)
	}

	tombstone, ok := findItem(items, dropKey)
	if !ok || !tombstone.IsDeleted {
		t.Fatalf("removed item must be tombstoned, got %s", describeItems(items))
	}
	if tombstone.SourceResourceID != "kb1" {
		t.Errorf("tombstone SourceResourceID = %q, want kb1", tombstone.SourceResourceID)
	}
}

// TestFetchIncremental_SameNameReplacementKeepsExternalID covers IMA minting a
// new media_id when a file is replaced in place: the item must come back as an
// update under the same external id rather than a delete plus an insert.
func TestFetchIncremental_SameNameReplacementKeepsExternalID(t *testing.T) {
	f := newFakeIMA(t)
	f.setKB("kb1", []fakeFile{{MediaID: "m-v1", Title: "Doc", MediaType: mediaTypeMarkdown, Body: "v1"}})

	c := NewConnector()
	cfg := f.config("kb1")
	key := logicalKey("kb1", "", "Doc")

	_, cursor, err := c.FetchIncremental(context.Background(), cfg, nil)
	if err != nil {
		t.Fatalf("first FetchIncremental: %v", err)
	}

	f.setKB("kb1", []fakeFile{{MediaID: "m-v2", Title: "Doc", MediaType: mediaTypeMarkdown, Body: "v2"}})
	items, cursor2, err := c.FetchIncremental(context.Background(), cfg, cursor)
	if err != nil {
		t.Fatalf("second FetchIncremental: %v", err)
	}

	updated := mustFindItem(t, items, key)
	if updated.IsDeleted {
		t.Fatal("a same-name replacement must be an update, not a delete")
	}
	if string(updated.Content) != "v2" {
		t.Errorf("content = %q, want v2", updated.Content)
	}
	if updated.Metadata["media_id"] != "m-v2" {
		t.Errorf("metadata media_id = %q, want m-v2", updated.Metadata["media_id"])
	}
	for _, it := range items {
		if it.IsDeleted {
			t.Errorf("no tombstone expected, got %s", describeItems(items))
		}
	}
	if decodeCursor(t, cursor2).KBLogical["kb1"][key] != "m-v2" {
		t.Error("cursor should track the new media_id")
	}
}

// TestFetchIncremental_UnsupportedTypeIsProbedOnce checks that a deterministic
// skip is remembered, so an IMA knowledge base full of notes does not re-pay
// for get_media_info on every scheduled sync.
func TestFetchIncremental_UnsupportedTypeIsProbedOnce(t *testing.T) {
	f := newFakeIMA(t)
	f.setKB("kb1", []fakeFile{{MediaID: "m-note", Title: "Note", MediaType: mediaTypeNote}})

	c := NewConnector()
	cfg := f.config("kb1")

	items, cursor, err := c.FetchIncremental(context.Background(), cfg, nil)
	if err != nil {
		t.Fatalf("first FetchIncremental: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("note must be skipped, got %s", describeItems(items))
	}
	if f.callCount("get_media_info:m-note") != 1 {
		t.Fatalf("get_media_info calls = %d, want 1", f.callCount("get_media_info:m-note"))
	}

	if _, _, err := c.FetchIncremental(context.Background(), cfg, cursor); err != nil {
		t.Fatalf("second FetchIncremental: %v", err)
	}
	if got := f.callCount("get_media_info:m-note"); got != 1 {
		t.Errorf("get_media_info calls after second sync = %d, want 1 (skip should be cached)", got)
	}
}

// TestFetchAll_AuthenticatedURLIsDownloaded covers media types with no fixed
// extension. When IMA attaches auth headers the URL is IMA-hosted and WeKnora's
// own fetch could not authenticate, so the connector must download it here and
// forward the headers.
func TestFetchAll_AuthenticatedURLIsDownloaded(t *testing.T) {
	f := newFakeIMA(t)
	f.setKB("kb1", []fakeFile{{
		MediaID:     "m-web",
		Title:       "Article",
		MediaType:   mediaTypeWeb,
		Body:        "<html>body</html>",
		ContentType: "text/html",
		URLHeaders:  map[string]string{"X-Ima-Token": "secret"},
	}})

	items, err := NewConnector().FetchAll(context.Background(), f.config("kb1"), []string{"kb1"})
	if err != nil {
		t.Fatalf("FetchAll: %v", err)
	}
	item := mustFindItem(t, items, logicalKey("kb1", "", "Article"))
	if string(item.Content) != "<html>body</html>" {
		t.Errorf("content = %q, want the downloaded body", item.Content)
	}
	if !strings.HasSuffix(item.FileName, ".html") {
		t.Errorf("FileName = %q, want an .html suffix", item.FileName)
	}
	if got := f.downloadHeaders["m-web"].Get("X-Ima-Token"); got != "secret" {
		t.Errorf("auth header forwarded = %q, want %q", got, "secret")
	}
}

// TestFetchAll_PublicURLStaysURLOnly is the counterpart: without auth headers
// the link is publicly reachable, so it is handed to the ingest layer as a URL
// and WeKnora renders the live page instead of a snapshot.
func TestFetchAll_PublicURLStaysURLOnly(t *testing.T) {
	f := newFakeIMA(t)
	f.setKB("kb1", []fakeFile{{MediaID: "m-web", Title: "Article", MediaType: mediaTypeWeb, Body: "ignored"}})

	items, err := NewConnector().FetchAll(context.Background(), f.config("kb1"), []string{"kb1"})
	if err != nil {
		t.Fatalf("FetchAll: %v", err)
	}
	item := mustFindItem(t, items, logicalKey("kb1", "", "Article"))
	if len(item.Content) != 0 {
		t.Errorf("public URL must not be downloaded, got %d bytes", len(item.Content))
	}
	if item.URL == "" {
		t.Error("URL-only item must carry a URL")
	}
	if f.callCount("download:m-web") != 0 {
		t.Error("no download should have been attempted")
	}
}

// TestFetchAll_ImageExtensionFollowsContentType guards against naming a JPEG
// ".png": IMA reports every image as media_type=9, so only the download's
// Content-Type reveals the real format, and the extension drives the ingest
// pipeline's file-type checks.
func TestFetchAll_ImageExtensionFollowsContentType(t *testing.T) {
	f := newFakeIMA(t)
	f.setKB("kb1", []fakeFile{{
		MediaID:     "m-img",
		Title:       "Photo",
		MediaType:   mediaTypeImage,
		Body:        "\xff\xd8\xff\xe0jpeg",
		ContentType: "image/jpeg",
	}})

	items, err := NewConnector().FetchAll(context.Background(), f.config("kb1"), []string{"kb1"})
	if err != nil {
		t.Fatalf("FetchAll: %v", err)
	}
	item := mustFindItem(t, items, logicalKey("kb1", "", "Photo"))
	if !strings.HasSuffix(item.FileName, ".jpg") {
		t.Errorf("FileName = %q, want a .jpg suffix for an image/jpeg body", item.FileName)
	}
}

func TestFetchAll_SkipsItemWithoutURL(t *testing.T) {
	f := newFakeIMA(t)
	f.setKB("kb1", []fakeFile{{MediaID: "m-empty", Title: "Empty", MediaType: mediaTypePDF, NoURL: true}})

	items, err := NewConnector().FetchAll(context.Background(), f.config("kb1"), []string{"kb1"})
	if err != nil {
		t.Fatalf("FetchAll: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("item without a URL must be skipped, got %s", describeItems(items))
	}
}

// TestFetchAll_ResolvesFolderPath checks the mixed file/folder listing walk.
func TestFetchAll_ResolvesFolderPath(t *testing.T) {
	f := newFakeIMA(t)
	f.setKB("kb1",
		[]fakeFile{
			{MediaID: "m-root", Title: "Root", MediaType: mediaTypeMarkdown, Body: "root"},
			{MediaID: "m-deep", Title: "Deep", ParentFolderID: "f2", MediaType: mediaTypeMarkdown, Body: "deep"},
		},
		fakeFolder{FolderID: "f1", Name: "Outer"},
		fakeFolder{FolderID: "f2", Name: "Inner", ParentFolderID: "f1"},
	)

	items, err := NewConnector().FetchAll(context.Background(), f.config("kb1"), []string{"kb1"})
	if err != nil {
		t.Fatalf("FetchAll: %v", err)
	}

	deep := mustFindItem(t, items, logicalKey("kb1", "f2", "Deep"))
	if deep.Metadata["folder_path"] != "Outer/Inner" {
		t.Errorf("folder_path = %q, want Outer/Inner", deep.Metadata["folder_path"])
	}
	root := mustFindItem(t, items, logicalKey("kb1", "", "Root"))
	if root.Metadata["folder_path"] != "" {
		t.Errorf("root folder_path = %q, want empty", root.Metadata["folder_path"])
	}
}

func TestListResources_ReturnsAddableBases(t *testing.T) {
	f := newFakeIMA(t)
	f.setKB("kb-b", nil)
	f.setKB("kb-a", nil)

	resources, err := NewConnector().ListResources(context.Background(), f.config(), "")
	if err != nil {
		t.Fatalf("ListResources: %v", err)
	}
	if len(resources) != 2 {
		t.Fatalf("got %d resources, want 2", len(resources))
	}
	if resources[0].ExternalID != "kb-a" || resources[1].ExternalID != "kb-b" {
		t.Errorf("resources are not sorted by id: %+v", resources)
	}
	if resources[0].Description != "desc kb-a" {
		t.Errorf("description not enriched from get_knowledge_base: %q", resources[0].Description)
	}
}

// TestListResources_FallsBackToSearch covers tenants whose credential exposes
// read-only scopes, where get_addable_knowledge_base_list comes back empty.
func TestListResources_FallsBackToSearch(t *testing.T) {
	f := newFakeIMA(t)
	f.searchBases = []searchedKnowledgeBaseInfo{{ID: "kb-x", Name: "X", CoverURL: "https://cover"}}

	resources, err := NewConnector().ListResources(context.Background(), f.config(), "")
	if err != nil {
		t.Fatalf("ListResources: %v", err)
	}
	if len(resources) != 1 || resources[0].ExternalID != "kb-x" {
		t.Fatalf("expected the search fallback to surface kb-x, got %+v", resources)
	}
	if resources[0].Metadata["cover_url"] != "https://cover" {
		t.Errorf("cover_url = %v, want https://cover", resources[0].Metadata["cover_url"])
	}
}

func TestListResources_ChildrenAreEmpty(t *testing.T) {
	f := newFakeIMA(t)
	f.setKB("kb1", nil)

	resources, err := NewConnector().ListResources(context.Background(), f.config(), "kb1")
	if err != nil {
		t.Fatalf("ListResources: %v", err)
	}
	if len(resources) != 0 {
		t.Errorf("knowledge bases have no children, got %+v", resources)
	}
}

func TestValidate_InvalidCredentials(t *testing.T) {
	f := newFakeIMA(t)
	cfg := f.config()
	cfg.Credentials["client_id"] = ""

	err := NewConnector().Validate(context.Background(), cfg)
	if err == nil {
		t.Fatal("expected an error for a missing client_id")
	}
	if !strings.Contains(err.Error(), "client_id") {
		t.Errorf("error = %v, want it to name the missing field", err)
	}
}

func TestFetchIncremental_RequiresResourceIDs(t *testing.T) {
	f := newFakeIMA(t)
	if _, _, err := NewConnector().FetchIncremental(context.Background(), f.config(), nil); err == nil {
		t.Fatal("expected an error when no knowledge base is selected")
	}
}

func TestConnectorIsRegisteredInMetadata(t *testing.T) {
	meta, ok := datasource.ConnectorMetadataRegistry[types.ConnectorTypeIMA]
	if !ok {
		t.Fatal("IMA connector missing from the metadata registry")
	}
	if meta.Type != types.ConnectorTypeIMA {
		t.Errorf("metadata type = %q, want %q", meta.Type, types.ConnectorTypeIMA)
	}
}
