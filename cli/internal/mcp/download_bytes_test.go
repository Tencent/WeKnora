package mcp

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"testing"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestTool_DocDownload_PreservesRawBytes(t *testing.T) {
	cases := []struct {
		name   string
		data   []byte
		base64 bool
	}{
		{name: "ASCII text", data: []byte("plain text\n")},
		{name: "Unicode text", data: []byte("中文 café 🐄\n")},
		{name: "empty file", data: []byte{}},
		{name: "early NUL", data: []byte{'a', 0, 'b'}, base64: true},
		{name: "invalid first byte", data: []byte{0xff, 'a', 'b'}, base64: true},
		{name: "invalid middle byte", data: []byte{'a', 0xff, 'b'}, base64: true},
		{name: "truncated UTF-8", data: []byte{'a', 0xe2, 0x82}, base64: true},
		{name: "invalid after probe", data: append(bytes.Repeat([]byte{'a'}, 512), 0xff), base64: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc := &fakeSvc{
				openDocName: "fixture.bin",
				openDocBody: io.NopCloser(bytes.NewReader(tc.data)),
			}
			c, _ := newTestServer(t, svc)
			var structured docDownloadOutput
			result := callTool(t, c, "doc_download", map[string]any{"doc_id": "doc-1"}, &structured)
			if result.StructuredContent == nil {
				t.Fatal("missing structured content")
			}
			if len(result.Content) != 1 {
				t.Fatalf("content blocks=%d, want 1", len(result.Content))
			}
			text, ok := result.Content[0].(*mcpsdk.TextContent)
			if !ok {
				t.Fatalf("content type=%T, want text", result.Content[0])
			}
			var fallback docDownloadOutput
			if err := json.Unmarshal([]byte(text.Text), &fallback); err != nil {
				t.Fatal(err)
			}
			if structured != fallback {
				t.Errorf("structured and text content disagree: %+v vs %+v", structured, fallback)
			}
			for name, output := range map[string]docDownloadOutput{"structured": structured, "text": fallback} {
				if output.DocID != "doc-1" || output.FileName != "fixture.bin" || output.Bytes != len(tc.data) {
					t.Errorf("%s metadata=%+v, want fixture identity and bytes=%d", name, output, len(tc.data))
				}
				if output.IsBase64 != tc.base64 {
					t.Errorf("%s is_base64=%v, want %v", name, output.IsBase64, tc.base64)
				}
				decoded := []byte(output.Content)
				if output.IsBase64 {
					var err error
					decoded, err = base64.StdEncoding.DecodeString(output.Content)
					if err != nil {
						t.Fatalf("%s base64: %v", name, err)
					}
				}
				if !bytes.Equal(decoded, tc.data) {
					t.Errorf("%s byte round-trip=%x, want %x", name, decoded, tc.data)
				}
			}
		})
	}
}
