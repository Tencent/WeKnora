package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	secutils "github.com/Tencent/WeKnora/internal/utils"
)

func TestDownloadFileFromURLBlocksRedirectToLoopback(t *testing.T) {
	internal := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("INTERNAL_SECRET=metadata-token-AKIAEXAMPLE"))
	}))
	defer internal.Close()

	if err := secutils.ValidateURLForSSRF(internal.URL); err == nil {
		t.Fatalf("precondition: direct internal URL should be blocked")
	}

	attacker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, internal.URL, http.StatusFound)
	}))
	defer attacker.Close()

	attackerURL := attacker.URL + "/malicious.pdf"
	fileName := ""
	fileType := "pdf"
	_, err := downloadFileFromURL(context.Background(), attackerURL, &fileName, &fileType)
	if err == nil {
		t.Fatal("expected redirect to loopback to be blocked")
	}
	if !strings.Contains(err.Error(), "redirect blocked") && !strings.Contains(err.Error(), "connection blocked") {
		t.Fatalf("unexpected error: %v", err)
	}
}
