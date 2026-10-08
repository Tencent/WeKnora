package docparser

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Tencent/WeKnora/internal/utils"
)

func TestCloudResultDownloadCancellation(t *testing.T) {
	utils.SetSSRFWhitelistFromRaw("127.0.0.1")
	t.Cleanup(utils.ResetSSRFWhitelistForTest)
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		t.Error("cancelled download reached server")
	}))
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, _, err := downloadAndExtractZip(ctx, server.URL)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("download ignored cancellation: %v", err)
	}
}
