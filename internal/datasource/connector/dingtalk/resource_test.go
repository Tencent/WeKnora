package dingtalk

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
)

func TestResourceRefRoundTripAndAncestors(t *testing.T) {
	workspace := workspaceResourceRef("workspace-1")
	folder := childResourceRef(workspace, "folder-1")
	document := childResourceRef(folder, "doc-1")

	encoded, err := encodeResourceRef(document)
	if err != nil {
		t.Fatalf("encodeResourceRef() error = %v", err)
	}
	decoded, err := decodeResourceRef(encoded)
	if err != nil {
		t.Fatalf("decodeResourceRef() error = %v", err)
	}
	if decoded.WorkspaceID != "workspace-1" || decoded.NodeID != "doc-1" || len(decoded.Ancestors) != 1 || decoded.Ancestors[0] != "folder-1" {
		t.Fatalf("decoded ref = %#v", decoded)
	}

	ancestors, err := ancestorResourceIDs(decoded)
	if err != nil {
		t.Fatalf("ancestorResourceIDs() error = %v", err)
	}
	if len(ancestors) != 2 {
		t.Fatalf("ancestor count = %d, want 2", len(ancestors))
	}
	root, _ := decodeResourceRef(ancestors[0])
	parent, _ := decodeResourceRef(ancestors[1])
	if root.NodeID != "" || root.WorkspaceID != "workspace-1" {
		t.Fatalf("root ancestor = %#v", root)
	}
	if parent.NodeID != "folder-1" || len(parent.Ancestors) != 0 {
		t.Fatalf("parent ancestor = %#v", parent)
	}
}

func TestDecodeResourceRefRejectsMalformedValues(t *testing.T) {
	for _, value := range []string{"", "other:v1:abc", resourceIDPrefix + "%%%"} {
		if _, err := decodeResourceRef(value); err == nil {
			t.Fatalf("decodeResourceRef(%q) succeeded, want error", value)
		}
	}

	encodeRaw := func(ref resourceRef) string {
		payload, err := json.Marshal(ref)
		if err != nil {
			t.Fatal(err)
		}
		return resourceIDPrefix + base64.RawURLEncoding.EncodeToString(payload)
	}
	for name, ref := range map[string]resourceRef{
		"empty workspace":          {NodeID: "doc"},
		"workspace with ancestors": {WorkspaceID: "w", Ancestors: []string{"a"}},
		"ancestor cycle":           {WorkspaceID: "w", NodeID: "doc", Ancestors: []string{"a", "a"}},
		"node cycle":               {WorkspaceID: "w", NodeID: "doc", Ancestors: []string{"doc"}},
		"empty ancestor":           {WorkspaceID: "w", NodeID: "doc", Ancestors: []string{""}},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := decodeResourceRef(encodeRaw(ref)); err == nil {
				t.Fatalf("decodeResourceRef(%#v) succeeded, want error", ref)
			}
		})
	}
}

func TestResourceRefDepthAndSizeLimits(t *testing.T) {
	tooDeep := resourceRef{WorkspaceID: "w", NodeID: "doc", Ancestors: make([]string, maxResourceDepth+1)}
	for i := range tooDeep.Ancestors {
		tooDeep.Ancestors[i] = "a" + strings.Repeat("x", i+1)
	}
	if _, err := encodeResourceRef(tooDeep); err == nil {
		t.Fatal("encodeResourceRef() accepted over-depth lineage")
	}

	tooLarge := resourceRef{WorkspaceID: strings.Repeat("w", maxResourceIDBytes), NodeID: "doc"}
	if _, err := encodeResourceRef(tooLarge); err == nil {
		t.Fatal("encodeResourceRef() accepted oversized payload")
	}
}
