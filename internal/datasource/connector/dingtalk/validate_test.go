package dingtalk

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
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

// listCountingAPI counts node listing calls so tests can bound the folder walk.
// Past limit, when set, every listing fails, so a walk that never terminates
// shows up as a failed assertion instead of a hung test.
type listCountingAPI struct {
	*fakeAPI
	listCalls int
	limit     int
}

func (a *listCountingAPI) count() error {
	a.listCalls++
	if a.limit > 0 && a.listCalls > a.limit {
		return errors.New("listing limit exceeded")
	}
	return nil
}

func (a *listCountingAPI) listNodes(ctx context.Context, parentID string) ([]node, error) {
	if err := a.count(); err != nil {
		return nil, err
	}
	return a.fakeAPI.listNodes(ctx, parentID)
}

func (a *listCountingAPI) listNodesPage(ctx context.Context, parentID, pageToken string) ([]node, string, error) {
	if err := a.count(); err != nil {
		return nil, "", err
	}
	return a.fakeAPI.listNodesPage(ctx, parentID, pageToken)
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

// A folder that cannot be listed may be exactly where the operator's readable
// documents live, so it must not be treated as empty: an unreadable document
// in another workspace then proves nothing, whichever order they are listed in.
func TestValidateTreatsUnlistableFolderAsInconclusive(t *testing.T) {
	team := workspace{ID: "team", RootNodeID: "root-team", Name: "Team"}
	other := workspace{ID: "other", RootNodeID: "root-other", Name: "Other"}
	for _, order := range [][]workspace{{team, other}, {other, team}} {
		api := &fakeAPI{
			workspaces: order,
			nodes: map[string][]node{
				"root-team": {
					{ID: "folder", Name: "Folder", Type: "FOLDER"},
				},
				"root-other": {
					{ID: "doc-other", Name: "Secret", Type: "FILE", Category: "ALIDOC", Extension: "adoc"},
				},
			},
			nodeErrors: map[string]error{"folder": errors.New("DingTalk API status=500")},
			blockErrors: map[string]error{
				"doc-other": errors.New("forbidden.accessDenied: the operator has no permission"),
			},
		}

		c := testConnector(api)
		if err := c.Validate(context.Background(), testConfig()); err != nil {
			t.Fatalf("Validate with workspace %s first must accept when a folder could not be listed, got: %v",
				order[0].Name, err)
		}
	}
}

// A cancelled or timed-out listing proves nothing about the credentials, so
// Validate must report it instead of accepting the data source, including when
// an earlier workspace already left the walk inconclusive.
func TestValidateReturnsContextErrorsFromTheFolderWalk(t *testing.T) {
	t.Run("subfolder listing times out", func(t *testing.T) {
		api := &fakeAPI{
			workspaces: []workspace{{ID: "team", RootNodeID: "root-team", Name: "Team"}},
			nodes: map[string][]node{
				"root-team": {{ID: "folder", Name: "Folder", Type: "FOLDER"}},
			},
			nodeErrors: map[string]error{"folder": context.DeadlineExceeded},
		}
		err := testConnector(api).Validate(context.Background(), testConfig())
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("Validate must return the timeout, got: %v", err)
		}
	})
	t.Run("root listing cancelled after an inconclusive workspace", func(t *testing.T) {
		api := &fakeAPI{
			workspaces: []workspace{
				{ID: "team", RootNodeID: "root-team", Name: "Team"},
				{ID: "late", RootNodeID: "root-late", Name: "Late"},
			},
			nodes: map[string][]node{
				"root-team": {{ID: "folder", Name: "Folder", Type: "FOLDER"}},
			},
			nodeErrors: map[string]error{
				"folder":    errors.New("DingTalk API status=500"),
				"root-late": context.Canceled,
			},
		}
		err := testConnector(api).Validate(context.Background(), testConfig())
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Validate must return the cancellation, got: %v", err)
		}
	})
	t.Run("document probe times out after an inconclusive workspace", func(t *testing.T) {
		api := &fakeAPI{
			workspaces: []workspace{
				{ID: "team", RootNodeID: "root-team", Name: "Team"},
				{ID: "other", RootNodeID: "root-other", Name: "Other"},
			},
			nodes: map[string][]node{
				"root-team": {{ID: "folder", Name: "Folder", Type: "FOLDER"}},
				"root-other": {
					{ID: "doc-other", Name: "Doc", Type: "FILE", Category: "ALIDOC", Extension: "adoc"},
				},
			},
			nodeErrors:  map[string]error{"folder": errors.New("DingTalk API status=500")},
			blockErrors: map[string]error{"doc-other": context.DeadlineExceeded},
		}
		err := testConnector(api).Validate(context.Background(), testConfig())
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("Validate must return the timeout, got: %v", err)
		}
	})
}

// DingTalk can list a folder, or the workspace root itself, among a node's
// children. The walk must list each folder once instead of repeating the same
// listings until the request is cancelled.
func TestValidateFolderWalkSurvivesCycles(t *testing.T) {
	inner := &fakeAPI{
		workspaces: []workspace{
			{ID: "loop", RootNodeID: "root-loop", Name: "Loop"},
			{ID: "locked", RootNodeID: "root-locked", Name: "Locked"},
		},
		nodes: map[string][]node{
			"root-loop": {
				{ID: "root-loop", Name: "Self", Type: "FOLDER"},
				{ID: "folder", Name: "Folder", Type: "FOLDER"},
			},
			"folder": {
				{ID: "root-loop", Name: "Parent", Type: "FOLDER"},
				{ID: "folder", Name: "Self", Type: "FOLDER"},
			},
			"root-locked": {
				{ID: "doc-locked", Name: "Secret", Type: "FILE", Category: "ALIDOC", Extension: "adoc"},
			},
		},
		blockErrors: map[string]error{
			"doc-locked": errors.New("forbidden.accessDenied: the operator has no permission"),
		},
	}
	api := &listCountingAPI{fakeAPI: inner, limit: 100}

	err := testConnector(api).Validate(context.Background(), testConfig())
	// root-loop, folder and root-locked are each listed exactly once.
	if api.listCalls != 3 {
		t.Fatalf("Validate made %d listings, want 3", api.listCalls)
	}
	// Every folder was explored, so the unreadable document is conclusive.
	if err == nil || !strings.Contains(err.Error(), `document "Secret"`) {
		t.Fatalf("Validate must report the unreadable document, got: %v", err)
	}
}

// The listing budget must bound real HTTP requests: a folder can hold
// thousands of nodes served 50 per page, and listing it whole would turn one
// budget unit into hundreds of serial requests.
func TestValidateBudgetCountsNodeListingPages(t *testing.T) {
	const folderPages = 200
	cases := []struct {
		name         string
		documentPage int // folder page holding a readable document; 0 for none
		wantRequests int
	}{
		{name: "no document", wantRequests: 1 + maxValidateListings},
		{name: "document on third page", documentPage: 3, wantRequests: 1 + 3},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var mu sync.Mutex
			nodeRequests, blockRequests := 0, 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch {
				case r.URL.Path == "/v1.0/oauth2/accessToken":
					_, _ = w.Write([]byte(`{"accessToken":"token","expireIn":7200}`))
				case r.URL.Path == "/v2.0/wiki/workspaces":
					_, _ = w.Write([]byte(`{"workspaces":[{"workspaceId":"a","rootNodeId":"root-a","name":"A"}]}`))
				case r.URL.Path == "/v2.0/wiki/nodes":
					mu.Lock()
					nodeRequests++
					mu.Unlock()
					if r.URL.Query().Get("parentNodeId") == "root-a" {
						_, _ = w.Write([]byte(`{"nodes":[{"nodeId":"folder","type":"FOLDER"}]}`))
						return
					}
					page, _ := strconv.Atoi(r.URL.Query().Get("nextToken"))
					page++
					item := fmt.Sprintf(`{"nodeId":"pdf-%d","type":"FILE","category":"FILE","extension":"pdf"}`, page)
					if page == tc.documentPage {
						item = `{"nodeId":"doc","name":"Doc","type":"FILE","category":"ALIDOC","extension":"adoc"}`
					}
					next := ""
					if page < folderPages {
						next = strconv.Itoa(page)
					}
					_, _ = fmt.Fprintf(w, `{"nodes":[%s],"nextToken":%q}`, item, next)
				case strings.HasPrefix(r.URL.Path, "/v1.0/doc/suites/documents/"):
					mu.Lock()
					blockRequests++
					mu.Unlock()
					_, _ = w.Write([]byte(`{"success":true,"result":{"data":[{"blockType":"paragraph"}]}}`))
				default:
					http.NotFound(w, r)
				}
			}))
			defer server.Close()

			c := testConnector(testClient(server))
			if err := c.Validate(context.Background(), testConfig()); err != nil {
				t.Fatalf("Validate must accept, got: %v", err)
			}
			mu.Lock()
			defer mu.Unlock()
			if nodeRequests != tc.wantRequests {
				t.Fatalf("Validate made %d node listing requests, want %d", nodeRequests, tc.wantRequests)
			}
			if wantBlocks := min(tc.documentPage, 1); blockRequests != wantBlocks {
				t.Fatalf("Validate made %d document probes, want %d", blockRequests, wantBlocks)
			}
		})
	}
}

// A folder's later pages must be read before its subfolders: otherwise a root
// whose first page is all folders spends the budget below them and never sees
// the document on the root's second page.
func TestValidateReadsFolderPagesBeforeDescending(t *testing.T) {
	var mu sync.Mutex
	probed := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/v1.0/oauth2/accessToken":
			_, _ = w.Write([]byte(`{"accessToken":"token","expireIn":7200}`))
		case r.URL.Path == "/v2.0/wiki/workspaces":
			_, _ = w.Write([]byte(`{"workspaces":[{"workspaceId":"a","rootNodeId":"root-a","name":"A"}]}`))
		case r.URL.Path == "/v2.0/wiki/nodes":
			parent := r.URL.Query().Get("parentNodeId")
			switch {
			case parent == "root-a" && r.URL.Query().Get("nextToken") == "":
				folders := make([]string, 0, maxValidateListings)
				for i := 0; i < maxValidateListings; i++ {
					folders = append(folders, fmt.Sprintf(`{"nodeId":"folder-%d","type":"FOLDER"}`, i))
				}
				_, _ = fmt.Fprintf(w, `{"nodes":[%s],"nextToken":"p2"}`, strings.Join(folders, ","))
			case parent == "root-a":
				_, _ = w.Write([]byte(
					`{"nodes":[{"nodeId":"doc","name":"Doc","type":"FILE","category":"ALIDOC","extension":"adoc"}]}`))
			default:
				_, _ = w.Write([]byte(`{"nodes":[{"nodeId":"pdf","type":"FILE","category":"FILE","extension":"pdf"}]}`))
			}
		case strings.HasPrefix(r.URL.Path, "/v1.0/doc/suites/documents/"):
			mu.Lock()
			probed = true
			mu.Unlock()
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"code":"forbidden.accessDenied","message":"no permission"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	err := testConnector(testClient(server)).Validate(context.Background(), testConfig())
	mu.Lock()
	defer mu.Unlock()
	if !probed {
		t.Fatalf("the document on the root's second page was never probed (err=%v)", err)
	}
	if err == nil {
		t.Fatal("Validate must report the unreadable document instead of accepting")
	}
}

// A folder that keeps returning the same nextToken must not be paged again
// until the budget runs out; the rest of it is unknown, so Validate accepts.
func TestValidateStopsOnRepeatedPageToken(t *testing.T) {
	var mu sync.Mutex
	folderRequests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1.0/oauth2/accessToken":
			_, _ = w.Write([]byte(`{"accessToken":"token","expireIn":7200}`))
		case "/v2.0/wiki/workspaces":
			_, _ = w.Write([]byte(`{"workspaces":[{"workspaceId":"a","rootNodeId":"root-a","name":"A"}]}`))
		case "/v2.0/wiki/nodes":
			if r.URL.Query().Get("parentNodeId") == "root-a" {
				_, _ = w.Write([]byte(`{"nodes":[{"nodeId":"folder","type":"FOLDER"}]}`))
				return
			}
			mu.Lock()
			folderRequests++
			mu.Unlock()
			_, _ = w.Write([]byte(
				`{"nodes":[{"nodeId":"pdf","type":"FILE","category":"FILE","extension":"pdf"}],"nextToken":"same"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	if err := testConnector(testClient(server)).Validate(context.Background(), testConfig()); err != nil {
		t.Fatalf("Validate must accept an inconclusive walk, got: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if folderRequests != 2 {
		t.Fatalf("folder was listed %d times, want 2 (first page + the repeated token once)", folderRequests)
	}
}
