package docparser

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/utils"
)

func TestPaddleOCRVLReadPageLocators(t *testing.T) {
	t.Setenv("SSRF_WHITELIST", "127.0.0.1")
	utils.SetSSRFWhitelistFromRaw("127.0.0.1")
	t.Cleanup(func() { utils.SetSSRFWhitelistFromRaw("") })

	const title = "Repeated title"
	const left = "第一页正文"
	const right = "第三页正文"
	const tail = "第四页正文"
	const table = "<table><tr><th>Name</th></tr><tr><td>Value</td></tr></table>"
	for _, tt := range []struct {
		name   string
		first  string
		last   string
		fourth string
		want   []string
	}{
		{
			name:  "both titles match",
			first: title + "\n\n" + left,
			last:  title + "\n\n" + right,
			want:  []string{"1:box:Repeated title", "1:box:第一页正文", "3:box:Repeated title", "3:box:第三页正文"},
		},
		{
			name:  "title missing from first page",
			first: left,
			last:  title + "\n\n" + right,
			want:  []string{"1:box:第一页正文", "3:box:Repeated title", "3:box:第三页正文"},
		},
		{
			name:  "HTML table at the end of the first page",
			first: left + "\n\n" + table,
			last:  title + "\n\n" + right,
			want: []string{
				"1:box:第一页正文", "1:page:" + strings.TrimSpace(NormalizeHTMLTables(table)),
				"3:box:Repeated title", "3:box:第三页正文",
			},
		},
		{
			name:   "cross-page table leaves subsequent page locators intact",
			first:  left + "\n\n<table><tr><th>Name</th></tr>",
			last:   "<tr><td>Value</td></tr></table>\n\n" + right,
			fourth: title + "\n\n" + tail,
			want:   []string{"1:box:第一页正文", "3:box:第三页正文", "4:box:Repeated title", "4:box:第四页正文"},
		},
		{
			name:  "ambiguous title retains its page without a box",
			first: left,
			last:  title + "\n\n" + title + "\n\n" + right,
			want:  []string{"1:box:第一页正文", "3:page:" + title + "\n\n" + title, "3:box:第三页正文"},
		},
		{
			name:  "unmatched page text and unmatched text between boxes",
			first: "未匹配到的页面",
			last:  title + "\n\n未匹配到的段落\n\n" + right,
			want:  []string{"1:page:未匹配到的页面", "3:box:Repeated title", "3:page:未匹配到的段落", "3:box:第三页正文"},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			pages := []paddleOCRVLPage{
				paddleOCRLocatorTestPage(t, tt.first, title, left),
				{},
				paddleOCRLocatorTestPage(t, tt.last, title, right),
			}
			wantMarkdown := tt.first + "\n\n" + tt.last
			if tt.fourth != "" {
				pages = append(pages, paddleOCRLocatorTestPage(t, tt.fourth, title, tail))
				wantMarkdown += "\n\n" + tt.fourth
			}
			result := map[string]any{"result": map[string]any{"layoutParsingResults": pages}}

			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/layout-parsing", "/result":
					_ = json.NewEncoder(w).Encode(result)
				case "/jobs":
					_, _ = fmt.Fprint(w, "{\"data\":{\"jobId\":\"fixture\"}}")
				case "/jobs/fixture":
					_, _ = fmt.Fprintf(w, "{\"data\":{\"state\":\"done\",\"resultUrl\":{\"jsonUrl\":%q}}}",
						"http://"+r.Host+"/result")
				default:
					http.NotFound(w, r)
				}
			}))
			defer server.Close()

			for _, reader := range []struct {
				name string
				read func(context.Context, *types.ReadRequest) (*types.ReadResult, error)
			}{
				{"self-hosted", NewPaddleOCRVLReader(map[string]string{"paddleocr_vl_endpoint": server.URL}).Read},
				{"cloud", NewPaddleOCRVLCloudReader(map[string]string{
					"paddleocr_vl_cloud_base_url": server.URL + "/jobs",
					"paddleocr_vl_cloud_token":    "test",
				}).Read},
			} {
				t.Run(reader.name, func(t *testing.T) {
					got, err := reader.read(t.Context(), &types.ReadRequest{
						FileName: "test.pdf", FileType: "pdf", FileContent: []byte("test"),
					})
					if err != nil {
						t.Fatal(err)
					}
					if want := NormalizeHTMLTables(wantMarkdown); got.MarkdownContent != want {
						t.Fatalf("markdown = %q, want %q", got.MarkdownContent, want)
					}
					runes := []rune(got.MarkdownContent)
					var matches []string
					for _, block := range got.SourceBlocks {
						if block.Start < 0 || block.End > len(runes) || block.Start >= block.End {
							t.Fatalf("invalid source block offsets: %+v", block)
						}
						locator := block.Locator
						if locator.Type != types.SourceLocatorPDF {
							t.Fatalf("expected PDF locator, got %+v", locator)
						}
						precision := "page"
						switch len(locator.BBox) {
						case 0:
						case 4:
							precision = "box"
						default:
							t.Fatalf("invalid bounding box: %+v", locator)
						}
						text := strings.TrimSpace(string(runes[block.Start:block.End]))
						matches = append(matches, fmt.Sprintf("%d:%s:%s", locator.Page, precision, text))
					}
					if !reflect.DeepEqual(matches, tt.want) {
						t.Errorf("source blocks = %q, want %q", matches, tt.want)
					}
				})
			}
		})
	}
}

func paddleOCRLocatorTestPage(t *testing.T, markdown string, texts ...string) paddleOCRVLPage {
	t.Helper()
	blocks := make([]map[string]any, 0, len(texts))
	for i, text := range texts {
		top := float64(10 * i)
		blocks = append(blocks, map[string]any{
			"block_content": text, "block_bbox": []float64{0, top, 80, top + 10},
		})
	}
	pruned, err := json.Marshal(map[string]any{
		"width": 100, "height": 100, "parsing_res_list": blocks,
	})
	if err != nil {
		t.Fatal(err)
	}
	page := paddleOCRVLPage{PrunedResult: pruned}
	page.Markdown.Text = markdown
	return page
}
