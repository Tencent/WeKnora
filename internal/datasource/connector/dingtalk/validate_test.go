package dingtalk

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// A tenant lists every workspace in the organisation, but the operator is only
// granted access to some of them. Validate must not fail because an unrelated,
// unreadable workspace happens to be returned first: app credentials are proven
// by one workspace that yields a readable document.
func TestValidateSkipsWorkspacesTheOperatorCannotRead(t *testing.T) {
	api := &fakeAPI{
		workspaces: []workspace{
			{ID: "locked", RootNodeID: "root-locked", Name: "Locked"},
			{ID: "open", RootNodeID: "root-open", Name: "Open"},
		},
		nodes: map[string][]node{
			"root-locked": {
				{ID: "doc-locked", Name: "Secret", Type: "FILE", Category: "ALIDOC", Extension: "adoc"},
			},
			"root-open": {
				{ID: "doc-open", Name: "Public", Type: "FILE", Category: "ALIDOC", Extension: "adoc"},
			},
		},
		blocks: map[string][]json.RawMessage{
			"doc-open": {rawJSON(`{"paragraph":{"text":"hello"}}`)},
		},
		blockErrors: map[string]error{
			"doc-locked": errors.New("forbidden.accessDenied: the operator has no permission"),
		},
	}

	c := testConnector(api)
	if err := c.Validate(context.Background(), testConfig()); err != nil {
		t.Fatalf("Validate must pass when a later workspace is readable, got: %v", err)
	}
}

// The first workspace exposes no document at all, so nothing is proven there.
// A later workspace that does expose a readable document must still be tried.
func TestValidateKeepsLookingPastWorkspacesWithoutDocuments(t *testing.T) {
	api := &fakeAPI{
		workspaces: []workspace{
			{ID: "empty", RootNodeID: "root-empty", Name: "Empty"},
			{ID: "docs", RootNodeID: "root-docs", Name: "Docs"},
		},
		nodes: map[string][]node{
			"root-empty": {
				{ID: "folder", Name: "Folder", Type: "FOLDER"},
			},
			"root-docs": {
				{ID: "doc", Name: "Handbook", Type: "FILE", Category: "ALIDOC", Extension: "adoc"},
			},
		},
		blocks: map[string][]json.RawMessage{
			"doc": {rawJSON(`{"paragraph":{"text":"hello"}}`)},
		},
	}

	c := testConnector(api)
	if err := c.Validate(context.Background(), testConfig()); err != nil {
		t.Fatalf("Validate must pass once a later workspace yields a document, got: %v", err)
	}
}

// Every visible document is unreadable, so the data source could never sync
// anything. That is worth reporting instead of accepting the credentials.
func TestValidateReportsWhenNoVisibleDocumentIsReadable(t *testing.T) {
	api := &fakeAPI{
		workspaces: []workspace{
			{ID: "a", RootNodeID: "root-a", Name: "Alpha"},
			{ID: "b", RootNodeID: "root-b", Name: "Beta"},
		},
		nodes: map[string][]node{
			"root-a": {
				{ID: "doc-a", Name: "Doc A", Type: "FILE", Category: "ALIDOC", Extension: "adoc"},
			},
			"root-b": {
				{ID: "doc-b", Name: "Doc B", Type: "FILE", Category: "ALIDOC", Extension: "adoc"},
			},
		},
		blockErrors: map[string]error{
			"doc-a": errors.New("forbidden.accessDenied: the operator has no permission"),
			"doc-b": errors.New("forbidden.accessDenied: the operator has no permission"),
		},
	}

	c := testConnector(api)
	err := c.Validate(context.Background(), testConfig())
	if err == nil {
		t.Fatal("Validate must fail when no visible document can be read")
	}
	if !strings.Contains(err.Error(), "no permission") {
		t.Fatalf("Validate error should carry the provider cause, got: %v", err)
	}
}

// A tenant with no documents anywhere leaves nothing to read, so the
// credentials cannot be disproved and Validate accepts them.
func TestValidateAcceptsTenantWithoutDocuments(t *testing.T) {
	api := &fakeAPI{
		workspaces: []workspace{{ID: "a", RootNodeID: "root-a", Name: "Alpha"}},
		nodes: map[string][]node{
			"root-a": {
				{ID: "folder", Name: "Folder", Type: "FOLDER"},
			},
		},
	}

	c := testConnector(api)
	if err := c.Validate(context.Background(), testConfig()); err != nil {
		t.Fatalf("Validate must accept a tenant that exposes no document, got: %v", err)
	}
}

// A workspace whose node listing fails must not abort the whole validation
// while another workspace can still prove the credentials.
func TestValidateSurvivesWorkspaceListingFailure(t *testing.T) {
	api := &fakeAPI{
		workspaces: []workspace{
			{ID: "broken", RootNodeID: "root-broken", Name: "Broken"},
			{ID: "ok", RootNodeID: "root-ok", Name: "Ok"},
		},
		nodeErrors: map[string]error{
			"root-broken": errors.New("DingTalk API status=500"),
		},
		nodes: map[string][]node{
			"root-ok": {
				{ID: "doc", Name: "Handbook", Type: "FILE", Category: "ALIDOC", Extension: "adoc"},
			},
		},
		blocks: map[string][]json.RawMessage{
			"doc": {rawJSON(`{"paragraph":{"text":"hello"}}`)},
		},
	}

	c := testConnector(api)
	if err := c.Validate(context.Background(), testConfig()); err != nil {
		t.Fatalf("Validate must survive one unlistable workspace, got: %v", err)
	}
}
