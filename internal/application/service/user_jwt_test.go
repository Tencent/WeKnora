package service

import (
	"testing"

	"github.com/golang-jwt/jwt/v5"
)

// tenantIDFromClaims is the JWT->tenant-id projection used by
// userService.ValidateToken. It must:
//   - prefer the claim when present (so /auth/switch-tenant takes effect)
//   - fall back to the caller-supplied default when the claim is absent
//     (backward compatibility with tokens minted before tenant-level RBAC)
//   - reject zero / negative claim values rather than scoping the session
//     to "tenant 0"
//
// These tests are pure-function so they don't need any DB/repo plumbing.
func TestTenantIDFromClaims(t *testing.T) {
	cases := []struct {
		name     string
		claims   jwt.MapClaims
		fallback uint64
		want     uint64
	}{
		{
			name:     "json number wins over fallback",
			claims:   jwt.MapClaims{"tenant_id": float64(99)},
			fallback: 1,
			want:     99,
		},
		{
			name:     "missing claim falls back to home tenant",
			claims:   jwt.MapClaims{"user_id": "u1"},
			fallback: 7,
			want:     7,
		},
		{
			name:     "zero claim treated as missing",
			claims:   jwt.MapClaims{"tenant_id": float64(0)},
			fallback: 5,
			want:     5,
		},
		{
			name:     "negative claim treated as missing",
			claims:   jwt.MapClaims{"tenant_id": float64(-3)},
			fallback: 5,
			want:     5,
		},
		{
			name:     "string claim treated as missing",
			claims:   jwt.MapClaims{"tenant_id": "12"},
			fallback: 8,
			want:     8,
		},
		{
			name:     "int64 claim accepted (test-built tokens)",
			claims:   jwt.MapClaims{"tenant_id": int64(42)},
			fallback: 1,
			want:     42,
		},
		{
			name:     "uint64 claim accepted",
			claims:   jwt.MapClaims{"tenant_id": uint64(123)},
			fallback: 1,
			want:     123,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tenantIDFromClaims(tc.claims, tc.fallback); got != tc.want {
				t.Fatalf("tenantIDFromClaims(%v, %d) = %d, want %d",
					tc.claims, tc.fallback, got, tc.want)
			}
		})
	}
}
