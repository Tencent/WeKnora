package dingtalk

import (
	"strings"
	"testing"
)

func TestResourceReferenceRoundTripAndAncestors(t *testing.T) {
	folder := resourceReference{WorkspaceID: "workspace", NodeID: "folder"}
	document := folder.child("document")
	documentID, err := encodeResourceReference(document)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := decodeResourceReference(documentID)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.WorkspaceID != "workspace" || decoded.NodeID != "document" ||
		len(decoded.Ancestors) != 1 || decoded.Ancestors[0] != "folder" {
		t.Fatalf("decoded reference = %#v", decoded)
	}

	ancestorIDs, err := resourceAncestorIDs(decoded)
	if err != nil {
		t.Fatal(err)
	}
	folderID, err := encodeResourceReference(folder)
	if err != nil {
		t.Fatal(err)
	}
	if len(ancestorIDs) != 2 || ancestorIDs[0] != "workspace" || ancestorIDs[1] != folderID {
		t.Fatalf("ancestor IDs = %#v", ancestorIDs)
	}
}

func TestResourceReferenceRejectsMalformedOrCyclicIDs(t *testing.T) {
	tooDeep := make([]string, maxResourceDepth+1)
	for index := range tooDeep {
		tooDeep[index] = strings.Repeat("a", index+1)
	}
	for _, ref := range []resourceReference{
		{},
		{WorkspaceID: " workspace"},
		{WorkspaceID: "workspace", Ancestors: []string{"folder"}},
		{WorkspaceID: "workspace", NodeID: "folder", Ancestors: []string{"folder"}},
		{WorkspaceID: "workspace", NodeID: "document", Ancestors: []string{"folder", "folder"}},
		{WorkspaceID: "workspace", NodeID: "document", Ancestors: tooDeep},
	} {
		if _, err := encodeResourceReference(ref); err == nil {
			t.Fatalf("encodeResourceReference(%#v) error = nil", ref)
		}
	}

	for _, raw := range []string{
		resourceIDPrefix + "workspace=space",
		resourceIDPrefix + "workspace=space&workspace=other&node=doc",
		resourceIDPrefix + "workspace=space&node=doc&unexpected=value",
	} {
		if _, err := decodeResourceReference(raw); err == nil {
			t.Fatalf("decodeResourceReference(%q) error = nil", raw)
		}
	}
}

// The bare node form ("dingtalk:v1?node=<id>") is the manual entry path: it
// exists because some content is readable by id but is never enumerated, so the
// picker cannot offer it — a personal-space document (GET /v2.0/wiki/workspaces
// lists team workspaces only) and a multi-dimensional table (nothing lists
// Bases). It used to be rejected along with the other malformed ids because a
// node without a workspace could not be resolved; it can now, because the node
// itself reports its workspace (see resolveSyncScopes).
func TestResourceReferenceAcceptsABareNodeID(t *testing.T) {
	for _, nodeID := range []string{"personal-doc", "node_7f3a9c2e5b1d4a8c6e0f", "doc_42"} {
		encoded, err := encodeResourceReference(resourceReference{NodeID: nodeID})
		if err != nil {
			t.Fatalf("encodeResourceReference(node=%q) error = %v", nodeID, err)
		}
		if want := resourceIDPrefix + "node=" + nodeID; encoded != want {
			t.Fatalf("encoded bare node = %q, want %q", encoded, want)
		}
		decoded, err := decodeResourceReference(encoded)
		if err != nil {
			t.Fatalf("decodeResourceReference(%q) error = %v", encoded, err)
		}
		if decoded.NodeID != nodeID || decoded.WorkspaceID != "" || len(decoded.Ancestors) != 0 {
			t.Fatalf("decoded bare node = %#v", decoded)
		}
		// The decoded reference must survive a second encode unchanged: the id
		// is what the cursor and the child references are keyed by.
		again, err := encodeResourceReference(decoded)
		if err != nil || again != encoded {
			t.Fatalf("re-encoded bare node = %q, %v; want %q", again, err, encoded)
		}
	}
}

func TestResourceReferenceKeepsWorkspaceIDsBackwardCompatible(t *testing.T) {
	decoded, err := decodeResourceReference("legacy-workspace")
	if err != nil {
		t.Fatal(err)
	}
	if decoded.WorkspaceID != "legacy-workspace" || decoded.NodeID != "" {
		t.Fatalf("decoded legacy workspace = %#v", decoded)
	}
	encoded, err := encodeResourceReference(decoded)
	if err != nil || encoded != "legacy-workspace" {
		t.Fatalf("encoded workspace = %q, %v", encoded, err)
	}
}
