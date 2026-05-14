// cli/acceptance/contract/wire_test.go
//
// Wire contract test. Drives root cobra in-process for each scenario,
// captures stdout + stderr, and asserts:
//
//   - stdout matches a JSON golden in cli/acceptance/testdata/wire/
//   - on wantErr cases, stderr contains the expected typed error code
//
// Successful cases produce bare JSON on stdout (no envelope wrapper);
// failure cases produce empty stdout (or, for `doctor`, the data object
// the command writes before returning SilentError) and a `code: msg`
// line on stderr.
//
// Cases intentionally omitted (with reason):
//   - doctor.success                            — non-offline path emits
//                                                 unstable timing
//                                                 ("reachable in 2ms").
//                                                 Unit tests in cli/cmd/doctor
//                                                 cover the all-pass shape;
//                                                 doctor.success_offline is
//                                                 the deterministic sibling
//                                                 kept here.
//   - auth_login.success                        — requires stdin pipe
//                                                 (--with-token) + keyring-
//                                                 aware Secrets store; the
//                                                 helper does not yet expose
//                                                 a stdin hook.
//   - auth_login.error_auth_unauthenticated     — same setup as above.
//
// All cases use leaf-positioned --json (e.g. `version --json`). --json is a
// per-leaf flag, not a global persistent flag.
package contract_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/cli/internal/config"
	sdk "github.com/Tencent/WeKnora/client"
)

// wireCase declares one row in the contract matrix. Optional fields:
//   server               — mock /api/v1/* endpoints; nil means no network.
//   preConfig            — seed config.yaml under the per-test XDG_CONFIG_HOME
//                          (set by newTestFactory); use for cases like
//                          `context use` that read local state without an
//                          SDK round-trip.
//   wantErr              — non-zero exit expected.
//   wantStderrSubstring  — stderr must contain this substring (typically the
//                          typed error code, e.g. "auth.unauthenticated").
//                          Only meaningful when wantErr=true.
type wireCase struct {
	name                string
	args                []string
	server              http.HandlerFunc
	preConfig           func(t *testing.T)
	wantErr             bool
	wantStderrSubstring string
}

// wireCases enumerates every contract scenario whose stdout is golden-pinned.
// Order is illustrative, not load-bearing.
var wireCases = []wireCase{
	// 1. version.success — pure local; no client touched.
	{
		name: "version.success",
		args: []string{"version", "--json"},
	},

	// 2. doctor.success_offline — only credential_storage runs; the three
	//    network checks are skipped. Stable details + summary.
	{
		name:   "doctor.success_offline",
		args:   []string{"doctor", "--offline", "--json"},
		server: doctorReachable, // ensures buildServices succeeds even if probed
	},

	// 3. doctor.error_network — base_url returns 500 → ping fail → cascade
	//    skip on auth_credential + server_version. credential_storage still
	//    runs (independent). Contract: any check=fail bumps summary.failed
	//    and RunE returns SilentError → exit 1 with the data object
	//    written by emit() as the only stdout content.
	{
		name:    "doctor.error_network",
		args:    []string{"doctor", "--json"},
		server:  alwaysServerError,
		wantErr: true,
	},

	// 4-7. kb list / get — SDK paths /api/v1/knowledge-bases[/<id>]
	{
		name:   "kb_list.success",
		args:   []string{"kb", "list", "--json"},
		server: kbListTwo,
	},
	{
		name:   "kb_list.success_empty",
		args:   []string{"kb", "list", "--json"},
		server: kbListEmpty,
	},
	{
		name:                "kb_list.error_auth_forbidden",
		args:                []string{"kb", "list", "--json"},
		server:              always403,
		wantErr:             true,
		wantStderrSubstring: "auth.forbidden",
	},
	{
		name:   "kb_view.success",
		args:   []string{"kb", "view", "kb1", "--json"},
		server: kbGetOne,
	},
	{
		name:                "kb_view.error_resource_not_found",
		args:                []string{"kb", "view", "missing", "--json"},
		server:              always404,
		wantErr:             true,
		wantStderrSubstring: "resource.not_found",
	},

	// 8. context use — pure local I/O against config.yaml.
	{
		name: "context_use.success",
		args: []string{"context", "use", "production", "--json"},
		preConfig: func(t *testing.T) {
			cfg := &config.Config{
				CurrentContext: "staging",
				Contexts: map[string]config.Context{
					"staging":    {Host: "https://staging.example.com"},
					"production": {Host: "https://prod.example.com"},
				},
			}
			if err := config.Save(cfg); err != nil {
				t.Fatalf("seed config: %v", err)
			}
		},
	},
	// (context_use.error_local_context_not_found dropped — see file header.)

	// 9-10. auth status — SDK /api/v1/auth/me, plus config inspection.
	{
		name:   "auth_status.success",
		args:   []string{"auth", "status", "--json"},
		server: whoamiOK,
	},
	{
		name:                "auth_status.error_auth_unauthenticated",
		args:                []string{"auth", "status", "--json"},
		server:              always401,
		wantErr:             true,
		wantStderrSubstring: "auth.unauthenticated",
	},

	// 11-13. search chunks — verb-noun shape (gh search parity), positional query, --kb required.
	// --kb accepts either kb_<id> (passed through) or a name (resolved via
	// list); UUID-format detection happens client-side, mirroring gcloud
	// --project's id-or-name auto-detection.
	{
		name:   "search.success",
		args:   []string{"search", "chunks", "query", "--kb=11111111-1111-4111-8111-111111111111", "--limit=3", "--json"},
		server: searchTwoResults,
	},
	{
		name:                "search.error_resource_not_found",
		args:                []string{"search", "chunks", "query", "--kb=eeeeeeee-eeee-4eee-8eee-eeeeeeeeeeee", "--json"},
		server:              always404,
		wantErr:             true,
		wantStderrSubstring: "resource.not_found",
	},
	{
		// --no-vector + --no-keyword is the input.invalid case; the KB UUID
		// is just there to satisfy MarkFlagRequired so validation runs deep
		// enough to hit the mutex-channel check.
		name:                "search.error_input_invalid",
		args:                []string{"search", "chunks", "query", "--kb=11111111-1111-4111-8111-111111111111", "--no-vector", "--no-keyword", "--json"},
		wantErr:             true,
		wantStderrSubstring: "input.invalid_argument",
	},
}

// TestWireGolden is the matrix-runner. Cases are sequential (the
// iostreams singleton swap inside helpers.runCmd is package-global;
// t.Parallel is contractually forbidden — see helpers_test.go).
func TestWireGolden(t *testing.T) {
	for _, tc := range wireCases {
		t.Run(tc.name, func(t *testing.T) {
			var ts *httptest.Server
			var mockClient *sdk.Client
			if tc.server != nil {
				ts = httptest.NewServer(tc.server)
				defer ts.Close()
				mockClient = sdk.NewClient(ts.URL)
			}
			f := newTestFactory(t, ts, mockClient)
			if tc.preConfig != nil {
				tc.preConfig(t)
			}
			stdout, stderr, exit := runCmd(t, f, tc.args...)
			if tc.wantErr && exit == 0 {
				t.Errorf("expected non-zero exit, got 0; stdout=%q stderr=%q", stdout, stderr)
			}
			if !tc.wantErr && exit != 0 {
				t.Errorf("unexpected non-zero exit %d; stdout=%q stderr=%q", exit, stdout, stderr)
			}
			if tc.wantStderrSubstring != "" && !strings.Contains(stderr, tc.wantStderrSubstring) {
				t.Errorf("stderr missing %q; got %q", tc.wantStderrSubstring, stderr)
			}
			path := filepath.Join("..", "testdata", "wire", tc.name+".json")
			assertGolden(t, []byte(stdout), path)
		})
	}
}

// ---------------------------------------------------------------------------
// HTTP fixtures
//
// Handlers are intentionally permissive on path matching (HasSuffix) so they
// work whether the SDK adds the /api/v1 prefix or not. The SDK pins the
// /api/v1 prefix today; the suffix match keeps the fixtures resilient to
// future route renames as long as the leaf path stays stable.

// fixedTime is the deterministic timestamp embedded in KnowledgeBase fixtures.
// time.Time marshals to RFC3339; using a fixed value keeps the golden stable.
var fixedTime = time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)

func whoamiOK(w http.ResponseWriter, r *http.Request) {
	if !strings.HasSuffix(r.URL.Path, "/auth/me") {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	resp := sdk.CurrentUserResponse{Success: true}
	resp.Data.User = &sdk.AuthUser{ID: "usr_abc", Email: "user@example.com", TenantID: 42}
	resp.Data.Tenant = &sdk.AuthTenant{ID: 42, Name: "Acme"}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

func always401(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	_, _ = w.Write([]byte(`{"error":"unauthenticated"}`))
}

func always403(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusForbidden)
	_, _ = w.Write([]byte(`{"error":"forbidden"}`))
}

func always404(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusNotFound)
	_, _ = w.Write([]byte(`{"error":"not found"}`))
}

func alwaysServerError(w http.ResponseWriter, _ *http.Request) {
	// 5xx triggers PingBaseURL failure path and SDK transport error.
	w.WriteHeader(http.StatusInternalServerError)
	_, _ = w.Write([]byte(`internal error`))
}

// doctorReachable serves /health 200 (so PingBaseURL would succeed if it
// were called). doctor.success_offline still skips ping, so this handler
// is a no-op for that case but keeps buildServices on a happy path.
func doctorReachable(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
}

func kbListTwo(w http.ResponseWriter, r *http.Request) {
	if !strings.HasSuffix(r.URL.Path, "/knowledge-bases") {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	resp := sdk.KnowledgeBaseListResponse{
		Success: true,
		Data: []sdk.KnowledgeBase{
			{
				ID:               "kb1",
				Name:             "Onboarding Docs",
				TenantID:         42,
				EmbeddingModelID: "text-embedding-3",
				CreatedAt:        fixedTime,
				UpdatedAt:        fixedTime,
				KnowledgeCount:   5,
				ChunkCount:       128,
			},
			{
				ID:               "kb2",
				Name:             "API Reference",
				TenantID:         42,
				EmbeddingModelID: "text-embedding-3",
				CreatedAt:        fixedTime,
				UpdatedAt:        fixedTime,
				KnowledgeCount:   12,
				ChunkCount:       340,
			},
		},
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

func kbListEmpty(w http.ResponseWriter, r *http.Request) {
	if !strings.HasSuffix(r.URL.Path, "/knowledge-bases") {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	resp := sdk.KnowledgeBaseListResponse{Success: true, Data: []sdk.KnowledgeBase{}}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

func kbGetOne(w http.ResponseWriter, r *http.Request) {
	if !strings.HasSuffix(r.URL.Path, "/knowledge-bases/kb1") {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	resp := sdk.KnowledgeBaseResponse{
		Success: true,
		Data: sdk.KnowledgeBase{
			ID:               "kb1",
			Name:             "Onboarding Docs",
			Description:      "Internal onboarding handbook",
			TenantID:         42,
			EmbeddingModelID: "text-embedding-3",
			CreatedAt:        fixedTime,
			UpdatedAt:        fixedTime,
			KnowledgeCount:   5,
			ChunkCount:       128,
		},
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

func searchTwoResults(w http.ResponseWriter, r *http.Request) {
	if !strings.Contains(r.URL.Path, "/knowledge-bases/11111111-1111-4111-8111-111111111111/hybrid-search") {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	resp := sdk.HybridSearchResponse{
		Success: true,
		Data: []*sdk.SearchResult{
			{
				ID:             "chunk-1",
				Content:        "first chunk content",
				KnowledgeID:    "doc-1",
				ChunkIndex:     0,
				KnowledgeTitle: "Doc 1",
				Score:          0.92,
				MatchType:      sdk.MatchTypeVector,
			},
			{
				ID:             "chunk-2",
				Content:        "second chunk content",
				KnowledgeID:    "doc-2",
				ChunkIndex:     1,
				KnowledgeTitle: "Doc 2",
				Score:          0.81,
				MatchType:      sdk.MatchTypeKeyword,
			},
		},
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}
