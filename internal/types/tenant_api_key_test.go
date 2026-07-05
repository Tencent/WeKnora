package types

import "testing"

func TestTenantAPIKeyScopeNormalizeDefaultsEmptyScopesToRead(t *testing.T) {
	scope := TenantAPIKeyScope{Scopes: StringArray{}}.Normalize()
	if len(scope.Scopes) != 1 || scope.Scopes[0] != TenantAPIKeyScopeRead {
		t.Fatalf("normalized scopes = %v, want [read]", scope.Scopes)
	}
}

func TestTenantAPIKeyScopeTenantRoleMapping(t *testing.T) {
	tests := []struct {
		name string
		in   StringArray
		want TenantRole
	}{
		{name: "read", in: StringArray{TenantAPIKeyScopeRead}, want: TenantRoleViewer},
		{name: "write", in: StringArray{TenantAPIKeyScopeWrite}, want: TenantRoleContributor},
		{name: "admin", in: StringArray{TenantAPIKeyScopeAdmin}, want: TenantRoleAdmin},
		{name: "empty defaults read", in: StringArray{}, want: TenantRoleViewer},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := TenantAPIKeyScope{Scopes: tt.in}.TenantRole()
			if got != tt.want {
				t.Fatalf("TenantRole() = %q, want %q", got, tt.want)
			}
		})
	}
}
