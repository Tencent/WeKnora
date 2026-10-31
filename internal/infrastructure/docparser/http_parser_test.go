package docparser

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	secutils "github.com/Tencent/WeKnora/internal/utils"
)

// A read result includes the whole document and its inline images, so it is not
// a control-plane JSON reply. Applying the 16 MiB control-plane cap here rejects
// a legitimate large document.
func TestHTTPDocumentReaderAcceptsResultAboveControlPlaneCap(t *testing.T) {
	secutils.SetSSRFWhitelistFromRaw("127.0.0.1")
	t.Cleanup(func() { secutils.SetSSRFWhitelistFromRaw("") })

	const markdownSize = maxJSONResponseBytes + (1 << 20)
	payload, err := json.Marshal(httpReadResponse{
		MarkdownContent: strings.Repeat("a", markdownSize),
	})
	if err != nil {
		t.Fatalf("marshal response: %v", err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if _, err := w.Write(payload); err != nil {
			t.Errorf("write response: %v", err)
		}
	}))
	t.Cleanup(server.Close)

	reader, err := NewHTTPDocumentReader(server.URL)
	if err != nil {
		t.Fatalf("NewHTTPDocumentReader() = %v", err)
	}
	result, err := reader.Read(context.Background(), &types.ReadRequest{
		FileName: "large.md",
		FileType: "md",
	})
	if err != nil {
		t.Fatalf("Read() = %v, want a %d byte result to be accepted", err, markdownSize)
	}
	if len(result.MarkdownContent) != markdownSize {
		t.Errorf("markdown length = %d, want %d", len(result.MarkdownContent), markdownSize)
	}
}
