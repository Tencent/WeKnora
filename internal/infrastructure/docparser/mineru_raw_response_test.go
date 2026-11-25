package docparser

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
)

func TestRawResponsePreviewKeepsUTF8AtTheCap(t *testing.T) {
	raw := "{}" + strings.Repeat("服务暂时不可用", 700)
	if len(raw) <= rawResponseLogBytes {
		t.Fatalf("test body is %d bytes, want more than %d", len(raw), rawResponseLogBytes)
	}

	got := rawResponsePreview(raw)

	if !utf8.ValidString(got) {
		t.Errorf("preview is not valid UTF-8: %q", got)
	}
	if !strings.HasSuffix(got, "...") {
		t.Errorf("preview does not end with the truncation marker: %q", got)
	}
	if len(got) > rawResponseLogBytes+len("...") {
		t.Errorf("preview is %d bytes, want at most %d", len(got), rawResponseLogBytes+len("..."))
	}
	if !strings.HasPrefix(raw, strings.TrimSuffix(got, "...")) {
		t.Error("preview is not a prefix of the original response")
	}
}

func TestRawResponsePreviewLeavesShortResponsesUntouched(t *testing.T) {
	raw := `{"markdown":"服务正常"}`

	if got := rawResponsePreview(raw); got != raw {
		t.Errorf("preview = %q, want it unchanged", got)
	}
}

// The legacy /file_parse path dumps the raw JSON before parsing it. MinerU
// responses carry CJK titles and OCR text, so a byte cut at 4000 used to leave
// invalid UTF-8 in the log line.
func TestMinerULegacyRawResponseLogStaysValidUTF8(t *testing.T) {
	allowLoopbackSSRF(t)
	body := "{}" + strings.Repeat("服务暂时不可用", 700)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/health" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, body)
	}))
	defer server.Close()

	var logs bytes.Buffer
	logger.SetOutput(&logs)
	t.Cleanup(func() { logger.SetOutput(os.Stdout) })

	req := &types.ReadRequest{FileContent: []byte("%PDF"), FileName: "a.pdf", FileType: "pdf"}
	_, _ = NewMinerUReader(map[string]string{"mineru_endpoint": server.URL}).Read(context.Background(), req)

	line := ""
	for _, candidate := range strings.Split(logs.String(), "\n") {
		if strings.Contains(candidate, "[MinerU] Raw response (truncated") {
			line = candidate
			break
		}
	}
	if line == "" {
		t.Fatal("raw response log line was not emitted")
	}
	if !utf8.ValidString(line) {
		t.Errorf("raw response log line is not valid UTF-8: %q", line)
	}
	if !strings.Contains(line, "服务暂时不可用") {
		t.Error("raw response log line lost the document body")
	}
}
