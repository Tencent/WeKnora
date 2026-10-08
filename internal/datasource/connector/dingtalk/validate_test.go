package dingtalk

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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

// A workspace that only holds folders proves nothing: there was no document to
// read there. Validate must keep looking, and when the next workspace does hold
// a document the operator cannot read, the data source would sync nothing at
// all, so Validate must reject it.
//
// This is deliberately stricter than stopping at the first listed workspace: an
// unreadable document means an unusable data source, not an unrelated
// permission problem, so the failure must name that workspace and document.
func TestValidateRejectsTenantWhoseOnlyDocumentIsUnreadable(t *testing.T) {
	api := &fakeAPI{
		workspaces: []workspace{
			{ID: "folders", RootNodeID: "root-folders", Name: "Folders"},
			{ID: "locked", RootNodeID: "root-locked", Name: "Locked"},
		},
		nodes: map[string][]node{
			"root-folders": {
				{ID: "folder", Name: "Folder", Type: "FOLDER"},
			},
			"root-locked": {
				{ID: "doc-locked", Name: "Secret", Type: "FILE", Category: "ALIDOC", Extension: "adoc"},
			},
		},
		blockErrors: map[string]error{
			"doc-locked": errors.New("forbidden.accessDenied: the operator has no permission"),
		},
	}

	c := testConnector(api)
	err := c.Validate(context.Background(), testConfig())
	if err == nil {
		t.Fatal("Validate must fail: the only document in the tenant is unreadable, " +
			"so the data source would sync nothing")
	}
	for _, want := range []string{`workspace "Locked"`, `document "Secret"`, "no permission"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("Validate error should carry %s, got: %v", want, err)
		}
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
	// The operator has to find the offending object in DingTalk, so the error
	// names the workspace and the document of the last failed probe, not just
	// the provider cause.
	for _, want := range []string{`workspace "Beta"`, `document "Doc B"`, "no permission"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("Validate error should carry %s, got: %v", want, err)
		}
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

// An app without the document read permission fails every probe. Validate must
// probe at most one document per workspace and stop after maxValidateProbes,
// instead of firing one call per document across the whole tenant.
func TestValidateCapsProbesWhenEveryDocumentIsUnreadable(t *testing.T) {
	api := &fakeAPI{
		nodes:       map[string][]node{},
		blockErrors: map[string]error{},
	}
	denied := errors.New("forbidden.accessDenied: the operator has no permission")
	workspaces := 3 * maxValidateProbes
	for w := 0; w < workspaces; w++ {
		root := fmt.Sprintf("root-%d", w)
		api.workspaces = append(api.workspaces, workspace{ID: root, RootNodeID: root, Name: root})
		for d := 0; d < 4; d++ {
			id := fmt.Sprintf("doc-%d-%d", w, d)
			api.nodes[root] = append(api.nodes[root],
				node{ID: id, Name: id, Type: "FILE", Category: "ALIDOC", Extension: "adoc"})
			api.blockErrors[id] = denied
		}
	}

	c := testConnector(api)
	if err := c.Validate(context.Background(), testConfig()); err == nil {
		t.Fatal("Validate must fail when no visible document can be read")
	}
	total := 0
	for _, n := range api.blockCalls {
		total += n
	}
	if total > maxValidateProbes {
		t.Fatalf("Validate made %d document probes, want at most %d", total, maxValidateProbes)
	}
	for w := 0; w < workspaces; w++ {
		if api.blockCalls[fmt.Sprintf("doc-%d-1", w)] != 0 {
			t.Fatalf("workspace root-%d was probed past its first document", w)
		}
	}
}

// listCountingAPI counts listNodes calls so tests can bound the folder walk.
type listCountingAPI struct {
	*fakeAPI
	listCalls int
}

func (a *listCountingAPI) listNodes(ctx context.Context, parentID string) ([]node, error) {
	a.listCalls++
	return a.fakeAPI.listNodes(ctx, parentID)
}

// A workspace whose root holds only folders can still hold readable documents
// below them, and sync walks into folders to reach them. Validate must look
// there too instead of letting an unrelated unreadable workspace fail the data
// source, whichever order the workspaces are listed in.
func TestValidateFindsReadableDocumentBelowRootFolders(t *testing.T) {
	team := workspace{ID: "team", RootNodeID: "root-team", Name: "Team"}
	other := workspace{ID: "other", RootNodeID: "root-other", Name: "Other"}
	denied := errors.New("forbidden.accessDenied: the operator has no permission")
	cases := []struct {
		name       string
		workspaces []workspace
		nodeErrors map[string]error
	}{
		{name: "folder-only workspace first", workspaces: []workspace{team, other}},
		{name: "folder-only workspace second", workspaces: []workspace{other, team}},
		{
			name:       "other workspace unlistable",
			workspaces: []workspace{team, other},
			nodeErrors: map[string]error{"root-other": errors.New("DingTalk API status=500")},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			api := &fakeAPI{
				workspaces: tc.workspaces,
				nodes: map[string][]node{
					"root-team": {
						{ID: "folder", Name: "Folder", Type: "FOLDER"},
					},
					"folder": {
						{ID: "doc-team", Name: "Handbook", Type: "FILE", Category: "ALIDOC", Extension: "adoc"},
					},
					"root-other": {
						{ID: "doc-other", Name: "Secret", Type: "FILE", Category: "ALIDOC", Extension: "adoc"},
					},
				},
				nodeErrors: tc.nodeErrors,
				blocks: map[string][]json.RawMessage{
					"doc-team": {rawJSON(`{"paragraph":{"text":"hello"}}`)},
				},
				blockErrors: map[string]error{"doc-other": denied},
			}

			c := testConnector(api)
			if err := c.Validate(context.Background(), testConfig()); err != nil {
				t.Fatalf("Validate must find the readable document below the root folder, got: %v", err)
			}
			if api.blockCalls["doc-team"] != 1 {
				t.Fatalf("Validate should probe the document inside the folder, calls=%v", api.blockCalls)
			}
		})
	}
}

// A workspace with a deep folder tree must not turn Validate into an unbounded
// walk. When the listing budget runs out with folders still unexplored, an
// unreadable document elsewhere proves nothing, so Validate accepts.
func TestValidateCapsFolderListingsAndAcceptsWhenInconclusive(t *testing.T) {
	inner := &fakeAPI{
		workspaces: []workspace{
			{ID: "deep", RootNodeID: "root-deep", Name: "Deep"},
			{ID: "locked", RootNodeID: "root-locked", Name: "Locked"},
		},
		nodes: map[string][]node{
			"root-locked": {
				{ID: "doc-locked", Name: "Secret", Type: "FILE", Category: "ALIDOC", Extension: "adoc"},
			},
		},
		blockErrors: map[string]error{
			"doc-locked": errors.New("forbidden.accessDenied: the operator has no permission"),
		},
	}
	parent := "root-deep"
	for i := 0; i < 3*maxValidateListings; i++ {
		id := fmt.Sprintf("folder-%d", i)
		inner.nodes[parent] = []node{{ID: id, Name: id, Type: "FOLDER"}}
		parent = id
	}
	api := &listCountingAPI{fakeAPI: inner}

	c := testConnector(api)
	if err := c.Validate(context.Background(), testConfig()); err != nil {
		t.Fatalf("Validate must accept when folders were left unexplored, got: %v", err)
	}
	// Each workspace root is listed once on top of the shared folder budget.
	if limit := maxValidateListings + len(inner.workspaces); api.listCalls > limit {
		t.Fatalf("Validate made %d listings, want at most %d", api.listCalls, limit)
	}
}
