package catalog

import (
	"net/http"
	"net/http/httptest"
	"testing"

	secutils "github.com/Tencent/WeKnora/internal/utils"
)

// TestOpenAIListURL pins the version-segment handling: bases that already
// carry a version path append /models; bare bases get /v1/models.
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

// TestListRemoteModelsSSRF verifies the probe refuses internal targets —
// the endpoint must not become an internal-network scanning channel.
func TestListRemoteModelsSSRF(t *testing.T) {
	for _, target := range []string{
		"http://127.0.0.1:8080",
		"http://169.254.169.254/latest/meta-data",
		"http://localhost:11434",
	} {
		if _, err := ListRemoteModels(t.Context(), "openai", target, "k"); err == nil {
			t.Errorf("ListRemoteModels(%q) must fail SSRF validation", target)
		}
	}
}

// TestListRemoteModelsLive exercises the OpenAI-compatible lister against a
// stub server. The loopback stub is reachable because the test allowlists it
// — the same mechanism operators use for private vLLM deployments, so the
// probe inherits the deployment's SSRF policy rather than bypassing it.
func TestListRemoteModelsLive(t *testing.T) {
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

	models, err := ListRemoteModels(t.Context(), "openai", srv.URL, "sekret")
	if err != nil {
		t.Fatalf("ListRemoteModels = %v", err)
	}
	if gotAuth != "Bearer sekret" {
		t.Errorf("Authorization = %q, want Bearer sekret", gotAuth)
	}
	if len(models) != 2 {
		t.Errorf("models = %d, want 2 (empty ids dropped)", len(models))
	}
	if models[0].ID != "m-a" || models[0].OwnedBy != "org" {
		t.Errorf("models[0] = %+v", models[0])
	}
}
