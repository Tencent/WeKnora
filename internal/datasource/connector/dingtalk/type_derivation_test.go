package dingtalk

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// The kind of a manual DingTalk selection is written into the reference
// (node= or base=), but the picker cannot know it: a document link and a
// multi-dimensional table link are byte-identical. What CAN be known is the
// node behind the id — GET /v2.0/wiki/nodes answers for both, and only the
// extension tells them apart — so the reference's kind is derived from the node
// instead of trusted from the form. These tests pin that derivation and the two
// forms it must not disturb.

// ableNodeFakeAPI is one multi-dimensional table that is also reachable through
// the wiki-nodes API, which is exactly how the picker meets it: a Base is a
// FILE in the ALIDOC category whose extension is able, so nothing about the
// read says "Base" except the extension. The tables, fields and records come
// from the shared Base fixture, so a reference that is corrected here is a
// reference the renderer is already known to read.
func ableNodeFakeAPI() *fakeAPI {
	api := notableBaseFixture()
	api.getNodes = map[string]node{
		"base-1": {
			ID: "base-1", WorkspaceID: "personal-space",
			Name: "项目与任务管理（AI判断风险与建议）.able",
			Type: "FILE", Category: "ALIDOC", Extension: "able",
			HasChildren: true,
			// A UTC timestamp, not a zero value: the row reports when the node
			// last changed, and a zero time would compare equal to "absent".
			ModifiedTime: "2026-10-01T08:00:00Z",
		},
	}
	return api
}

// nodeFormReference renders the node reference for one id, the way the picker's
// manual entry commits it.
func nodeFormReference(t *testing.T, nodeID string) string {
	t.Helper()
	resourceID, err := encodeResourceReference(resourceReference{NodeID: nodeID})
	if err != nil {
		t.Fatal(err)
	}
	return resourceID
}

// A multi-dimensional table's id entered under the document kind is described
// as the Base it is, under the reference that reads it. The node form is the
// one reference the sync cannot read — the connector ingests a Base only
// through base= — so a row that echoed it back would be a positive signal for a
// selection that can only fail.
func TestDescribeOfAnAbleNodeReportsTheBaseReference(t *testing.T) {
	api := ableNodeFakeAPI()
	nodeID := nodeFormReference(t, "base-1")
	baseID := baseReference(t, "base-1")

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
	// The row reports the reference the selection has to store for the sync to
	// read anything at all.
	if row.ExternalID != baseID {
		t.Fatalf("described row id = %q, want the Base reference %q", row.ExternalID, baseID)
	}
	if row.Type != resourceTypeBase {
		t.Fatalf("described row type = %q, want %q", row.Type, resourceTypeBase)
	}
	// Named and explained exactly like a Base entered under its own kind: the
	// name the sync will title the item with, and the tables it will ingest.
	if row.Name != "项目与任务管理（AI判断风险与建议）.able" {
		t.Fatalf("described row name = %q", row.Name)
	}
	if row.Description != "清单, 明细" {
		t.Fatalf("described row description = %q, want the table names", row.Description)
	}
	if row.Metadata["base_id"] != "base-1" || row.Metadata["node_id"] != "base-1" ||
		row.Metadata["extension"] != "able" {
		t.Fatalf("described row metadata = %#v", row.Metadata)
	}
	// The identifying read is the one the node form already made: correcting
	// the kind must not ask the wiki API for the same node twice.
	if api.getNodeCalls["base-1"] != 1 {
		t.Fatalf("node reads = %#v, want exactly one", api.getNodeCalls)
	}
	if api.workspaceCalls != 0 {
		t.Fatalf("describing the reference listed workspaces %d times", api.workspaceCalls)
	}
}

// A node reference that also carries a workspace and a path is corrected the
// same way, and the Base reference it reports carries neither: a Base is
// self-addressing, so a path to one describes nothing and must not survive into
// the reference the selection stores.
func TestDescribeOfAnAbleNodeDropsTheWorkspacePath(t *testing.T) {
	api := ableNodeFakeAPI()
	resourceID, err := encodeResourceReference(resourceReference{
		WorkspaceID: "team", NodeID: "base-1", Ancestors: []string{"folder-1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	baseID := baseReference(t, "base-1")

	resources, err := testConnector(api).ListResources(
		context.Background(), testConfig(resourceID), describedResourceParentID(resourceID),
	)
	if err != nil {
		t.Fatalf("ListResources(describe) error = %v", err)
	}
	if len(resources) != 1 || resources[0].ExternalID != baseID {
		t.Fatalf("described row = %#v, want the bare Base reference %q", resources, baseID)
	}
	if api.workspaceCalls != 0 {
		t.Fatalf("describing the reference listed workspaces %d times", api.workspaceCalls)
	}
}

// The reference the describe row reports is the reference the sync reads: the
// end-to-end answer the picker depends on. Nothing here consults the workspace
// listing, because a Base is resolved by its own id.
func TestFetchAllReadsTheReferenceAnAbleNodeWasDescribedAs(t *testing.T) {
	api := ableNodeFakeAPI()
	nodeID := nodeFormReference(t, "base-1")

	described, err := testConnector(api).ListResources(
		context.Background(), testConfig(nodeID), describedResourceParentID(nodeID),
	)
	if err != nil || len(described) != 1 {
		t.Fatalf("describe = %#v, %v", described, err)
	}
	stored := described[0].ExternalID
	if stored != "dingtalk:v1?base=base-1" {
		t.Fatalf("described reference = %q, want the base form", stored)
	}

	items, err := testConnector(api).FetchAll(
		context.Background(), testConfig(stored), []string{stored},
	)
	if err != nil {
		t.Fatalf("FetchAll(%q) error = %v", stored, err)
	}
	if len(items) != 1 {
		t.Fatalf("FetchAll(%q) returned %d items, want the Base as one item", stored, len(items))
	}
	if items[0].ExternalID != "base-1" || items[0].Metadata["base_id"] != "base-1" {
		t.Fatalf("synced item = %#v", items[0])
	}
	// The tables really are the content: the reference syncs, it does not merely
	// avoid an error.
	if !strings.Contains(string(items[0].Content), "## 清单") ||
		!strings.Contains(string(items[0].Content), "## 明细") {
		t.Fatalf("Base markdown does not hold one heading per table:\n%s", items[0].Content)
	}
	if api.workspaceCalls != 0 {
		t.Fatalf("syncing the Base listed workspaces %d times", api.workspaceCalls)
	}
}

// The failure the derivation exists to prevent, pinned: the node form names a
// document, the connector has no document read path for a Base, and the scope
// fails with the retryable "resource is unavailable" item that sends an
// operator looking for a permissions problem that is not there. Correcting the
// reference is therefore the only fix — which is why the picker must store the
// corrected one, and why Validate reports the uncorrected one.
func TestABareNodeReferenceToABaseIsTheSelectionThatFailsToSync(t *testing.T) {
	api := ableNodeFakeAPI()
	nodeID := nodeFormReference(t, "base-1")

	items, err := testConnector(api).FetchAll(
		context.Background(), testConfig(nodeID), []string{nodeID},
	)
	if err == nil {
		t.Fatalf("FetchAll(%q) error = nil, want the node form refused", nodeID)
	}
	if len(items) != 1 || items[0].IsDeleted {
		t.Fatalf("FetchAll(%q) items = %#v, want one failed resource", nodeID, items)
	}
	if items[0].Metadata["error_reason_code"] != "dingtalk_resource_failed" {
		t.Fatalf("failed item = %#v", items[0])
	}
}

// Validate reports the mistake at save time, in the terms the operator can act
// on: the id, the type it really is, and the reference form that reads it.
// Without it a reference this wrong is accepted and only fails at sync, where
// the message blames access.
func TestValidateReportsAnAbleNodeSavedAsANodeReference(t *testing.T) {
	api := ableNodeFakeAPI()
	nodeID := nodeFormReference(t, "base-1")

	err := testConnector(api).Validate(context.Background(), testConfig(nodeID))
	if err == nil {
		t.Fatal("Validate() error = nil, want the node form of a Base refused")
	}
	message := err.Error()
	for _, want := range []string{"base-1", "multi-dimensional table", "dingtalk:v1?base=base-1"} {
		if !strings.Contains(message, want) {
			t.Fatalf("Validate() error = %q, want it to name %q", message, want)
		}
	}
	// The verdict is about the reference, not about the tenant: it needs no
	// workspace walk, and there is no content to probe under a reference the
	// sync cannot read.
	if api.workspaceCalls != 0 {
		t.Fatalf("Validate() listed workspaces %d times", api.workspaceCalls)
	}
	if api.notableTableCalls["base-1"] != 0 {
		t.Fatalf("table listings = %#v, want none", api.notableTableCalls)
	}
}

// A node that really is a document is not affected: it validates exactly as it
// did before, through the workspace walk, and its reference is not rewritten.
func TestValidateStillAcceptsADocumentNodeReference(t *testing.T) {
	api := &fakeAPI{
		workspaces: []workspace{{ID: "team", RootNodeID: "team-root", Name: "Team"}},
		nodes: map[string][]node{
			"team-root": {{
				ID: "doc-1", WorkspaceID: "team", Name: "Ledger.adoc",
				Type: "FILE", Category: "ALIDOC", Extension: "adoc",
			}},
		},
	}
	nodeID := nodeFormReference(t, "doc-1")

	if err := testConnector(api).Validate(context.Background(), testConfig(nodeID)); err != nil {
		t.Fatalf("Validate() error = %v, want a document node reference accepted", err)
	}
	if strings.Contains(nodeID, "base=") {
		t.Fatalf("node reference = %q, want the node form", nodeID)
	}
}

// A personal-space document is described exactly as before: named by the node,
// typed as the document it is, and still stored under the node reference.
func TestDescribeOfADocumentNodeReferenceIsUnchanged(t *testing.T) {
	api := &fakeAPI{
		getNodes: map[string]node{
			"doc-1": {
				ID: "doc-1", WorkspaceID: "personal-space", Name: "Ledger.adoc",
				Type: "FILE", Category: "ALIDOC", Extension: "adoc",
				URL: "https://alidocs.dingtalk.com/i/nodes/doc-1",
			},
		},
	}
	nodeID := nodeFormReference(t, "doc-1")

	resources, err := testConnector(api).ListResources(
		context.Background(), testConfig(nodeID), describedResourceParentID(nodeID),
	)
	if err != nil || len(resources) != 1 {
		t.Fatalf("describe = %#v, %v", resources, err)
	}
	row := resources[0]
	if row.ExternalID != nodeID || row.Type != "document" || row.Name != "Ledger.adoc" {
		t.Fatalf("described row = %#v, want the document under its own reference", row)
	}
	if row.HasChildren {
		t.Fatalf("described document claims children: %#v", row)
	}
	// A document is never asked about through the notable API: the tables of a
	// Base are not a thing a document has.
	if api.notableTableCalls["doc-1"] != 0 {
		t.Fatalf("table listings = %#v, want none", api.notableTableCalls)
	}
}

// A reference already in the Base form is untouched. It is the form the sync
// reads, the picker's row is the one the selection stores, and the node behind
// it is still read exactly once.
func TestDescribeOfABaseReferenceIsUnchanged(t *testing.T) {
	api := ableNodeFakeAPI()
	baseID := baseReference(t, "base-1")

	resources, err := testConnector(api).ListResources(
		context.Background(), testConfig(baseID), describedResourceParentID(baseID),
	)
	if err != nil || len(resources) != 1 {
		t.Fatalf("describe = %#v, %v", resources, err)
	}
	row := resources[0]
	if row.ExternalID != baseID || row.Type != resourceTypeBase ||
		row.Name != "项目与任务管理（AI判断风险与建议）.able" || row.Description != "清单, 明细" {
		t.Fatalf("described row = %#v", row)
	}
	if api.getNodeCalls["base-1"] != 1 {
		t.Fatalf("node reads = %#v, want exactly one", api.getNodeCalls)
	}
	if api.notableTableCalls["base-1"] != 1 {
		t.Fatalf("table listings = %#v, want exactly one", api.notableTableCalls)
	}

	// And it still validates as readable content on its own.
	if err := testConnector(api).Validate(context.Background(), testConfig(baseID)); err != nil {
		t.Fatalf("Validate() error = %v, want the Base reference accepted", err)
	}
	if api.workspaceCalls != 0 {
		t.Fatalf("validating the Base consulted the workspace listing %d times", api.workspaceCalls)
	}
}

// A node the wiki API will not answer for is not evidence of anything: the
// describe row and Validate both leave it to the sync, which has the per-scope
// failure channel to carry it. A failed read must never be turned into a
// confident "this is a document".
func TestAnUnreadableNodeIsNotDerivedIntoEitherKind(t *testing.T) {
	nodeID := nodeFormReference(t, "missing-1")

	describeAPI := &fakeAPI{getNodeErrors: map[string]error{"missing-1": errors.New("unavailable")}}
	resources, err := testConnector(describeAPI).ListResources(
		context.Background(), testConfig(nodeID), describedResourceParentID(nodeID),
	)
	if err != nil || len(resources) != 1 {
		t.Fatalf("describe = %#v, %v", resources, err)
	}
	if resources[0].ExternalID != nodeID || resources[0].Name != "missing-1" {
		t.Fatalf("described row = %#v, want the id under the reference asked about", resources[0])
	}
	if describeAPI.notableTableCalls["missing-1"] != 0 {
		t.Fatalf("table listings = %#v, want none", describeAPI.notableTableCalls)
	}

	// Nothing readable in the tenant either, so Validate accepts: it reports a
	// reference it can prove wrong, not one it cannot read.
	validateAPI := &fakeAPI{getNodeErrors: map[string]error{"missing-1": errors.New("unavailable")}}
	if err := testConnector(validateAPI).Validate(context.Background(), testConfig(nodeID)); err != nil {
		t.Fatalf("Validate() error = %v, want an unreadable node left to the sync", err)
	}
}

// A context error is not a type: it must stay a failure rather than be absorbed
// into a fallback row or a validation verdict.
func TestDescribeAndValidateKeepContextErrorsFromTheKindRead(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	nodeID := nodeFormReference(t, "base-1")

	api := &fakeAPI{getNodeErrors: map[string]error{"base-1": context.Canceled}}
	if _, err := testConnector(api).ListResources(
		ctx, testConfig(nodeID), describedResourceParentID(nodeID),
	); !errors.Is(err, context.Canceled) {
		t.Fatalf("ListResources(describe) error = %v, want context.Canceled", err)
	}

	if err := testConnector(api).Validate(ctx, testConfig(nodeID)); !errors.Is(err, context.Canceled) {
		t.Fatalf("Validate() error = %v, want context.Canceled", err)
	}
}
