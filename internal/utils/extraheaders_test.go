package utils

import (
	"net/http"
	"strings"
	"testing"
)

func TestApplyCustomHeaders_SkipReserved(t *testing.T) {
	req, _ := http.NewRequest("GET", "https://example.com", nil)
	req.Header.Set("Authorization", "Bearer original")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-goog-api-key", "google-original")

	ApplyCustomHeaders(req, map[string]string{
		"Authorization":  "Bearer injected",
		"Content-Type":   "text/plain",
		"x-goog-api-key": "google-injected",
		"X-Trace-Id":     "trace-123",
		"X-Route":        "edge",
		"":               "empty-key-should-be-skipped",
	})

	if got := req.Header.Get("Authorization"); got != "Bearer original" {
		t.Fatalf("authorization overwritten: %q", got)
	}
	if got := req.Header.Get("Content-Type"); got != "application/json" {
		t.Fatalf("content-type overwritten: %q", got)
	}
	if got := req.Header.Get("x-goog-api-key"); got != "google-original" {
		t.Fatalf("x-goog-api-key overwritten: %q", got)
	}
	if got := req.Header.Get("X-Trace-Id"); got != "trace-123" {
		t.Fatalf("X-Trace-Id not injected: %q", got)
	}
	if got := req.Header.Get("X-Route"); got != "edge" {
		t.Fatalf("X-Route not injected: %q", got)
	}
}

func TestApplyCustomHeaders_NilSafe(t *testing.T) {
	ApplyCustomHeaders(nil, map[string]string{"x": "y"})
	req, _ := http.NewRequest("GET", "https://example.com", nil)
	ApplyCustomHeaders(req, nil)
	if len(req.Header) != 0 {
		t.Fatalf("unexpected headers added: %+v", req.Header)
	}
}

func TestResolveHeaderTemplates_ExpandsKnownVariables(t *testing.T) {
	headers := map[string]string{
		"X-User-Name":  "${user_name}",
		"X-User-Email": "${user_email}",
		"X-Combined":   "u=${user_id};t=${tenant_name}",
		"X-Static":     "plain-value",
	}
	vars := map[string]string{
		"user_id":     "u-1",
		"user_name":   "alice",
		"user_email":  "alice@example.com",
		"tenant_name": "acme",
	}

	resolved, unresolved := ResolveHeaderTemplates(headers, vars)

	if len(unresolved) != 0 {
		t.Fatalf("unexpected unresolved variables: %v", unresolved)
	}
	if got := resolved["X-User-Name"]; got != "alice" {
		t.Fatalf("X-User-Name = %q, want alice", got)
	}
	if got := resolved["X-User-Email"]; got != "alice@example.com" {
		t.Fatalf("X-User-Email = %q, want alice@example.com", got)
	}
	if got := resolved["X-Combined"]; got != "u=u-1;t=acme" {
		t.Fatalf("X-Combined = %q, want u=u-1;t=acme", got)
	}
	if got := resolved["X-Static"]; got != "plain-value" {
		t.Fatalf("X-Static = %q, want plain-value", got)
	}
}

func TestResolveHeaderTemplates_UnresolvedBecomesEmpty(t *testing.T) {
	headers := map[string]string{
		"X-User-Name": "${user_name}",
		"X-Other":     "${nope}",
	}

	resolved, unresolved := ResolveHeaderTemplates(headers, map[string]string{})

	if got := resolved["X-User-Name"]; got != "" {
		t.Fatalf("unresolved variable must expand to empty, got %q", got)
	}
	if got := resolved["X-Other"]; got != "" {
		t.Fatalf("unresolved variable must expand to empty, got %q", got)
	}
	if len(unresolved) != 2 || unresolved[0] != "nope" || unresolved[1] != "user_name" {
		t.Fatalf("unresolved = %v, want sorted [nope user_name]", unresolved)
	}
}

func TestResolveHeaderTemplates_DoesNotExpandTwice(t *testing.T) {
	// A variable whose value itself looks like a placeholder must not be
	// re-expanded: the gateway would receive a value it never configured.
	resolved, _ := ResolveHeaderTemplates(
		map[string]string{"X-Name": "${user_name}"},
		map[string]string{"user_name": "${tenant_name}", "tenant_name": "acme"},
	)
	if got := resolved["X-Name"]; got != "${tenant_name}" {
		t.Fatalf("X-Name = %q, want ${tenant_name} (no second expansion)", got)
	}
}

func TestResolveHeaderTemplates_StripsCRLF(t *testing.T) {
	// A value taken from user data must not be able to inject a new header.
	resolved, _ := ResolveHeaderTemplates(
		map[string]string{"X-Name": "${user_name}"},
		map[string]string{"user_name": "alice\r\nX-Injected: evil"},
	)
	if got := resolved["X-Name"]; got != "aliceX-Injected: evil" {
		t.Fatalf("X-Name = %q, want CR/LF stripped", got)
	}
	if strings.ContainsAny(resolved["X-Name"], "\r\n") {
		t.Fatalf("header value still contains a line break: %q", resolved["X-Name"])
	}
}

func TestResolveHeaderTemplates_IgnoresNonIdentifierPlaceholders(t *testing.T) {
	// ${金额} and ${} are not variables and must be left alone.
	resolved, unresolved := ResolveHeaderTemplates(
		map[string]string{"X-Text": "cost ${金额} ${}"},
		map[string]string{"user_name": "alice"},
	)
	if got := resolved["X-Text"]; got != "cost ${金额} ${}" {
		t.Fatalf("X-Text = %q, want non-identifier placeholders untouched", got)
	}
	if len(unresolved) != 0 {
		t.Fatalf("non-identifier placeholders must not be reported, got %v", unresolved)
	}
}

func TestResolveHeaderTemplates_DoesNotMutateInput(t *testing.T) {
	headers := map[string]string{"X-Name": "${user_name}"}
	ResolveHeaderTemplates(headers, map[string]string{"user_name": "alice"})
	if headers["X-Name"] != "${user_name}" {
		t.Fatalf("input map was mutated: %q", headers["X-Name"])
	}
}
