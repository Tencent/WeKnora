package docparser

import (
	"context"
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/utils"
)

func TestNewMinerUReaderResolvesParseMethod(t *testing.T) {
	tests := []struct {
		name      string
		overrides map[string]string
		want      string
	}{
		{name: "default is auto", overrides: map[string]string{}, want: "auto"},
		{name: "legacy enabled becomes auto", overrides: map[string]string{"mineru_enable_ocr": "true"}, want: "auto"},
		{name: "legacy disabled becomes text", overrides: map[string]string{"mineru_enable_ocr": "false"}, want: "txt"},
		{name: "explicit method wins", overrides: map[string]string{"mineru_parse_method": "ocr", "mineru_enable_ocr": "false"}, want: "ocr"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reader := NewMinerUReader(tt.overrides)
			if reader.parseMethod != tt.want {
				t.Fatalf("parseMethod = %q, want %q", reader.parseMethod, tt.want)
			}
		})
	}
}

func TestMinerUReaderPreservesMultipartFilename(t *testing.T) {
	utils.SetSSRFWhitelistFromRaw("127.0.0.1")
	t.Cleanup(func() {
		utils.SetSSRFWhitelistFromRaw("")
	})

	const fileContent = "%PDF-1.7 synthetic test content"
	tests := []struct {
		name     string
		fileName string
		fileType string
		want     string
	}{
		{name: "original basename", fileName: "reports/Illustrator 研报.pdf", fileType: "pdf", want: "Illustrator 研报.pdf"},
		{
			name:     "strip multipart controls",
			fileName: "reports/\r\nIllustrator\x00 export.pdf",
			fileType: "pdf",
			want:     "Illustrator export.pdf",
		},
		{name: "fallback from bare type", fileType: "pdf", want: "document.pdf"},
		{name: "fallback from dotted uppercase type", fileType: ".PDF", want: "document.pdf"},
		{name: "fallback from path-only name", fileName: "/", fileType: "pdf", want: "document.pdf"},
		{name: "legacy fallback without metadata", want: "document"},
		{name: "reject unsafe fallback type", fileType: "../pdf", want: "document"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			type capturedRequest struct {
				method   string
				path     string
				fileName string
				content  string
				err      error
			}

			captured := make(chan capturedRequest, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				got := capturedRequest{method: r.Method, path: r.URL.Path}
				file, header, err := r.FormFile("files")
				if err != nil {
					got.err = err
				} else {
					got.fileName = header.Filename
					content, readErr := io.ReadAll(file)
					got.content = string(content)
					got.err = readErr
					_ = file.Close()
				}
				captured <- got
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, `{"results":{"document":{"md_content":"ok","images":{}}}}`)
			}))
			defer server.Close()

			reader := NewMinerUReader(map[string]string{"mineru_endpoint": server.URL})
			result, err := reader.Read(context.Background(), &types.ReadRequest{
				FileContent: []byte(fileContent),
				FileName:    tt.fileName,
				FileType:    tt.fileType,
			})
			if err != nil {
				t.Fatalf("Read returned error: %v", err)
			}
			if result.MarkdownContent != "ok" {
				t.Fatalf("MarkdownContent = %q, want %q", result.MarkdownContent, "ok")
			}

			request := <-captured
			if request.err != nil {
				t.Fatalf("capture multipart request: %v", request.err)
			}
			if request.method != http.MethodPost {
				t.Errorf("method = %q, want %q", request.method, http.MethodPost)
			}
			if request.path != "/file_parse" {
				t.Errorf("path = %q, want %q", request.path, "/file_parse")
			}
			if request.fileName != tt.want {
				t.Errorf("multipart filename = %q, want %q", request.fileName, tt.want)
			}
			if request.content != fileContent {
				t.Errorf("multipart content = %q, want %q", request.content, fileContent)
			}
		})
	}
}

func TestNormalizeMinerUMarkdownPreservesMarkdownAndHTML(t *testing.T) {
	input := strings.Join([]string{
		"# Heading",
		"",
		"![](images/cover.jpg)",
		"",
		`<details><summary>text_image</summary>caption</details>`,
		"",
		`<table><tr><td><img src="images/profile.jpg"/></td></tr></table>`,
	}, "\n")

	got := normalizeMinerUMarkdown(input)

	if !strings.Contains(got, "# Heading") {
		t.Fatalf("expected heading to stay intact, got: %q", got)
	}
	if strings.Contains(got, `\# Heading`) {
		t.Fatalf("expected heading to avoid escaped form, got: %q", got)
	}
	if !strings.Contains(got, "![](images/cover.jpg)") {
		t.Fatalf("expected markdown image syntax to stay intact, got: %q", got)
	}
	if strings.Contains(got, `!\[](images/cover.jpg)`) {
		t.Fatalf("expected markdown image syntax to avoid escaped form, got: %q", got)
	}
	if !strings.Contains(got, `<details><summary>text_image</summary>caption</details>`) {
		t.Fatalf("expected details/summary block to be preserved, got: %q", got)
	}
	if !strings.Contains(got, `<img src="images/profile.jpg"/>`) {
		t.Fatalf("expected html img tag to be preserved, got: %q", got)
	}
}

func TestProcessImagesKeepsReferencedVariants(t *testing.T) {
	reader := &MinerUReader{}
	mdContent := strings.Join([]string{
		"![](images/cover.jpg)",
		`<img src="./images/profile.jpg"/>`,
		`![](plain.jpg)`,
	}, "\n")

	png := createTestPNG(200, 150)
	b64 := base64.StdEncoding.EncodeToString(png)
	images := map[string]string{
		"cover.jpg":   "data:image/png;base64," + b64,
		"profile.jpg": "data:image/png;base64," + b64,
		"plain.jpg":   "data:image/png;base64," + b64,
	}

	refs, gotMarkdown := reader.processImages(mdContent, images)

	if gotMarkdown != mdContent {
		t.Fatalf("processImages should not rewrite markdown content")
	}
	if len(refs) != 3 {
		t.Fatalf("expected 3 image refs, got %d", len(refs))
	}
}

// TestProcessImagesMatchesPathsWithSpaces guards against a regression where
// MinerU image filenames containing spaces (common on Chinese documents,
// e.g. "images/第 1 页.jpg") would be silently dropped because the markdown
// regex used to extract refs disallowed whitespace inside the URL group.
func TestProcessImagesMatchesPathsWithSpaces(t *testing.T) {
	reader := &MinerUReader{}
	mdContent := "![](images/第 1 页.jpg)"

	png := createTestPNG(200, 150)
	b64 := base64.StdEncoding.EncodeToString(png)
	images := map[string]string{
		"第 1 页.jpg": "data:image/png;base64," + b64,
	}

	refs, _ := reader.processImages(mdContent, images)
	if len(refs) != 1 {
		t.Fatalf("expected 1 image ref for path with spaces, got %d", len(refs))
	}
	if refs[0].OriginalRef != "images/第 1 页.jpg" {
		t.Fatalf("unexpected OriginalRef: %q", refs[0].OriginalRef)
	}
}
