package dingtalk

import (
	"context"
	"errors"
	"testing"

	"github.com/Tencent/WeKnora/internal/datasource"
)

// The picker's "describe this reference" request answers with the reference
// itself, never with its children: the same reference stays expandable, and a
// row returned as a child would be placed under itself by the lazy tree. A Base
// is described by the name the wiki-nodes API reports for its id, and by the
// tables that a base= reference actually syncs — the wiki children it lists
// when expanded are a different thing entirely.
func TestDescribeBaseNamesItFromTheNodeAndItsTables(t *testing.T) {
	api := &fakeAPI{
		// The workspace listing exists and must not be consulted: a Base is
		// self-addressing, and its workspace is a personal space that
		// /v2.0/wiki/workspaces never returns.
		workspaces: []workspace{{ID: "team", RootNodeID: "team-root", Name: "Team"}},
		getNodes: map[string]node{
			"base-9": {
				ID: "base-9", WorkspaceID: "personal-space", Name: "Project tracker.able",
				Type: "FILE", Category: "ALIDOC", Extension: "able", HasChildren: true,
				URL:          "https://alidocs.dingtalk.com/i/nodes/base-9",
				ModifiedTime: "2026-07-01T08:00:00Z",
			},
		},
		notableTables: map[string][]notableTable{
			"base-9": {{ID: "t1", Name: "Checklist"}, {ID: "t2", Name: "Details"}},
		},
	}
	resourceID := baseReference(t, "base-9")

	resources, err := testConnector(api).ListResources(
		context.Background(), testConfig(resourceID), describedResourceParentID(resourceID),
	)
	if err != nil {
		t.Fatalf("ListResources(describe) error = %v", err)
	}
	if len(resources) != 1 {
		t.Fatalf("describe returned %d rows, want the reference itself", len(resources))
	}
	row := resources[0]
	if row.ExternalID != resourceID {
		t.Fatalf("described row id = %q, want the reference the picker asked about", row.ExternalID)
	}
	if row.Name != "Project tracker.able" || row.Type != resourceTypeBase || !row.HasChildren {
		t.Fatalf("described row = %#v", row)
	}
	if row.ParentID != "" {
		t.Fatalf("described row parent = %q, want a root: it is not its own child", row.ParentID)
	}
	// The tables are what selecting the Base syncs, so the row names them.
	if row.Description != "Checklist, Details" {
		t.Fatalf("described row description = %q, want the table names", row.Description)
	}
	if row.Metadata["base_id"] != "base-9" || row.Metadata["workspace_id"] != "personal-space" ||
		row.Metadata["node_id"] != "base-9" || row.Metadata["extension"] != "able" {
		t.Fatalf("described row metadata = %#v", row.Metadata)
	}
	if api.workspaceCalls != 0 {
		t.Fatalf("describing a Base listed workspaces %d times", api.workspaceCalls)
	}
	if api.notableTableCalls["base-9"] != 1 {
		t.Fatalf("table listings = %#v, want one", api.notableTableCalls)
	}
}

// A personal-space document is described the same way, through the same node
// read the sync uses: the name the picker shows and the title of the synced
// item come from one source and cannot disagree.
func TestDescribeNodeNamesAPersonalSpaceDocument(t *testing.T) {
	api := &fakeAPI{
		workspaces: []workspace{{ID: "team", RootNodeID: "team-root", Name: "Team"}},
		getNodes: map[string]node{
			"personal-doc": {
				ID: "personal-doc", WorkspaceID: "personal-space", Name: "Ledger.adoc",
				Type: "FILE", Category: "ALIDOC", Extension: "adoc",
			},
		},
	}
	nodeID, err := encodeResourceReference(resourceReference{NodeID: "personal-doc"})
	if err != nil {
		t.Fatal(err)
	}

	resources, err := testConnector(api).ListResources(
		context.Background(), testConfig(nodeID), describedResourceParentID(nodeID),
	)
	if err != nil {
		t.Fatalf("ListResources(describe) error = %v", err)
	}
	if len(resources) != 1 {
		t.Fatalf("describe returned %d rows, want the reference itself", len(resources))
	}
	row := resources[0]
	if row.ExternalID != nodeID || row.Name != "Ledger.adoc" || row.Type != "document" {
		t.Fatalf("described row = %#v", row)
	}
	// A document has no children to expand; offering an expander that can only
	// fail is worse than offering none.
	if row.HasChildren {
		t.Fatalf("described document claims children: %#v", row)
	}
	if row.Metadata["workspace_id"] != "personal-space" || row.Metadata["node_id"] != "personal-doc" {
		t.Fatalf("described row metadata = %#v", row.Metadata)
	}
	if api.workspaceCalls != 0 {
		t.Fatalf("describing a node listed workspaces %d times", api.workspaceCalls)
	}
}

// A name is a label, not content: when the node cannot be read the row still
// comes back, named by the id, so a selection the sync can read never breaks
// the picker step that shows it.
func TestDescribeFallsBackToTheIDWhenTheNodeCannotBeRead(t *testing.T) {
	api := &fakeAPI{
		getNodeErrors: map[string]error{"base-9": errors.New("permission denied")},
		notableTables: map[string][]notableTable{
			"base-9": {{ID: "t1", Name: "Checklist"}},
		},
	}
	resourceID := baseReference(t, "base-9")

	resources, err := testConnector(api).ListResources(
		context.Background(), testConfig(resourceID), describedResourceParentID(resourceID),
	)
	if err != nil {
		t.Fatalf("ListResources(describe) error = %v", err)
	}
	if len(resources) != 1 || resources[0].Name != "base-9" ||
		resources[0].Type != resourceTypeBase || resources[0].Description != "Checklist" {
		t.Fatalf("described row = %#v, want the id as the fallback name", resources)
	}

	// The same holds for a node reference, and for a Base whose tables cannot
	// be listed either.
	api = &fakeAPI{
		getNodeErrors:      map[string]error{"personal-doc": errors.New("permission denied")},
		notableTableErrors: map[string]error{"base-9": errors.New("permission denied")},
	}
	nodeID, err := encodeResourceReference(resourceReference{NodeID: "personal-doc"})
	if err != nil {
		t.Fatal(err)
	}
	resources, err = testConnector(api).ListResources(
		context.Background(), testConfig(nodeID), describedResourceParentID(nodeID),
	)
	if err != nil || len(resources) != 1 || resources[0].Name != "personal-doc" {
		t.Fatalf("described row = %#v, %v; want the id as the fallback name", resources, err)
	}
	resources, err = testConnector(api).ListResources(
		context.Background(), testConfig(resourceID), describedResourceParentID(resourceID),
	)
	if err != nil || len(resources) != 1 || resources[0].Name != "base-9" ||
		resources[0].Description != "" {
		t.Fatalf("described row = %#v, %v; want a row without table names", resources, err)
	}
}

// A describe request is a picker protocol detail, not a resource: it must not
// decode as a reference, or a saved selection could carry it into the sync.
func TestDescribeRequestIsNotAResourceReference(t *testing.T) {
	baseID := baseReference(t, "base-9")
	if _, err := decodeResourceReference(describedResourceParentID(baseID)); err == nil {
		t.Fatalf("the describe form decoded as a resource reference")
	}
}

// A workspace reference is already a row of the root listing, and naming it
// would need the listing this path exists to avoid; the picker never asks.
func TestDescribeRefusesAWorkspaceReference(t *testing.T) {
	api := &fakeAPI{workspaces: []workspace{{ID: "team", RootNodeID: "team-root", Name: "Team"}}}
	_, err := testConnector(api).ListResources(
		context.Background(), testConfig("team"), describedResourceParentID("team"),
	)
	if !errors.Is(err, datasource.ErrResourceNotFound) {
		t.Fatalf("ListResources(describe workspace) error = %v, want ErrResourceNotFound", err)
	}
	if api.workspaceCalls != 0 {
		t.Fatalf("describing a workspace listed workspaces %d times", api.workspaceCalls)
	}
}

// Expanding a Base lists the wiki documents that live under it, through the id
// the reference already carries. Nothing about it needs the workspace listing:
// the Base's own workspace is a personal space that is never enumerated.
func TestListResourcesExpandsABaseThroughTheWikiNodesAPI(t *testing.T) {
	api := &fakeAPI{
		nodes: map[string][]node{
			"base-9": {
				{
					ID: "child-b", WorkspaceID: "personal-space", Name: "Notes.adoc",
					Type: "FILE", Category: "ALIDOC", Extension: "adoc",
				},
				{
					ID: "child-a", WorkspaceID: "personal-space", Name: "Appendix.adoc",
					Type: "FILE", Category: "ALIDOC", Extension: "adoc",
				},
				// A Base is not offered as a tree child: only folders and wiki
				// documents are selectable here, and a Base is added through
				// its own base= reference.
				{
					ID: "child-able", WorkspaceID: "personal-space", Name: "Nested.able",
					Type: "FILE", Category: "ALIDOC", Extension: "able",
				},
			},
		},
	}
	resourceID := baseReference(t, "base-9")

	resources, err := testConnector(api).ListResources(
		context.Background(), testConfig(resourceID), resourceID,
	)
	if err != nil {
		t.Fatalf("ListResources(base) error = %v", err)
	}
	if len(resources) != 2 {
		t.Fatalf("ListResources(base) = %#v, want the two readable documents", resources)
	}
	// Sorted like every other listing, and nested under the Base the picker
	// expanded.
	if resources[0].Name != "Appendix.adoc" || resources[1].Name != "Notes.adoc" {
		t.Fatalf("children = %#v, want them sorted by name", resources)
	}
	for _, child := range resources {
		if child.Type != resourceTypeBaseChild || child.ParentID != resourceID {
			t.Fatalf("child = %#v, want a base child under %q", child, resourceID)
		}
		if child.Metadata["workspace_id"] != "personal-space" {
			t.Fatalf("child metadata = %#v", child.Metadata)
		}
		// A child is addressed on its own: its id resolves it directly, and it
		// cannot sit on a workspace-rooted ancestor path.
		ref, err := decodeResourceReference(child.ExternalID)
		if err != nil {
			t.Fatalf("decodeResourceReference(%q) error = %v", child.ExternalID, err)
		}
		if ref.NodeID != child.Metadata["node_id"] || ref.BaseID != "" ||
			ref.WorkspaceID != "" || len(ref.Ancestors) != 0 {
			t.Fatalf("child reference = %#v", ref)
		}
	}
	if api.workspaceCalls != 0 {
		t.Fatalf("expanding a Base listed workspaces %d times", api.workspaceCalls)
	}
}

// Describing a Base and expanding it answer two different questions from one
// reference, which is why they are two request forms: the row names what the
// Base syncs, the children are the wiki documents under it.
func TestDescribingAndExpandingABaseAreDifferentAnswers(t *testing.T) {
	api := &fakeAPI{
		getNodes: map[string]node{
			"base-9": {
				ID: "base-9", WorkspaceID: "personal-space", Name: "Project tracker.able",
				Type: "FILE", Category: "ALIDOC", Extension: "able", HasChildren: true,
			},
		},
		nodes: map[string][]node{
			"base-9": {{
				ID: "child-a", WorkspaceID: "personal-space", Name: "Appendix.adoc",
				Type: "FILE", Category: "ALIDOC", Extension: "adoc",
			}},
		},
		notableTables: map[string][]notableTable{
			"base-9": {{ID: "t1", Name: "Checklist"}},
		},
	}
	resourceID := baseReference(t, "base-9")
	connector := testConnector(api)
	cfg := testConfig(resourceID)

	described, err := connector.ListResources(
		context.Background(), cfg, describedResourceParentID(resourceID),
	)
	if err != nil || len(described) != 1 {
		t.Fatalf("describe = %#v, %v", described, err)
	}
	children, err := connector.ListResources(context.Background(), cfg, resourceID)
	if err != nil || len(children) != 1 {
		t.Fatalf("expand = %#v, %v", children, err)
	}
	if described[0].Name != "Project tracker.able" || len(children) != 1 ||
		children[0].Name != "Appendix.adoc" {
		t.Fatalf("describe = %#v, expand = %#v", described, children)
	}
	if described[0].ExternalID == children[0].ExternalID {
		t.Fatalf("describe and expand returned the same row: %#v", described[0])
	}
}
