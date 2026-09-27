package dingtalk

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

// A bare node reference — "dingtalk:v1?node=<id>", the form the manual entry
// field in the UI writes — names a node the picker can never list: a document
// in the operator's personal space is absent from GET /v2.0/wiki/workspaces,
// and a multi-dimensional table is not enumerable at all. Resolution therefore
// reads the node itself and adopts the workspace the node reports, so the scope
// reference, the cursor key and the document metadata all carry a workspace the
// reference never named.
func TestResolveSyncScopesAdoptsTheWorkspaceOfABareNode(t *testing.T) {
	api := &fakeAPI{
		workspaces: []workspace{{ID: "team", RootNodeID: "team-root", Name: "Team"}},
		getNodes: map[string]node{
			"personal-doc": {
				ID: "personal-doc", WorkspaceID: "personal-space",
				Name: "Plan.adoc", Type: "FILE", Category: "ALIDOC", Extension: "adoc",
			},
		},
	}
	resourceID, err := encodeResourceReference(resourceReference{NodeID: "personal-doc"})
	if err != nil {
		t.Fatal(err)
	}

	scopes, failures, err := resolveSyncScopes(context.Background(), api, nil, []string{resourceID})
	if err != nil {
		t.Fatalf("resolveSyncScopes() error = %v", err)
	}
	if len(failures) != 0 || len(scopes) != 1 {
		t.Fatalf("scopes = %#v, failures = %#v", scopes, failures)
	}
	scope := scopes[0]
	if scope.Reference.WorkspaceID != "personal-space" || scope.Reference.NodeID != "personal-doc" {
		t.Fatalf("scope reference = %#v", scope.Reference)
	}
	if scope.Document == nil || scope.Document.ID != "personal-doc" {
		t.Fatalf("scope document = %#v", scope.Document)
	}
	// The cursor key is the canonical reference, so the same node pasted by hand
	// and picked from the tree lands on one cursor entry.
	canonical, err := encodeResourceReference(resourceReference{
		WorkspaceID: "personal-space", NodeID: "personal-doc",
	})
	if err != nil {
		t.Fatal(err)
	}
	if scope.ResourceID != canonical {
		t.Fatalf("cursor key = %q, want %q", scope.ResourceID, canonical)
	}
	if api.getNodeCalls["personal-doc"] != 1 {
		t.Fatalf("node reads = %#v, want the node read by id", api.getNodeCalls)
	}
}

// A bare node selection is self-addressing: it names its own object, so it must
// keep working when the workspace listing is unavailable. That is the whole
// point of entering an id by hand — the tenant's listing cannot offer it.
func TestFetchAllReadsABareNodeWithoutTheWorkspaceListing(t *testing.T) {
	api := &fakeAPI{
		workspaces: []workspace{{ID: "team", RootNodeID: "team-root", Name: "Team"}},
		getNodes: map[string]node{
			"personal-doc": {
				ID: "personal-doc", WorkspaceID: "personal-space", Name: "Plan.adoc",
				Type: "FILE", Category: "ALIDOC", Extension: "adoc",
				ModifiedTime: "2026-07-25T08:00:00Z",
			},
		},
		blocks: map[string][]json.RawMessage{
			"personal-doc": {rawJSON(`{
				"blockType":"paragraph",
				"children":[{"elementType":"text","text":"Q3 goals"}]
			}`)},
		},
	}
	resourceID, err := encodeResourceReference(resourceReference{NodeID: "personal-doc"})
	if err != nil {
		t.Fatal(err)
	}

	items, cursor, err := testConnector(api).FetchAllFromCursor(
		context.Background(), testConfig(resourceID), []string{resourceID}, nil,
	)
	if err != nil {
		t.Fatalf("FetchAllFromCursor() error = %v", err)
	}
	if len(items) != 1 || items[0].ExternalID != "personal-doc" ||
		string(items[0].Content) != "# Plan.adoc\n\nQ3 goals\n" {
		t.Fatalf("items = %#v", items)
	}
	canonical, err := encodeResourceReference(resourceReference{
		WorkspaceID: "personal-space", NodeID: "personal-doc",
	})
	if err != nil {
		t.Fatal(err)
	}
	if items[0].SourceResourceID != canonical ||
		items[0].Metadata["workspace_id"] != "personal-space" ||
		items[0].Metadata["channel"] != types.ChannelDingtalk {
		t.Fatalf("item = %#v", items[0])
	}
	// Without the workspace adopted from the node, the cursor would be keyed by
	// a reference that names no workspace and could never be matched again.
	stored, ok := cursor.ConnectorCursor["resources"].(map[string]interface{})
	if !ok {
		t.Fatalf("cursor = %#v", cursor.ConnectorCursor)
	}
	if _, exists := stored[canonical]; !exists {
		t.Fatalf("cursor keys = %#v, want %q", stored, canonical)
	}
	if api.workspaceCalls != 0 {
		t.Fatalf("workspace listings = %d, want the bare node resolved by id alone", api.workspaceCalls)
	}
}

// Expanding a bare node that turns out to be a folder must hand back children
// whose ids embed the resolved workspace: they are what the picker sends back
// on the next expansion, and a child id without a workspace could not be
// resolved again.
func TestListResourcesExpandsABareNodeFolderWithItsResolvedWorkspace(t *testing.T) {
	api := &fakeAPI{
		getNodes: map[string]node{
			"personal-folder": {
				ID: "personal-folder", WorkspaceID: "personal-space",
				Name: "My documents", Type: "FOLDER", HasChildren: true,
			},
		},
		nodes: map[string][]node{
			"personal-folder": {{
				ID: "personal-doc", WorkspaceID: "personal-space", Name: "Plan.adoc",
				Type: "FILE", Category: "ALIDOC", Extension: "adoc",
			}},
		},
	}
	parentID, err := encodeResourceReference(resourceReference{NodeID: "personal-folder"})
	if err != nil {
		t.Fatal(err)
	}

	resources, err := testConnector(api).ListResources(context.Background(), testConfig(), parentID)
	if err != nil {
		t.Fatalf("ListResources() error = %v", err)
	}
	if len(resources) != 1 || resources[0].Type != "document" || resources[0].Name != "Plan.adoc" {
		t.Fatalf("ListResources() = %#v", resources)
	}
	if resources[0].Metadata["workspace_id"] != "personal-space" {
		t.Fatalf("child metadata = %#v", resources[0].Metadata)
	}
	ref, err := decodeResourceReference(resources[0].ExternalID)
	if err != nil {
		t.Fatalf("decodeResourceReference(%q) error = %v", resources[0].ExternalID, err)
	}
	if ref.WorkspaceID != "personal-space" || ref.NodeID != "personal-doc" ||
		len(ref.Ancestors) != 1 || ref.Ancestors[0] != "personal-folder" {
		t.Fatalf("child reference = %#v", ref)
	}
}

// A node reference is titled by the name the node reports, exactly as a listed
// document is; the id is only what is left when the API reports no name at all.
// An id-titled entry is unreadable, but a missing name must never fail a sync
// whose content is readable.
func TestBareNodeReferenceTitlesTheItemFromTheNodeName(t *testing.T) {
	for _, testCase := range []struct{ name, wantTitle, wantFile string }{
		{"Ledger.axls", "Ledger.axls", "Ledger.axls.md"},
		{"", "personal-doc", "personal-doc.md"},
	} {
		api := &fakeAPI{
			getNodes: map[string]node{
				"personal-doc": {
					ID: "personal-doc", WorkspaceID: "personal-space", Name: testCase.name,
					Type: "FILE", Category: "ALIDOC", Extension: "adoc",
				},
			},
			blocks: map[string][]json.RawMessage{
				"personal-doc": {rawJSON(`{
					"blockType":"paragraph",
					"children":[{"elementType":"text","text":"Q3 goals"}]
				}`)},
			},
		}
		resourceID, err := encodeResourceReference(resourceReference{NodeID: "personal-doc"})
		if err != nil {
			t.Fatal(err)
		}

		items, err := testConnector(api).FetchAll(
			context.Background(), testConfig(resourceID), []string{resourceID},
		)
		if err != nil || len(items) != 1 {
			t.Fatalf("FetchAll() = %#v, %v", items, err)
		}
		if items[0].Title != testCase.wantTitle || items[0].FileName != testCase.wantFile {
			t.Fatalf("item = %#v, want title %q and file %q",
				items[0], testCase.wantTitle, testCase.wantFile)
		}
	}
}

// A personal-space node is never part of the listed tree, so there is no
// ancestor chain the picker could expand to reveal it. Reporting none keeps the
// UI from asking to expand something it can never show. A Base keeps its own
// root: it is a whole selectable object addressed by one id.
func TestBareNodeHasNoAncestorsToRevealButABaseStillDoes(t *testing.T) {
	connector := testConnector(&fakeAPI{})
	nodeID, err := encodeResourceReference(resourceReference{NodeID: "personal-doc"})
	if err != nil {
		t.Fatal(err)
	}
	ancestors, err := connector.ResolveResourceAncestors(
		context.Background(), testConfig(), []string{nodeID},
	)
	if err != nil || len(ancestors) != 0 {
		t.Fatalf("bare node ancestors = %#v, %v; want none", ancestors, err)
	}

	baseID, err := encodeResourceReference(resourceReference{BaseID: "base-one"})
	if err != nil {
		t.Fatal(err)
	}
	ancestors, err = connector.ResolveResourceAncestors(
		context.Background(), testConfig(), []string{baseID},
	)
	if err != nil || len(ancestors) != 1 || ancestors[0] != baseID {
		t.Fatalf("base ancestors = %#v, %v; want the base itself", ancestors, err)
	}
}

// The two manual forms address different object families, and which one applies
// is decided by the user's type selector rather than inferred from the link: a
// DingTalk document link and a Base link are byte-identical in shape. Mixing
// them would describe something that cannot be fetched, so both the encoded and
// the decoded form refuse it, and so does a rootless ancestor path: ancestors
// are walked from a workspace root the reference does not name.
func TestManualReferenceFormsRejectMixedOrRootlessPaths(t *testing.T) {
	for _, ref := range []resourceReference{
		{NodeID: "personal-doc", BaseID: "base-one"},
		{NodeID: "child", Ancestors: []string{"folder"}},
		{BaseID: "base-one", Ancestors: []string{"folder"}},
	} {
		if _, err := encodeResourceReference(ref); err == nil {
			t.Fatalf("encodeResourceReference(%#v) error = nil", ref)
		}
	}

	for _, raw := range []string{
		resourceIDPrefix + "node=personal-doc&base=base-one",
		resourceIDPrefix + "base=base-one&node=personal-doc",
		resourceIDPrefix + "base=base-one&workspace=space",
		resourceIDPrefix + "node=child&ancestor=folder",
	} {
		if _, err := decodeResourceReference(raw); err == nil {
			t.Fatalf("decodeResourceReference(%q) error = nil", raw)
		}
	}
}
