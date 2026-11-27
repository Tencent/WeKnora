package client

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// The handlers read "q" and "limit" and answer with a JSON array in "data".
func TestSearchOrganizationsMatchesServerContract(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/organizations/search" {
			t.Errorf("path = %s", r.URL.Path)
		}
		if q := r.URL.Query(); q.Get("q") != "acme" || q.Get("limit") != "5" {
			t.Errorf("query = %s", r.URL.RawQuery)
		}
		_, _ = w.Write([]byte(`{"success":true,"total":1,"data":[` +
			`{"id":"org-1","name":"Acme","member_count":3}]}`))
	}))
	defer srv.Close()

	orgs, err := NewClient(srv.URL).SearchOrganizations(context.Background(), "acme", 1, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(orgs) != 1 || orgs[0].ID != "org-1" || orgs[0].MemberCount != 3 {
		t.Fatalf("orgs = %+v", orgs)
	}
}

func TestSearchUsersForInviteMatchesServerContract(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/organizations/org-1/search-users" || r.URL.Query().Get("q") != "42" {
			t.Errorf("request = %s", r.URL.String())
		}
		_, _ = w.Write([]byte(`{"success":true,"data":[{"tenant_id":42,"tenant_name":"Acme",` +
			`"representative_user_id":"u-1","representative_username":"alice",` +
			`"representative_email":"alice@example.com"}]}`))
	}))
	defer srv.Close()

	users, err := NewClient(srv.URL).SearchUsersForInvite(context.Background(), "org-1", "42")
	if err != nil {
		t.Fatal(err)
	}
	if len(users) != 1 || users[0].ID != "u-1" || users[0].Username != "alice" ||
		users[0].Email != "alice@example.com" || users[0].TenantID != 42 {
		t.Fatalf("users = %+v", users)
	}
}
