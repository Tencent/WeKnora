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
		resourceIDPrefix + "node=doc",
		resourceIDPrefix + "workspace=space&workspace=other&node=doc",
		resourceIDPrefix + "workspace=space&node=doc&unexpected=value",
	} {
		if _, err := decodeResourceReference(raw); err == nil {
			t.Fatalf("decodeResourceReference(%q) error = nil", raw)
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
