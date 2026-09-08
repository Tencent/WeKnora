package cubeclient

import (
	"context"
	"testing"

	"github.com/golang-jwt/jwt/v5"
)

func TestClientSignJWT(t *testing.T) {
	c := New("http://localhost:4000/cubejs-api/v1", "test-secret-key")
	sec := SecurityContext{
		Sub:      "user-123",
		Groups:   []string{"analytics", "ops"},
		TenantID: 10001,
	}
	token, err := c.sign(sec)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	if token == "" {
		t.Fatal("empty token")
	}

	// verify the JWT can be parsed with the same secret
	parsed, err := jwt.Parse(token, func(_ *jwt.Token) (interface{}, error) {
		return []byte("test-secret-key"), nil
	})
	if err != nil || !parsed.Valid {
		t.Fatalf("JWT verification failed: %v", err)
	}
	claims, ok := parsed.Claims.(jwt.MapClaims)
	if !ok {
		t.Fatal("invalid claims type")
	}
	if claims["sub"] != "user-123" {
		t.Errorf("sub mismatch: %v", claims["sub"])
	}
	groups, ok := claims["groups"].([]interface{})
	if !ok || len(groups) != 2 {
		t.Errorf("groups mismatch: %v", claims["groups"])
	}
	if claims["tenant_id"].(float64) != 10001 {
		t.Errorf("tenant_id mismatch: %v", claims["tenant_id"])
	}
	if _, ok := claims["iat"]; !ok {
		t.Error("iat missing")
	}
	if _, ok := claims["exp"]; !ok {
		t.Error("exp missing")
	}
}

func TestClientEmptyEndpoint(t *testing.T) {
	c := New("", "secret")
	// Endpoint() returns empty string when not configured
	if c.Endpoint() != "" {
		t.Errorf("expected empty endpoint")
	}
}

func TestSecurityContextRoundTrip(t *testing.T) {
	ctx := context.Background()
	sec := SecurityContext{Sub: "user-456", Groups: []string{"sales"}, TenantID: 20002}
	ctx2 := WithSecurityContext(ctx, sec)
	got := SecurityContextFromContext(ctx2)
	if got.Sub != "user-456" || len(got.Groups) != 1 || got.Groups[0] != "sales" || got.TenantID != 20002 {
		t.Errorf("round-trip mismatch: %+v", got)
	}
	// absent context returns zero value
	got2 := SecurityContextFromContext(context.Background())
	if got2.Sub != "" || len(got2.Groups) != 0 {
		t.Errorf("absent context should return zero value: %+v", got2)
	}
}

func TestTruncate(t *testing.T) {
	cases := []struct {
		input string
		n     int
		want  string
	}{
		{"hello", 10, "hello"},
		{"hello world", 5, "hello..."},
		{"", 5, ""},
		{"abc", 3, "abc"},
	}
	for _, tc := range cases {
		if got := truncate(tc.input, tc.n); got != tc.want {
			t.Errorf("truncate(%q, %d) = %q, want %q", tc.input, tc.n, got, tc.want)
		}
	}
}
