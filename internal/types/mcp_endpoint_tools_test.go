package types

import (
	"reflect"
	"testing"
)

func TestNormalizeMCPEndpointToolsDropsUnknownAndOrders(t *testing.T) {
	got := NormalizeMCPEndpointTools([]string{
		" ask ", "bogus", MCPEndpointToolSearchKnowledge, MCPEndpointToolAsk, "", MCPEndpointToolListKnowledgeBases,
	})
	want := []string{MCPEndpointToolListKnowledgeBases, MCPEndpointToolSearchKnowledge, MCPEndpointToolAsk}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestDefaultMCPEndpointToolsExcludesDestructive(t *testing.T) {
	for _, name := range DefaultMCPEndpointTools() {
		def, ok := LookupMCPEndpointTool(name)
		if !ok {
			t.Fatalf("default tool %q missing from catalog", name)
		}
		if def.Destructive {
			t.Fatalf("default tool %q must not be destructive", name)
		}
	}
	if _, ok := LookupMCPEndpointTool(MCPEndpointToolDeleteDocument); !ok {
		t.Fatal("delete_document must be in the catalog")
	}
}

func TestMCPEndpointCapabilitiesForTools(t *testing.T) {
	cases := []struct {
		name  string
		tools []string
		want  []string
	}{
		{"retrieve only", []string{MCPEndpointToolSearchKnowledge}, []string{"retrieve"}},
		{"wiki only", []string{MCPEndpointToolWikiIndex}, []string{"retrieve"}},
		{"ask", []string{MCPEndpointToolAsk}, []string{"retrieve", "chat", "read_agents"}},
		{"ingest", []string{MCPEndpointToolAddDocument}, []string{"ingest"}},
		{"none", nil, []string{}},
	}
	for _, tc := range cases {
		if got := MCPEndpointCapabilitiesForTools(tc.tools); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: got %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestMCPEndpointScopeMirrorsEndpoint(t *testing.T) {
	ep := &MCPEndpoint{
		KnowledgeBaseIDs: StringArray{"kb-1", "kb-2"},
		Tools:            StringArray{MCPEndpointToolAsk, MCPEndpointToolDeleteDocument},
	}
	scope := MCPEndpointScope(ep)
	if scope.FullAccess {
		t.Fatal("endpoint scope must never be full access")
	}
	if !scope.AllowsKnowledgeBase("kb-1") || scope.AllowsKnowledgeBase("kb-3") {
		t.Fatal("knowledge base allowlist not mirrored")
	}
	if !scope.HasCapability(APIKeyCapabilityChat) || !scope.HasCapability(APIKeyCapabilityIngest) {
		t.Fatal("capabilities not derived from tools")
	}
	if MCPEndpointScope(nil).HasCapability(APIKeyCapabilityRetrieve) {
		t.Fatal("nil endpoint must yield an empty scope")
	}
	if !ep.HasTool("ask") || ep.HasTool("search_knowledge") {
		t.Fatal("HasTool mismatch")
	}
	if !(&MCPEndpoint{}).AllowsKnowledgeBase("anything") {
		t.Fatal("unrestricted endpoint must allow any knowledge base")
	}
}
