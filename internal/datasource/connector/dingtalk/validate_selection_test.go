package dingtalk

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/datasource"
)

// A data source that names resources syncs those resources, so Validate has to
// check them instead of the whole tenant. Sampling the tenant answers a
// different question, and an unreadable workspace listed first can spend the
// probe budget before the selection is ever reached.

// recordingAPI records every node listing, so a test can prove which
// workspaces a validation walked. The fakeAPI counters only say which
// documents were probed.
type recordingAPI struct {
	*fakeAPI
	listed []string
}

func (a *recordingAPI) listNodes(ctx context.Context, parentID string) ([]node, error) {
	a.listed = append(a.listed, parentID)
	return a.fakeAPI.listNodes(ctx, parentID)
}

func (a *recordingAPI) listNodesPage(ctx context.Context, parentID, pageToken string) ([]node, string, error) {
	a.listed = append(a.listed, parentID)
	return a.fakeAPI.listNodesPage(ctx, parentID, pageToken)
}

// mustEncodeResource encodes a picker reference the way ListResources does, so
// the tests exercise the resource IDs a saved data source really carries.
func mustEncodeResource(t *testing.T, ref resourceReference) string {
	t.Helper()
	id, err := encodeResourceReference(ref)
	if err != nil {
		t.Fatalf("encode resource reference %+v: %v", ref, err)
	}
	return id
}

// realTenant mirrors the tenant shape this scoping was reported from: eight
// workspaces in the listing, the first ones full of documents the operator
// cannot read, and the reachable one — the fifth — behind them. The probe
// budget was spent on the first workspace, so the reachable documents were
// never probed and the data source could not be created, even though a sync of
// the same selection read every one of its documents.
func realTenant(t *testing.T, denied error) *fakeAPI {
	t.Helper()
	api := &fakeAPI{
		workspaces: []workspace{
			{ID: "security", RootNodeID: "root-security", Name: "Security"},
			{ID: "ops", RootNodeID: "root-ops", Name: "Operations"},
			{ID: "sales", RootNodeID: "root-sales", Name: "Sales"},
			{ID: "hr", RootNodeID: "root-hr", Name: "People"},
			{ID: "faq", RootNodeID: "root-faq", Name: "Product FAQ"},
			{ID: "pm", RootNodeID: "root-pm", Name: "Projects"},
			{ID: "design", RootNodeID: "root-design", Name: "Design"},
			{ID: "archive", RootNodeID: "root-archive", Name: "Archive"},
		},
		nodes: map[string][]node{
			"root-faq": {
				{ID: "faq-folder", Name: "FAQ", Type: "FOLDER"},
			},
			"faq-folder": {
				{ID: "faq-doc", Name: "Product FAQ", Type: "FILE", Category: "ALIDOC", Extension: "adoc"},
			},
			"root-design": {
				{ID: "design-folder", Name: "Design", Type: "FOLDER"},
			},
			"root-archive": {
				{ID: "archive-folder", Name: "Archive", Type: "FOLDER"},
			},
		},
		blocks: map[string][]json.RawMessage{
			"faq-doc": {rawJSON(`{"paragraph":{"text":"hello"}}`)},
		},
		blockErrors: map[string]error{},
	}
	// Every other workspace exposes documents this operator cannot read,
	// several of them enough to spend the whole probe budget.
	for _, workspaceID := range []string{"security", "ops", "sales", "hr", "pm"} {
		root := "root-" + workspaceID
		for i := 0; i < 6; i++ {
			id := fmt.Sprintf("%s-doc-%d", workspaceID, i)
			api.nodes[root] = append(api.nodes[root], node{
				ID: id, Name: id, Type: "FILE", Category: "ALIDOC", Extension: "adoc",
			})
			api.blockErrors[id] = denied
		}
	}
	return api
}

// A selected workspace or folder must be validated on its own: the readable
// document inside it decides the outcome, whatever the rest of the tenant
// looks like. On the tenant this was reported from, Validate failed here and
// the data source could not be created at all.
func TestValidateSamplesOnlyTheSelectedResources(t *testing.T) {
	denied := errors.New("forbidden.accessDenied: the operator has no permission")
	cases := []struct {
		name       string
		resourceID func(t *testing.T) string
	}{
		{
			name:       "the selected workspace",
			resourceID: func(*testing.T) string { return "faq" },
		},
		{
			name: "a folder inside the selected workspace",
			resourceID: func(t *testing.T) string {
				return mustEncodeResource(t, resourceReference{
					WorkspaceID: "faq", NodeID: "faq-folder",
				})
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			api := realTenant(t, denied)
			recorder := &recordingAPI{fakeAPI: api}
			err := testConnector(recorder).Validate(
				context.Background(), testConfig(tc.resourceID(t)),
			)
			if err != nil {
				t.Fatalf("Validate must accept a selection whose documents are readable, got: %v", err)
			}
			if api.blockCalls["faq-doc"] != 1 {
				t.Fatalf("the selected document was probed %d times, want 1", api.blockCalls["faq-doc"])
			}
			// The unrelated workspaces must not be walked at all: sampling
			// them is what spent the budget before the selection was reached.
			if len(recorder.listed) == 0 {
				t.Fatal("Validate never listed the selection")
			}
			for _, parent := range recorder.listed {
				if parent != "root-faq" && parent != "faq-folder" {
					t.Fatalf("Validate listed %q, which the selection does not name", parent)
				}
			}
		})
	}
}

// The mirror image: a readable document outside the selection must not vouch
// for a selection whose own documents are all unreadable. The data source
// would sync nothing, so Validate has to reject it.
func TestValidateRejectsASelectionWhoseDocumentsAreAllUnreadable(t *testing.T) {
	denied := errors.New("forbidden.accessDenied: the operator has no permission")
	api := &fakeAPI{
		workspaces: []workspace{
			{ID: "open", RootNodeID: "root-open", Name: "Open"},
			{ID: "locked", RootNodeID: "root-locked", Name: "Locked"},
		},
		nodes: map[string][]node{
			"root-open": {
				{ID: "doc-open", Name: "Public", Type: "FILE", Category: "ALIDOC", Extension: "adoc"},
			},
			"root-locked": {
				{ID: "locked-folder", Name: "Contracts", Type: "FOLDER"},
			},
			"locked-folder": {
				{ID: "locked-a", Name: "Contract A", Type: "FILE", Category: "ALIDOC", Extension: "adoc"},
				{ID: "locked-b", Name: "Contract B", Type: "FILE", Category: "ALIDOC", Extension: "adoc"},
			},
		},
		blocks: map[string][]json.RawMessage{
			"doc-open": {rawJSON(`{"paragraph":{"text":"hello"}}`)},
		},
		blockErrors: map[string]error{"locked-a": denied, "locked-b": denied},
	}
	selected := mustEncodeResource(t, resourceReference{
		WorkspaceID: "locked", NodeID: "locked-folder",
	})

	err := testConnector(api).Validate(context.Background(), testConfig(selected))
	if err == nil {
		t.Fatal("Validate must fail: every document in the selection is unreadable, " +
			"so the data source would sync nothing")
	}
	for _, want := range []string{`workspace "Locked"`, `document "Contract B"`, "no permission"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("Validate error should carry %s, got: %v", want, err)
		}
	}
	if strings.Contains(err.Error(), "only the first") {
		t.Fatalf("two probed documents must not be described as the probe cap, got: %v", err)
	}
	if api.blockCalls["doc-open"] != 0 {
		t.Fatalf("a document outside the selection was probed %d times", api.blockCalls["doc-open"])
	}
	if api.blockCalls["locked-a"] != 1 || api.blockCalls["locked-b"] != 1 {
		t.Fatalf("both selected documents should be probed, calls=%v", api.blockCalls)
	}
}

// A selection that holds no document at all has nothing to read, so the
// credentials cannot be disproved and Validate accepts. Folders and types with
// no ingest path do not count as documents.
func TestValidateAcceptsASelectionWithoutDocuments(t *testing.T) {
	api := &fakeAPI{
		workspaces: []workspace{{ID: "empty", RootNodeID: "root-empty", Name: "Empty"}},
		nodes: map[string][]node{
			"root-empty": {
				{ID: "empty-folder", Name: "Empty", Type: "FOLDER"},
			},
			"empty-folder": {
				{ID: "sub", Name: "Sub", Type: "FOLDER"},
				{ID: "clip", Name: "Clip", Type: "FILE", Category: "VIDEO", Extension: "mp4"},
			},
			"sub": {},
		},
	}
	selected := mustEncodeResource(t, resourceReference{
		WorkspaceID: "empty", NodeID: "empty-folder",
	})

	if err := testConnector(api).Validate(context.Background(), testConfig(selected)); err != nil {
		t.Fatalf("Validate must accept a selection that holds no document to read, got: %v", err)
	}
	if len(api.blockCalls) != 0 || len(api.verifyCalls) != 0 {
		t.Fatalf("a selection without ingestible documents must not be probed, blocks=%v verifies=%v",
			api.blockCalls, api.verifyCalls)
	}
}

// A cancelled or timed-out request proves nothing about the credentials, so it
// must never fall through to acceptance, not even when another selected scope
// would have proven them.
func TestValidateFailsWhenTheSelectionWalkIsCancelled(t *testing.T) {
	denied := errors.New("forbidden.accessDenied: the operator has no permission")
	t.Run("cancelled before the first call", func(t *testing.T) {
		api := realTenant(t, denied)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		err := testConnector(api).Validate(ctx, testConfig("faq"))
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Validate must report the cancellation, got: %v", err)
		}
		if api.blockCalls["faq-doc"] != 0 {
			t.Fatal("Validate must not probe anything under a cancelled context")
		}
	})
	t.Run("document probe times out", func(t *testing.T) {
		api := &fakeAPI{
			workspaces: []workspace{{ID: "faq", RootNodeID: "root-faq", Name: "FAQ"}},
			nodes: map[string][]node{
				"root-faq": {
					{ID: "faq-doc", Name: "FAQ", Type: "FILE", Category: "ALIDOC", Extension: "adoc"},
				},
			},
			blockErrors: map[string]error{"faq-doc": context.DeadlineExceeded},
		}
		err := testConnector(api).Validate(context.Background(), testConfig("faq"))
		if !errors.Is(err, context.DeadlineExceeded) ||
			!strings.Contains(err.Error(), `document "FAQ"`) {
			t.Fatalf("Validate must surface the timed-out probe with its document, got: %v", err)
		}
	})
	t.Run("resolution listing is cancelled", func(t *testing.T) {
		api := &fakeAPI{
			workspaces: []workspace{{ID: "faq", RootNodeID: "root-faq", Name: "FAQ"}},
			nodes:      map[string][]node{"root-faq": {{ID: "faq-folder", Type: "FOLDER"}}},
			nodeErrors: map[string]error{"root-faq": context.Canceled},
		}
		selected := mustEncodeResource(t, resourceReference{
			WorkspaceID: "faq", NodeID: "faq-folder",
		})
		err := testConnector(api).Validate(context.Background(), testConfig(selected))
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Validate must report the cancellation, got: %v", err)
		}
	})
	t.Run("a cancelled scope is not rescued by a readable one", func(t *testing.T) {
		api := &fakeAPI{
			workspaces: []workspace{
				{ID: "locked", RootNodeID: "root-locked", Name: "Locked"},
				{ID: "open", RootNodeID: "root-open", Name: "Open"},
			},
			nodes: map[string][]node{
				"root-locked": {
					{
						ID: "cancelled-doc", Name: "Cancelled", Type: "FILE",
						Category: "ALIDOC", Extension: "adoc",
					},
				},
				"root-open": {
					{ID: "open-folder", Name: "Handbook", Type: "FOLDER"},
				},
				"open-folder": {
					{
						ID: "open-doc", Name: "Handbook", Type: "FILE",
						Category: "ALIDOC", Extension: "adoc",
					},
				},
			},
			blocks:      map[string][]json.RawMessage{"open-doc": {rawJSON(`{"paragraph":{"text":"hi"}}`)}},
			blockErrors: map[string]error{"cancelled-doc": context.Canceled},
		}
		selected := []string{
			"locked",
			mustEncodeResource(t, resourceReference{WorkspaceID: "open", NodeID: "open-folder"}),
		}
		err := testConnector(api).Validate(context.Background(), testConfig(selected...))
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Validate must report the cancellation, got: %v", err)
		}
		if api.blockCalls["open-doc"] != 0 {
			t.Fatal("Validate must stop at the cancellation instead of accepting on the next scope")
		}
	})
}

// Without a selection there is nothing narrower to sample, so the whole tenant
// is walked exactly as before.
func TestValidateWithoutASelectionKeepsSamplingTheTenant(t *testing.T) {
	denied := errors.New("forbidden.accessDenied: the operator has no permission")
	t.Run("a readable later workspace still proves the credentials", func(t *testing.T) {
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
			blockErrors: map[string]error{"doc-locked": denied},
		}
		recorder := &recordingAPI{fakeAPI: api}

		if err := testConnector(recorder).Validate(context.Background(), testConfig()); err != nil {
			t.Fatalf("Validate must sample the whole tenant without a selection, got: %v", err)
		}
		if api.blockCalls["doc-locked"] != 1 || api.blockCalls["doc-open"] != 1 {
			t.Fatalf("every workspace should be sampled, calls=%v", api.blockCalls)
		}
		if !slices.Contains(recorder.listed, "root-locked") ||
			!slices.Contains(recorder.listed, "root-open") {
			t.Fatalf("the tenant walk must list every workspace, listed=%v", recorder.listed)
		}
	})
	t.Run("the probe cap still stops the tenant walk", func(t *testing.T) {
		// Deliberately unchanged: with no selection, the probe budget is still
		// shared across the tenant, so a workspace that spends all of it
		// still hides the readable workspace behind it. A selection is what
		// makes Validate answer for the operator's own scope.
		api := realTenant(t, denied)
		err := testConnector(api).Validate(context.Background(), testConfig())
		if err == nil {
			t.Fatal("Validate must fail when the probe cap stops the tenant walk")
		}
		wantNote := fmt.Sprintf("only the first %d documents were probed", maxValidateProbes)
		if !strings.Contains(err.Error(), wantNote) {
			t.Fatalf("Validate error should say the probe cap stopped the walk, got: %v", err)
		}
		if api.blockCalls["faq-doc"] != 0 {
			t.Fatal("the tenant walk must not have reached the later workspace")
		}
	})
}

// A reference this build cannot resolve must be reported, never quietly
// replaced by the tenant walk: the data source would fail on that resource,
// and a selection the picker can no longer resolve is what the operator has to
// see. The first case is the shape of a Base reference
// (dingtalk:v1?base=<id>) on a build whose resource IDs do not model Bases:
// the reference does not decode, so it cannot be validated and must not be
// silently dropped.
func TestValidateRejectsASelectionItCannotResolve(t *testing.T) {
	readableTenant := func() *fakeAPI {
		return &fakeAPI{
			workspaces: []workspace{{ID: "open", RootNodeID: "root-open", Name: "Open"}},
			nodes: map[string][]node{
				"root-open": {
					{ID: "doc-open", Name: "Public", Type: "FILE", Category: "ALIDOC", Extension: "adoc"},
				},
			},
			blocks: map[string][]json.RawMessage{
				"doc-open": {rawJSON(`{"paragraph":{"text":"hello"}}`)},
			},
		}
	}
	cases := []struct {
		name     string
		resource string
		want     string
		wantErr  error
	}{
		{
			name:     "a reference field this build does not model",
			resource: "dingtalk:v1?base=base-1",
			want:     "unsupported field",
		},
		{
			name:     "a workspace that is gone",
			resource: "gone",
			want:     "is unavailable",
			wantErr:  datasource.ErrResourceNotFound,
		},
		{
			name: "a node that is gone",
			resource: mustEncodeResource(t, resourceReference{
				WorkspaceID: "open", NodeID: "gone-node",
			}),
			want:    "is unavailable",
			wantErr: datasource.ErrResourceNotFound,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			api := readableTenant()
			err := testConnector(api).Validate(context.Background(), testConfig(tc.resource))
			if err == nil {
				t.Fatal("Validate must not accept a selection it cannot resolve")
			}
			if !strings.Contains(err.Error(), "resource "+tc.resource) {
				t.Fatalf("Validate error should name the resource, got: %v", err)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Validate error should carry %q, got: %v", tc.want, err)
			}
			if tc.wantErr != nil && !errors.Is(err, tc.wantErr) {
				t.Fatalf("Validate error should wrap %v, got: %v", tc.wantErr, err)
			}
			if len(api.blockCalls) != 0 {
				t.Fatalf("Validate must not fall back to the tenant walk, probed %v", api.blockCalls)
			}
		})
	}
	t.Run("a node with no ingest path", func(t *testing.T) {
		// The upload switch is off for testConfig, so the uploaded file the
		// reference names is not ingestible in this data source: the selection
		// would sync nothing, and a sync would report the resource as failed.
		api := readableTenant()
		api.nodes["root-open"] = []node{{
			ID: "doc-open", Name: "Upload", Type: "FILE", Category: "DOCUMENT", Extension: "pdf",
		}}
		selected := mustEncodeResource(t, resourceReference{WorkspaceID: "open", NodeID: "doc-open"})

		err := testConnector(api).Validate(context.Background(), testConfig(selected))
		if err == nil {
			t.Fatal("Validate must not accept a selection it cannot resolve")
		}
		if !strings.Contains(err.Error(), "not a supported online document or folder") {
			t.Fatalf("Validate error should name the unsupported node, got: %v", err)
		}
		if len(api.blockCalls) != 0 || len(api.verifyCalls) != 0 {
			t.Fatalf("Validate must not fall back to the tenant walk, blocks=%v verifies=%v",
				api.blockCalls, api.verifyCalls)
		}
	})
}

// A selected folder that cannot be listed cannot be scanned, so Validate
// reports it instead of accepting. The error names the folder, which is what
// the operator has to look at: the workspace alone would not say which one.
func TestValidateReportsASelectedFolderThatCannotBeListed(t *testing.T) {
	api := &fakeAPI{
		workspaces: []workspace{{ID: "team", RootNodeID: "root-team", Name: "Team"}},
		nodes: map[string][]node{
			"root-team": {
				{ID: "broken", Name: "Broken", Type: "FOLDER"},
			},
		},
		nodeErrors: map[string]error{"broken": errors.New("DingTalk API status=500")},
	}
	selected := mustEncodeResource(t, resourceReference{WorkspaceID: "team", NodeID: "broken"})

	err := testConnector(api).Validate(context.Background(), testConfig(selected))
	if err == nil {
		t.Fatal("Validate must not accept a selection it cannot scan")
	}
	for _, want := range []string{`workspace "Team"`, `node "broken"`, "status=500"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("Validate error should carry %s, got: %v", want, err)
		}
	}
}

// A selected document is a leaf: it is probed through its own read API instead
// of being walked, and a readable one is enough to accept the data source.
func TestValidateProbesASelectedDocumentDirectly(t *testing.T) {
	denied := errors.New("forbidden.accessDenied: the operator has no permission")
	t.Run("readable document", func(t *testing.T) {
		api := &fakeAPI{
			workspaces: []workspace{{ID: "open", RootNodeID: "root-open", Name: "Open"}},
			nodes: map[string][]node{
				"root-open": {
					{ID: "doc", Name: "Handbook", Type: "FILE", Category: "ALIDOC", Extension: "adoc"},
				},
			},
			blocks: map[string][]json.RawMessage{
				"doc": {rawJSON(`{"paragraph":{"text":"hello"}}`)},
			},
		}
		recorder := &recordingAPI{fakeAPI: api}
		selected := mustEncodeResource(t, resourceReference{WorkspaceID: "open", NodeID: "doc"})

		if err := testConnector(recorder).Validate(context.Background(), testConfig(selected)); err != nil {
			t.Fatalf("Validate must accept a readable selected document, got: %v", err)
		}
		if api.blockCalls["doc"] != 1 {
			t.Fatalf("the selected document was probed %d times, want 1", api.blockCalls["doc"])
		}
		if len(recorder.listed) != 1 || recorder.listed[0] != "root-open" {
			t.Fatalf("a selected document is resolved through its parent only, listed=%v", recorder.listed)
		}
	})
	t.Run("unreadable document", func(t *testing.T) {
		api := &fakeAPI{
			workspaces: []workspace{{ID: "open", RootNodeID: "root-open", Name: "Open"}},
			nodes: map[string][]node{
				"root-open": {
					{ID: "doc", Name: "Handbook", Type: "FILE", Category: "ALIDOC", Extension: "adoc"},
				},
			},
			blockErrors: map[string]error{"doc": denied},
		}
		selected := mustEncodeResource(t, resourceReference{WorkspaceID: "open", NodeID: "doc"})

		err := testConnector(api).Validate(context.Background(), testConfig(selected))
		if err == nil || !strings.Contains(err.Error(), `document "Handbook"`) {
			t.Fatalf("Validate must report the unreadable selected document, got: %v", err)
		}
	})
	t.Run("readable uploaded file", func(t *testing.T) {
		api := selectedUpload(nil)
		selected := mustEncodeResource(t, resourceReference{WorkspaceID: "open", NodeID: "file"})

		err := testConnector(api).Validate(context.Background(), testConfigWithUploadedFiles(selected))
		if err != nil {
			t.Fatalf("Validate must accept a readable selected upload, got: %v", err)
		}
		// An uploaded file has no blocks: its download location is resolved,
		// which exercises the same permissions without transferring the bytes.
		if api.verifyCalls["file"] != 1 || api.dlCalls["file"] != 0 {
			t.Fatalf("upload should be verified through its download location, verify=%v download=%v",
				api.verifyCalls, api.dlCalls)
		}
	})
	t.Run("unreadable uploaded file", func(t *testing.T) {
		api := selectedUpload(denied)
		selected := mustEncodeResource(t, resourceReference{WorkspaceID: "open", NodeID: "file"})

		err := testConnector(api).Validate(context.Background(), testConfigWithUploadedFiles(selected))
		if err == nil || !strings.Contains(err.Error(), `document "Handbook.pdf"`) {
			t.Fatalf("Validate must report the unreadable selected upload, got: %v", err)
		}
	})
}

// selectedUpload is a tenant whose only selected node is an uploaded PDF, so
// the selection walk has to use the download verification path.
func selectedUpload(verifyErr error) *fakeAPI {
	api := &fakeAPI{
		workspaces: []workspace{{ID: "open", RootNodeID: "root-open", Name: "Open"}},
		nodes: map[string][]node{
			"root-open": {
				{
					ID: "file", Name: "Handbook.pdf", Type: "FILE",
					Category: "DOCUMENT", Extension: "pdf",
				},
			},
		},
	}
	if verifyErr != nil {
		api.verifyErrors = map[string]error{"file": verifyErr}
	}
	return api
}

// One selected scope that yields a readable document proves the credentials,
// exactly as one readable workspace does for the tenant walk: the other
// selected scopes stay in the report of the sync that will follow, they do not
// reject the data source.
func TestValidateAcceptsASelectionWhenOneScopeIsReadable(t *testing.T) {
	denied := errors.New("forbidden.accessDenied: the operator has no permission")
	api := &fakeAPI{
		workspaces: []workspace{
			{ID: "locked", RootNodeID: "root-locked", Name: "Locked"},
			{ID: "open", RootNodeID: "root-open", Name: "Open"},
		},
		nodes: map[string][]node{
			"root-locked": {
				{ID: "locked-folder", Name: "Contracts", Type: "FOLDER"},
			},
			"locked-folder": {
				{ID: "locked-doc", Name: "Contract", Type: "FILE", Category: "ALIDOC", Extension: "adoc"},
			},
			"root-open": {
				{ID: "open-folder", Name: "Handbook", Type: "FOLDER"},
			},
			"open-folder": {
				{ID: "open-doc", Name: "Handbook", Type: "FILE", Category: "ALIDOC", Extension: "adoc"},
			},
		},
		blocks: map[string][]json.RawMessage{
			"open-doc": {rawJSON(`{"paragraph":{"text":"hello"}}`)},
		},
		blockErrors: map[string]error{"locked-doc": denied},
	}
	selected := []string{
		mustEncodeResource(t, resourceReference{WorkspaceID: "locked", NodeID: "locked-folder"}),
		mustEncodeResource(t, resourceReference{WorkspaceID: "open", NodeID: "open-folder"}),
	}

	if err := testConnector(api).Validate(context.Background(), testConfig(selected...)); err != nil {
		t.Fatalf("Validate must accept when one selected scope yields a readable document, got: %v", err)
	}
	if api.blockCalls["locked-doc"] != 1 || api.blockCalls["open-doc"] != 1 {
		t.Fatalf("both selected scopes should be probed, calls=%v", api.blockCalls)
	}
}
