package types

import "testing"

func TestTenantAPIKeyScopeNormalizeDefaultsEmptyRoleToViewer(t *testing.T) {
	scope := TenantAPIKeyScope{Role: ""}.Normalize()
	if scope.Role != TenantRoleViewer {
		t.Fatalf("normalized role = %q, want viewer", scope.Role)
	}
}

func TestNormalizeTenantAPIKeyRole(t *testing.T) {
	tests := []struct {
		name string
		in   TenantRole
		want TenantRole
	}{
		{name: "viewer", in: TenantRoleViewer, want: TenantRoleViewer},
		{name: "contributor", in: TenantRoleContributor, want: TenantRoleContributor},
		{name: "admin", in: TenantRoleAdmin, want: TenantRoleAdmin},
		{name: "owner", in: TenantRoleOwner, want: TenantRoleOwner},
		{name: "empty defaults viewer", in: "", want: TenantRoleViewer},
		{name: "invalid", in: "superuser", want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := NormalizeTenantAPIKeyRole(tt.in)
			if got != tt.want {
				t.Fatalf("NormalizeTenantAPIKeyRole(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestTenantAPIKeyScopeTenantRole(t *testing.T) {
	got := TenantAPIKeyScope{Role: TenantRoleContributor}.TenantRole()
	if got != TenantRoleContributor {
		t.Fatalf("TenantRole() = %q, want contributor", got)
	}
}
