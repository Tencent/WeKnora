package rerank

import (
	stderrors "errors"
	"net/http"
	"net/http/httptest"
	"testing"

	secutils "github.com/Tencent/WeKnora/internal/utils"
)

func withRerankSSRFWhitelist(t *testing.T, raw string) {
	t.Helper()
	t.Setenv("SSRF_WHITELIST", raw)
	secutils.ResetSSRFWhitelistForTest()
	t.Cleanup(secutils.ResetSSRFWhitelistForTest)
}

func TestOpenAIRerankerRejectsInternalBaseURL(t *testing.T) {
	withRerankSSRFWhitelist(t, "")

	_, err := NewOpenAIReranker(&RerankerConfig{
		BaseURL:   "http://169.254.169.254/latest/meta-data/",
		ModelName: "rerank-test",
	})
	if err == nil {
		t.Fatalf("NewOpenAIReranker returned nil error for blocked internal BaseURL")
	}
}

func TestOpenAIRerankerBlocksRedirectToInternalURL(t *testing.T) {
	withRerankSSRFWhitelist(t, "127.0.0.1")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://169.254.169.254/latest/meta-data/", http.StatusFound)
	}))
	defer server.Close()

	reranker, err := NewOpenAIReranker(&RerankerConfig{
		BaseURL:   server.URL,
		ModelName: "rerank-test",
		APIKey:    "sk-test",
	})
	if err != nil {
		t.Fatalf("NewOpenAIReranker: %v", err)
	}

	_, err = reranker.Rerank(t.Context(), "query", []string{"doc"})
	if !stderrors.Is(err, secutils.ErrSSRFRedirectBlocked) {
		t.Fatalf("Rerank error = %v, want ErrSSRFRedirectBlocked", err)
	}
}
