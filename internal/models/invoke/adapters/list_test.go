package adapters

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/invoke"
	secutils "github.com/Tencent/WeKnora/internal/utils"
)

// TestOpenAIListURL pins the version-segment handling (v1 catalog/remote.go
// parity): bases that already carry a version path append /models; bare bases
// get /v1/models.
func TestOpenAIListURL(t *testing.T) {
	cases := map[string]string{
		"https://api.openai.com/v1":                         "https://api.openai.com/v1/models",
		"https://dashscope.aliyuncs.com/compatible-mode/v1": "https://dashscope.aliyuncs.com/compatible-mode/v1/models",
		"https://generativelanguage.googleapis.com/v1beta":  "https://generativelanguage.googleapis.com/v1beta/models",
		"http://localhost:8000":                             "http://localhost:8000/v1/models",
		"https://openrouter.ai/api/v1":                      "https://openrouter.ai/api/v1/models",
	}
	for base, want := range cases {
		if got := openAIListURL(base); got != want {
			t.Errorf("openAIListURL(%q) = %q, want %q", base, got, want)
		}
	}
}

func TestAnthropicListURL(t *testing.T) {
	cases := map[string]string{
		"https://api.anthropic.com":      "https://api.anthropic.com/v1/models",
		"https://api.anthropic.com/v1":   "https://api.anthropic.com/v1/models",
		"https://proxy.example.com/anth": "https://proxy.example.com/anth/v1/models",
	}
	for base, want := range cases {
		if got := anthropicListURL(base); got != want {
			t.Errorf("anthropicListURL(%q) = %q, want %q", base, got, want)
		}
	}
}

// TestAzureListRejected pins the deployment-mode rejection: the request must
// never go out (v1 let it 404 server-side; DRIFT recorded — same handler
// degrade, different reason text).
func TestAzureListRejected(t *testing.T) {
	a := &openaiAdapter{name: "azure_openai", spec: openaiVendorSpec{azure: true}}
	if _, err := a.BuildListRequest(invoke.Endpoint{BaseURL: "https://x.openai.azure.com"}); err == nil {
		t.Fatal("azure BuildListRequest must fail explicitly")
	}
}

// TestListEntryLive exercises the full invoke.List path against a loopback
// stub — allowlisted exactly like a private vLLM deployment, so the probe
// inherits the deployment's SSRF policy rather than bypassing it.
func TestListEntryLive(t *testing.T) {
	secutils.SetSSRFWhitelistFromRaw("127.0.0.1")
	t.Cleanup(secutils.ResetSSRFWhitelistForTest)
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		if r.URL.Path != "/models" && r.URL.Path != "/v1/models" {
			t.Errorf("unexpected list path %q", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"data":[{"id":"m-a","owned_by":"org"},{"id":"m-b"},{"id":""}]}`))
	}))
	defer srv.Close()

	models, err := invoke.List(t.Context(), "openrouter", &invoke.ListOptions{
		BaseURL:     srv.URL,
		Credentials: invoke.Credentials{APIKey: "sekret"},
	})
	if err != nil {
		t.Fatalf("invoke.List = %v", err)
	}
	if gotAuth != "Bearer sekret" {
		t.Errorf("Authorization = %q, want Bearer sekret", gotAuth)
	}
	if len(models) != 2 {
		t.Fatalf("models = %d, want 2 (empty ids dropped)", len(models))
	}
	if models[0].ID != "m-a" || models[0].OwnedBy != "org" {
		t.Errorf("models[0] = %+v", models[0])
	}
}

// TestListEntryAnthropicLive pins the anthropic-family headers end to end.
func TestListEntryAnthropicLive(t *testing.T) {
	secutils.SetSSRFWhitelistFromRaw("127.0.0.1")
	t.Cleanup(secutils.ResetSSRFWhitelistForTest)
	var gotKey, gotVersion string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotKey = r.Header.Get("x-api-key")
		gotVersion = r.Header.Get("anthropic-version")
		_, _ = w.Write([]byte(`{"data":[{"id":"claude-x","display_name":"Claude"}]}`))
	}))
	defer srv.Close()

	models, err := invoke.List(t.Context(), "anthropic", &invoke.ListOptions{
		BaseURL:     srv.URL,
		Credentials: invoke.Credentials{APIKey: "sk-ant"},
	})
	if err != nil {
		t.Fatalf("invoke.List = %v", err)
	}
	if gotKey != "sk-ant" || gotVersion != anthropicVersion {
		t.Errorf("headers x-api-key=%q version=%q", gotKey, gotVersion)
	}
	if len(models) != 1 || models[0].DisplayName != "Claude" {
		t.Errorf("models = %+v", models)
	}
}

// TestListEntryUnsupported pins lock #3 for providers whose adapters do not
// serve the listing facet (jina): explicit error, never a doomed request.
func TestListEntryUnsupported(t *testing.T) {
	_, err := invoke.List(t.Context(), "jina", &invoke.ListOptions{
		BaseURL:     "https://api.jina.ai",
		Credentials: invoke.Credentials{APIKey: "k"},
	})
	if err == nil {
		t.Fatal("jina listing must fail with an explicit unsupported error")
	}
}
