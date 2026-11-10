package types

import "testing"

func TestTenantAPIKeyScopeNormalizePreservesFullAccess(t *testing.T) {
	scope := TenantAPIKeyScope{FullAccess: true}.Normalize()
	if !scope.FullAccess {
		t.Fatal("normalized scope lost full access")
	}
}

func TestTenantAPIKeyScopeNormalizeDefaultsToScopedAccess(t *testing.T) {
	scope := TenantAPIKeyScope{}.Normalize()
	if scope.FullAccess {
		t.Fatal("empty scope must not become full access")
	}
}

func TestTenantAPIKeyScopeNormalizeDropsInvalidCapabilities(t *testing.T) {
	scope := TenantAPIKeyScope{
		Capabilities: StringArray{"chat", "bogus", "retrieve", "chat"},
	}.Normalize()
	want := StringArray{"chat", "retrieve"}
	if len(scope.Capabilities) != len(want) {
		t.Fatalf("capabilities = %#v, want %#v", scope.Capabilities, want)
	}
	for i := range want {
		if scope.Capabilities[i] != want[i] {
			t.Fatalf("capabilities = %#v, want %#v", scope.Capabilities, want)
		}
	}
}
