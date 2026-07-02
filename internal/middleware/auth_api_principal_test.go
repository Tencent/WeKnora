package middleware

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/golang-jwt/jwt/v5"
)

func signedExternalUserToken(t *testing.T, secret string, claims jwt.MapClaims) string {
	t.Helper()

	token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(secret))
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}
	return token
}

func TestResolveAPIPrincipalDefaultsToTenant(t *testing.T) {
	p, err := resolveAPIPrincipal(context.Background(), &types.Tenant{ID: 7}, http.Header{})
	if err != nil {
		t.Fatalf("resolveAPIPrincipal error = %v", err)
	}

	if p.Type != types.PrincipalAPITenant || p.ID != "7" {
		t.Fatalf("principal = %#v", p)
	}
}

func TestResolveAPIPrincipalDirectHeader(t *testing.T) {
	header := http.Header{}
	header.Set("X-External-User-ID", "external-u1")

	p, err := resolveAPIPrincipal(context.Background(), &types.Tenant{
		ID: 7,
		APIPrincipalConfig: &types.APIPrincipalConfig{
			Mode: types.APIPrincipalModeDirect,
		},
	}, header)
	if err != nil {
		t.Fatalf("resolveAPIPrincipal error = %v", err)
	}

	if p.Type != types.PrincipalAPIExternalUser || p.ID != "7:external-u1" {
		t.Fatalf("principal = %#v", p)
	}
}

func TestResolveAPIPrincipalSignedToken(t *testing.T) {
	secret := "test-secret"
	header := http.Header{}
	header.Set("X-External-User-Token", signedExternalUserToken(t, secret, jwt.MapClaims{
		"sub":       "external-u1",
		"tenant_id": float64(7),
		"aud":       "weknora",
		"exp":       time.Now().Add(time.Minute).Unix(),
	}))

	p, err := resolveAPIPrincipal(context.Background(), &types.Tenant{
		ID: 7,
		APIPrincipalConfig: &types.APIPrincipalConfig{
			Mode:       types.APIPrincipalModeSignedToken,
			HMACSecret: secret,
		},
	}, header)
	if err != nil {
		t.Fatalf("resolveAPIPrincipal error = %v", err)
	}

	if p.Type != types.PrincipalAPIExternalUser || p.ID != "7:external-u1" {
		t.Fatalf("principal = %#v", p)
	}
}

func TestResolveAPIPrincipalSignedTokenRejectsWrongTenant(t *testing.T) {
	secret := "test-secret"
	header := http.Header{}
	header.Set("X-External-User-Token", signedExternalUserToken(t, secret, jwt.MapClaims{
		"sub":       "external-u1",
		"tenant_id": float64(8),
		"aud":       "weknora",
		"exp":       time.Now().Add(time.Minute).Unix(),
	}))

	p, err := resolveAPIPrincipal(context.Background(), &types.Tenant{
		ID: 7,
		APIPrincipalConfig: &types.APIPrincipalConfig{
			Mode:       types.APIPrincipalModeSignedToken,
			HMACSecret: secret,
		},
	}, header)
	if err == nil {
		t.Fatalf("resolveAPIPrincipal error = nil, want error")
	}
	_ = p
}

func TestResolveAPIPrincipalSignedTokenRejectsExpired(t *testing.T) {
	secret := "test-secret"
	header := http.Header{}
	header.Set("X-External-User-Token", signedExternalUserToken(t, secret, jwt.MapClaims{
		"sub":       "external-u1",
		"tenant_id": float64(7),
		"aud":       "weknora",
		"exp":       time.Now().Add(-time.Minute).Unix(),
	}))

	p, err := resolveAPIPrincipal(context.Background(), &types.Tenant{
		ID: 7,
		APIPrincipalConfig: &types.APIPrincipalConfig{
			Mode:       types.APIPrincipalModeSignedToken,
			HMACSecret: secret,
		},
	}, header)
	if err == nil {
		t.Fatalf("resolveAPIPrincipal error = nil, want error")
	}
	_ = p
}

func TestResolveAPIPrincipalDirectHeaderRequired(t *testing.T) {
	_, err := resolveAPIPrincipal(context.Background(), &types.Tenant{
		ID: 7,
		APIPrincipalConfig: &types.APIPrincipalConfig{
			Mode:                types.APIPrincipalModeDirect,
			RequireDirectHeader: true,
		},
	}, http.Header{})
	if !errors.Is(err, errMissingDirectHeader) {
		t.Fatalf("resolveAPIPrincipal error = %v, want errMissingDirectHeader", err)
	}
}

func TestResolveAPIPrincipalDirectHeaderRejectsInvalidUserID(t *testing.T) {
	header := http.Header{}
	header.Set("X-External-User-ID", strings.Repeat("a", maxExternalUserIDLen+1))

	_, err := resolveAPIPrincipal(context.Background(), &types.Tenant{
		ID: 7,
		APIPrincipalConfig: &types.APIPrincipalConfig{
			Mode: types.APIPrincipalModeDirect,
		},
	}, header)
	if !errors.Is(err, errInvalidExternalUserID) {
		t.Fatalf("resolveAPIPrincipal error = %v, want errInvalidExternalUserID", err)
	}
}

func TestResolveAPIPrincipalSignedTokenRejectsLongLifetime(t *testing.T) {
	secret := "test-secret"
	header := http.Header{}
	header.Set("X-External-User-Token", signedExternalUserToken(t, secret, jwt.MapClaims{
		"sub":       "external-u1",
		"tenant_id": float64(7),
		"aud":       "weknora",
		"exp":       time.Now().Add(48 * time.Hour).Unix(),
	}))

	_, err := resolveAPIPrincipal(context.Background(), &types.Tenant{
		ID: 7,
		APIPrincipalConfig: &types.APIPrincipalConfig{
			Mode:       types.APIPrincipalModeSignedToken,
			HMACSecret: secret,
		},
	}, header)
	if err == nil {
		t.Fatalf("resolveAPIPrincipal error = nil, want error")
	}
}
