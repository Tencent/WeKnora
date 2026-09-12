package invoke

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	secutils "github.com/Tencent/WeKnora/internal/utils"
)

// listProbeAdapter is a minimal ListModelsAdapter fake for entry-level probe
// tests (SSRF gate, result cap); the wire-shape parity lives in the adapters
// package tests. It serves ONLY the listing facet, so the declaration carries
// the Common.ModelListing shard alone (registration lock #2).
type listProbeAdapter struct {
	results []RemoteModel
}

func (a *listProbeAdapter) Provider() string { return "fake" }

func (a *listProbeAdapter) Capabilities() Capabilities {
	return Capabilities{
		Common: CommonCaps{ModelListing: ModelListingCaps{Supported: true}},
	}
}

func (a *listProbeAdapter) BuildListRequest(ep Endpoint, _ ListOptions) (*Request, error) {
	return &Request{Method: http.MethodGet, URL: ep.BaseURL + "/models"}, nil
}

func (a *listProbeAdapter) ParseListResponse(_ int, _ http.Header, _ []byte) ([]RemoteModel, error) {
	return a.results, nil
}

// allowProbeHost whitelists a fake probe host for one test: the URL only has
// to clear the entry's SSRF gate to reach the facet check (请求不真正拨号，
// 且避开本环境 fake-IP DNS).
func allowProbeHost(t *testing.T) {
	t.Helper()
	t.Setenv("SSRF_WHITELIST", "probe.example.com")
	secutils.ResetSSRFWhitelistForTest()
	t.Cleanup(secutils.ResetSSRFWhitelistForTest)
}

// TestListEntrySSRF verifies the probe refuses internal targets — the
// endpoint must not become an internal-network scanning channel (v1
// catalog/remote.go gate, now entry-owned).
func TestListEntrySSRF(t *testing.T) {
	for _, target := range []string{
		"http://127.0.0.1:8080",
		"http://169.254.169.254/latest/meta-data",
		"http://localhost:11434",
	} {
		if _, err := List(t.Context(), "fake", &ListOptions{BaseURL: target}); err == nil {
			t.Errorf("List(%q) must fail SSRF validation", target)
		}
	}
}

func TestListEntryRequiresBaseURL(t *testing.T) {
	if _, err := List(t.Context(), "fake", &ListOptions{}); err == nil {
		t.Fatal("List without base URL must fail")
	}
}

// TestListEntryResultCap pins the 500-result cap (design §5.10.2). The stub
// server exists because the entry hands the built request to the REAL
// executor — the probe goes over the wire even against a fake adapter.
func TestListEntryResultCap(t *testing.T) {
	allowLoopbackSSRF(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"data":[]}`))
	}))
	defer srv.Close()

	results := make([]RemoteModel, maxRemoteModels+50)
	for i := range results {
		results[i] = RemoteModel{ID: fmt.Sprintf("m-%d", i)}
	}
	registerFake(t, &listProbeAdapter{results: results})
	models, err := List(t.Context(), "fake", &ListOptions{BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("List = %v", err)
	}
	if len(models) != maxRemoteModels {
		t.Errorf("models = %d, want capped at %d", len(models), maxRemoteModels)
	}
}

func TestProbeTimeoutDefault(t *testing.T) {
	t.Setenv("WEKNORA_REMOTE_CATALOG_TIMEOUT_SECONDS", "")
	if got := probeTimeout(); got != defaultProbeTimeout {
		t.Errorf("probeTimeout default = %v, want %v", got, defaultProbeTimeout)
	}
	t.Setenv("WEKNORA_REMOTE_CATALOG_TIMEOUT_SECONDS", "3")
	if got := probeTimeout(); got != 3*time.Second {
		t.Errorf("probeTimeout(3) = %v, want 3s", got)
	}
	// Invalid values fall back to the default rather than zeroing the probe.
	t.Setenv("WEKNORA_REMOTE_CATALOG_TIMEOUT_SECONDS", "bogus")
	if got := probeTimeout(); got != defaultProbeTimeout {
		t.Errorf("probeTimeout(bogus) = %v, want default", got)
	}
}

// paginatedProbeAdapter opts into the entry's pagination loop: page 1 serves
// a FULL page (2 models), page 2 a partial one (1) — the loop must stop on
// the short page. It also records the ModelType/PageNo it was asked for.
type paginatedProbeAdapter struct {
	listProbeAdapter
	queries []ListOptions
}

func (a *paginatedProbeAdapter) ListPageSize() int { return 2 }

func (a *paginatedProbeAdapter) BuildListRequest(ep Endpoint, opts ListOptions) (*Request, error) {
	a.queries = append(a.queries, opts)
	return &Request{
		Method: http.MethodGet,
		URL:    fmt.Sprintf("%s/models?page_no=%d&model_type=%s", ep.BaseURL, opts.PageNo, opts.ModelType),
	}, nil
}

func (a *paginatedProbeAdapter) ParseListResponse(_ int, _ http.Header, _ []byte) ([]RemoteModel, error) {
	switch len(a.queries) {
	case 1:
		return []RemoteModel{{ID: "p1-a"}, {ID: "p1-b"}}, nil // 满页 → 续拉
	default:
		return []RemoteModel{{ID: "p2-a"}}, nil // 短页 → 停
	}
}

// TestListEntryPaginationLoop pins the 2026-09-12 ruling ③ mechanics: a
// PaginatedLister adapter gets page-looped probes (full page → next page,
// short page stops) and every page carries the ModelType filter verbatim;
// non-paginated adapters keep the single-shot contract.
func TestListEntryPaginationLoop(t *testing.T) {
	allowLoopbackSSRF(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"output":{"models":[]}}`))
	}))
	defer srv.Close()

	fake := &paginatedProbeAdapter{}
	registerFake(t, fake)
	models, err := List(t.Context(), "fake", &ListOptions{BaseURL: srv.URL, ModelType: "embedding"})
	if err != nil {
		t.Fatalf("List = %v", err)
	}
	if len(models) != 3 {
		t.Fatalf("models = %d, want 3 (full page + short page)", len(models))
	}
	if len(fake.queries) != 2 {
		t.Fatalf("probe calls = %d, want 2", len(fake.queries))
	}
	if fake.queries[0].PageNo != 1 || fake.queries[1].PageNo != 2 {
		t.Errorf("page numbers = %d,%d, want 1,2", fake.queries[0].PageNo, fake.queries[1].PageNo)
	}
	for i, q := range fake.queries {
		if q.ModelType != "embedding" {
			t.Errorf("query[%d].ModelType = %q, want forwarded verbatim", i, q.ModelType)
		}
	}

	// 单发契约：未实现 PaginatedLister 的适配器只发一次（即使满页）。
	single := &listProbeAdapter{results: []RemoteModel{{ID: "a"}, {ID: "b"}, {ID: "c"}}}
	registerFake(t, single)
	models, err = List(t.Context(), "fake", &ListOptions{BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("single-shot List = %v", err)
	}
	if len(models) != 3 {
		t.Fatalf("single-shot models = %d, want 3", len(models))
	}
}
