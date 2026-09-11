package outline

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

func makeDSConfig(f *fakeOutline, resourceIDs []string) *types.DataSourceConfig {
	return &types.DataSourceConfig{
		Type: types.ConnectorTypeOutline,
		Credentials: map[string]interface{}{
			"api_token": f.cfg().APIToken,
			"base_url":  f.cfg().BaseURL,
		},
		ResourceIDs: resourceIDs,
	}
}

func TestConnector_Type(t *testing.T) {
	if got := NewConnector().Type(); got != types.ConnectorTypeOutline {
		t.Errorf("Type() = %q, want %q", got, types.ConnectorTypeOutline)
	}
}

func TestConnector_Validate_Success(t *testing.T) {
	f := newFakeOutline(nil)
	defer f.Close()

	if err := NewConnector().Validate(context.Background(), makeDSConfig(f, nil)); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestConnector_Validate_BadToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
	}))
	defer srv.Close()

	err := NewConnector().Validate(context.Background(), &types.DataSourceConfig{
		Credentials: map[string]interface{}{"api_token": "bad", "base_url": srv.URL},
	})
	if err == nil {
		t.Fatal("expected error for a rejected token")
	}
}

func TestConnector_Validate_MissingToken(t *testing.T) {
	err := NewConnector().Validate(context.Background(), &types.DataSourceConfig{
		Credentials: map[string]interface{}{"base_url": "http://localhost:3000"},
	})
	if err == nil {
		t.Fatal("expected error when api_token is missing")
	}
}

func TestConnector_ListResources_Collections(t *testing.T) {
	f := newFakeOutline(nil)
	defer f.Close()

	res, err := NewConnector().ListResources(context.Background(), makeDSConfig(f, nil), "")
	if err != nil {
		t.Fatalf("ListResources: %v", err)
	}
	if len(res) != 2 {
		t.Fatalf("len = %d, want 2", len(res))
	}
	// Deterministic order matters for UI rendering and response caching.
	if res[0].ExternalID != "col-1" || res[1].ExternalID != "col-2" {
		t.Errorf("unstable order: %q, %q", res[0].ExternalID, res[1].ExternalID)
	}
	if res[0].Type != "collection" {
		t.Errorf("Type = %q, want collection", res[0].Type)
	}
	if res[0].Name != "Handbook" {
		t.Errorf("Name = %q", res[0].Name)
	}
	// Outline returns a path; the resource URL must be openable in a browser.
	want := f.server.URL + "/collection/handbook-abc"
	if res[0].URL != want {
		t.Errorf("URL = %q, want %q", res[0].URL, want)
	}
	if res[0].ModifiedAt.IsZero() {
		t.Error("ModifiedAt was not parsed")
	}
}

// Collections are a flat list, so a lazy-load request for a child level has
// nothing to add.
func TestConnector_ListResources_NonEmptyParentIsEmpty(t *testing.T) {
	f := newFakeOutline(nil)
	defer f.Close()

	res, err := NewConnector().ListResources(context.Background(), makeDSConfig(f, nil), "col-1")
	if err != nil {
		t.Fatalf("ListResources: %v", err)
	}
	if len(res) != 0 {
		t.Errorf("len = %d, want 0 for a non-empty parentID", len(res))
	}
}

func TestConnector_ResolveResourceAncestors_Empty(t *testing.T) {
	f := newFakeOutline(nil)
	defer f.Close()

	got, err := NewConnector().ResolveResourceAncestors(
		context.Background(), makeDSConfig(f, nil), []string{"col-1"})
	if err != nil {
		t.Fatalf("ResolveResourceAncestors: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("len = %d, want 0 (a flat resource list has no ancestors)", len(got))
	}
}
