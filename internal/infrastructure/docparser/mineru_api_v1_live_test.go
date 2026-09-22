package docparser

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/utils"
)

// hostnameOf extracts host:port → host (no scheme, no port) for whitelist entries.
func hostnameOf(raw string) string {
	s := strings.TrimPrefix(strings.TrimPrefix(raw, "https://"), "http://")
	if i := strings.IndexByte(s, '/'); i >= 0 {
		s = s[:i]
	}
	if i := strings.LastIndexByte(s, ':'); i >= 0 && !strings.Contains(s, "]") {
		s = s[:i]
	}
	return s
}

// TestLiveMinerUV1Server validates the V1 client against a real
// mineru-kit api-server. Skipped unless MINERU_V1_LIVE is set.
//	MINERU_V1_LIVE=1 MINERU_V1_ENDPOINT=http://192.168.5.2:8310 \
//	MINERU_V1_PDF=<path> go test ./internal/infrastructure/docparser/ -run TestLiveMinerUV1 -v -timeout 20m
func TestLiveMinerUV1Server(t *testing.T) {
	endpoint := os.Getenv("MINERU_V1_ENDPOINT")
	pdfPath := os.Getenv("MINERU_V1_PDF")
	if os.Getenv("MINERU_V1_LIVE") == "" || endpoint == "" || pdfPath == "" {
		t.Skip("MINERU_V1_LIVE / MINERU_V1_ENDPOINT / MINERU_V1_PDF not set")
	}
	// The endpoint is typically an internal address (e.g. the host gateway);
	// allow it the same way a deployment would via SSRF_WHITELIST.
	utils.SetSSRFWhitelistFromRaw(hostnameOf(endpoint))
	if !detectMinerUAPI(context.Background(), endpoint, "") {
		t.Fatalf("endpoint %s does not speak V1 API", endpoint)
	}
	content, err := os.ReadFile(pdfPath)
	if err != nil {
		t.Fatalf("read pdf: %v", err)
	}
	reader := NewMinerUReader(map[string]string{"mineru_endpoint": endpoint})
	res, err := reader.Read(context.Background(), &types.ReadRequest{
		FileName: "live.pdf", FileType: "pdf", FileContent: content,
	})
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	t.Logf("markdown chars=%d images=%d", len(res.MarkdownContent), len(res.ImageRefs))
	if len(res.MarkdownContent) == 0 {
		t.Fatal("empty markdown")
	}
}
