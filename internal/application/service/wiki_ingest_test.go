package service

import (
	"context"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
)

func TestSlugify(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"Hello World", "hello-world"},
		{"Acme Corp", "acme-corp"},
		{"  spaces  ", "spaces"},
		{"under_score", "under-score"},
		{"Already-Good", "already-good"},
		{"Special!@#Chars", "specialchars"},
		{"CamelCase", "camelcase"},
		{"", ""},
		{"a/b/c", "a/b/c"},               // preserve slashes for hierarchical slugs
		{"中文标题", "中文标题"},                 // preserve CJK
		{"Mix 中英文 Test", "mix-中英文-test"}, // mixed
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := slugify(tt.input)
			if got != tt.want {
				t.Errorf("slugify(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestTruncateString(t *testing.T) {
	tests := []struct {
		input  string
		maxLen int
		want   string
	}{
		{"hello world", 20, "hello world"},
		{"hello world", 5, "hello..."},
		{"", 10, ""},
		{"abc", 3, "abc"},
		{"abcd", 3, "abc..."},
		{"中文测试", 2, "中文..."},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := truncateString(tt.input, tt.maxLen)
			if got != tt.want {
				t.Errorf("truncateString(%q, %d) = %q, want %q", tt.input, tt.maxLen, got, tt.want)
			}
		})
	}
}

func TestAppendUnique(t *testing.T) {
	arr := types.StringArray{"a", "b"}

	// Add new
	result := appendUnique(arr, "c")
	if len(result) != 3 {
		t.Errorf("Expected 3 items, got %d", len(result))
	}

	// Add duplicate
	result = appendUnique(result, "b")
	if len(result) != 3 {
		t.Errorf("Expected 3 items (no dup), got %d", len(result))
	}
}

func TestReconstructContent(t *testing.T) {
	chunks := []*types.Chunk{
		{ChunkIndex: 2, ChunkType: types.ChunkTypeText, Content: "Third paragraph."},
		{ChunkIndex: 0, ChunkType: types.ChunkTypeText, Content: "First paragraph."},
		{ChunkIndex: 1, ChunkType: types.ChunkTypeText, Content: "Second paragraph."},
		{ChunkIndex: 3, ChunkType: types.ChunkTypeImageOCR, Content: "OCR text should be excluded."},
	}

	content := reconstructContent(chunks)

	// Should be sorted by ChunkIndex and exclude non-text chunks
	if content == "" {
		t.Fatal("reconstructContent should not be empty")
	}

	// Verify order: first, second, third
	if len(content) == 0 {
		t.Fatal("empty content")
	}

	// The first characters should be "First"
	if content[:5] != "First" {
		t.Errorf("Expected content to start with 'First', got: %s", content[:20])
	}
}

func TestReconstructContentEmpty(t *testing.T) {
	content := reconstructContent(nil)
	if content != "" {
		t.Errorf("Empty chunks should produce empty content, got %q", content)
	}
}

func TestStripImageMarkup(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"plain text untouched", "Hello world.", "Hello world."},
		{"single markdown image removed", "![alt](images/page_1.png)", ""},
		{
			"scanned-pdf style page references all stripped",
			"![MX5280_page_1.png](images/MX5280_page_1.png)\n\n![MX5280_page_2.png](images/MX5280_page_2.png)",
			"\n\n",
		},
		{"mixed text and image keeps text", "Intro paragraph.\n![fig](a.png)\nConclusion.", "Intro paragraph.\n\nConclusion."},
		{"html img tag stripped", `Before <img src="x.png" alt="y"/> after`, "Before  after"},
		{
			// Regression guard: an earlier version stripped the WHOLE
			// <image>...</image> block (including <image_ocr> content),
			// silently destroying successful VLM OCR results. The fix must
			// preserve the inner OCR / caption text.
			"enriched <image> block keeps inner OCR + caption text",
			`<image url="images/page_1.png">
<image_original>![p1](images/page_1.png)</image_original>
<image_caption>scanned letter on letterhead</image_caption>
<image_ocr>SEHR GEEHRTER HERR MUSTERMANN, ...</image_ocr>
</image>`,
			"\n\nscanned letter on letterhead\nSEHR GEEHRTER HERR MUSTERMANN, ...\n",
		},
		{
			"empty <image> block (OCR failed) reduces to whitespace",
			`<image url="x"><image_original>![a](x)</image_original></image>`,
			"",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := stripImageMarkup(tt.input)
			if got != tt.want {
				t.Errorf("stripImageMarkup(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestHasSufficientTextContent(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  bool
	}{
		{"empty string", "", false},
		{"only whitespace", "   \n\n\t  ", false},
		{
			"only image references (scanned PDF without OCR)",
			"![MX5280_page_1.png](images/MX5280_page_1.png)\n![MX5280_page_2.png](images/MX5280_page_2.png)",
			false,
		},
		{"too-short text below 10-rune threshold", "hi", false},
		{
			"short legitimate note above threshold",
			"Meeting at 3pm tomorrow.",
			true,
		},
		{
			"image-only with successful VLM OCR (the fix)",
			`<image url="images/p1.png">
<image_original>![p1](images/p1.png)</image_original>
<image_caption>scanned letter</image_caption>
<image_ocr>Sehr geehrter Herr Mustermann, in der Sache 4711/2024 ...</image_ocr>
</image>`,
			true,
		},
		{
			"image-only with failed VLM OCR (still rejected)",
			`<image url="images/p1.png">
<image_original>![p1](images/p1.png)</image_original>
</image>`,
			false,
		},
		{
			"sufficient text mixed with images still passes",
			"![cover](cover.png)\nDie Beklagte hat die Klage anerkannt.\n![sig](sig.png)",
			true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := hasSufficientTextContent(tt.input)
			if got != tt.want {
				t.Errorf("hasSufficientTextContent(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestMaskImageURLs(t *testing.T) {
	const urlA = "minio://kb/10000/exports/4135-aaaa-bbbb-cccc/page_1.jpg"
	const urlB = "local://kb/10000/exports/9999-dddd-eeee-ffff/page_2.png"

	t.Run("single markdown image round trips exact URL", func(t *testing.T) {
		input := "Intro ![alt text](" + urlA + ") outro"
		masked, urlMap := maskImageURLs(input)

		if masked != "Intro ![alt text](wkimg:0001) outro" {
			t.Fatalf("masked = %q", masked)
		}
		if got := urlMap["wkimg:0001"]; got != urlA {
			t.Fatalf("urlMap[wkimg:0001] = %q, want %q", got, urlA)
		}
		if got := unmaskImageURLs(masked, urlMap); got != input {
			t.Fatalf("unmaskImageURLs() = %q, want %q", got, input)
		}
	})

	t.Run("enriched image attribute and original markdown share token", func(t *testing.T) {
		input := `<image url="` + urlA + `">
<image_original>![page](` + urlA + `)</image_original>
<image_caption>caption text</image_caption>
</image>`
		masked, urlMap := maskImageURLs(input)

		if len(urlMap) != 1 {
			t.Fatalf("len(urlMap) = %d, want 1", len(urlMap))
		}
		if strings.Count(masked, "wkimg:0001") != 2 {
			t.Fatalf("masked should contain the same placeholder twice: %q", masked)
		}
		if strings.Contains(masked, urlA) {
			t.Fatalf("masked still contains real URL: %q", masked)
		}
		if got := unmaskImageURLs(masked, urlMap); got != input {
			t.Fatalf("unmaskImageURLs() = %q, want %q", got, input)
		}
	})

	t.Run("same URL repeated and distinct URLs map correctly", func(t *testing.T) {
		input := "![a](" + urlA + ")\n![again](" + urlA + ")\n![b](" + urlB + ")"
		masked, urlMap := maskImageURLs(input)

		if strings.Count(masked, "wkimg:0001") != 2 {
			t.Fatalf("same URL should reuse wkimg:0001: %q", masked)
		}
		if strings.Count(masked, "wkimg:0002") != 1 {
			t.Fatalf("second URL should use wkimg:0002: %q", masked)
		}
		if got := unmaskImageURLs(masked, urlMap); got != input {
			t.Fatalf("unmaskImageURLs() = %q, want %q", got, input)
		}
	})

	t.Run("caption is preserved even when it resembles a placeholder", func(t *testing.T) {
		input := "![wkimg:0001](" + urlA + ")"
		masked, urlMap := maskImageURLs(input)
		got := unmaskImageURLs(masked, urlMap)

		if got != input {
			t.Fatalf("unmaskImageURLs() = %q, want %q", got, input)
		}
	})

	t.Run("non image text is unchanged", func(t *testing.T) {
		input := "slug: entity/example\nlanguage: zh"
		masked, urlMap := maskImageURLs(input)

		if masked != input {
			t.Fatalf("maskImageURLs() = %q, want %q", masked, input)
		}
		if len(urlMap) != 0 {
			t.Fatalf("len(urlMap) = %d, want 0", len(urlMap))
		}
	})
}

func TestUnmaskImageURLsDropsUnknownPlaceholders(t *testing.T) {
	urlMap := map[string]string{"wkimg:0001": "minio://kb/exports/real.jpg"}
	input := `{"details":"keep ![ok](wkimg:0001) drop ![bad](wkimg:001) and wkimg:9999"}`
	got := unmaskImageURLs(input, urlMap)

	if !strings.Contains(got, "![ok](minio://kb/exports/real.jpg)") {
		t.Fatalf("known placeholder was not restored: %q", got)
	}
	if strings.Contains(got, "wkimg:") || strings.Contains(got, "![bad]") {
		t.Fatalf("unknown placeholders should be dropped: %q", got)
	}
}

func TestGenerateWithTemplateMasksImageURLsBeforeLLM(t *testing.T) {
	const realURL = "minio://kb/10000/exports/4135-aaaa-bbbb-cccc/page_1.jpg"
	model := &templateCaptureChatModel{
		response: `{"details":"Model kept ![caption](wkimg:0001)"}`,
	}
	service := &wikiIngestService{}

	got, err := service.generateWithTemplate(
		context.Background(),
		model,
		`Content={{.Content}} Existing={{.ExistingContent}}`,
		map[string]string{
			"Content":         "new ![alt](" + realURL + ")",
			"ExistingContent": "old ![same](" + realURL + ")",
		},
	)
	if err != nil {
		t.Fatalf("generateWithTemplate() error = %v", err)
	}
	if strings.Contains(model.prompt, realURL) {
		t.Fatalf("LLM prompt contains real URL: %q", model.prompt)
	}
	if strings.Count(model.prompt, "wkimg:0001") != 2 {
		t.Fatalf("same URL across fields should share wkimg:0001: %q", model.prompt)
	}
	if strings.Contains(got, "wkimg:") {
		t.Fatalf("returned content still contains placeholder: %q", got)
	}
	if !strings.Contains(got, realURL) {
		t.Fatalf("returned content does not contain restored real URL: %q", got)
	}
}

type templateCaptureChatModel struct {
	prompt   string
	response string
}

func (m *templateCaptureChatModel) Chat(
	_ context.Context,
	messages []chat.Message,
	_ *chat.ChatOptions,
) (*types.ChatResponse, error) {
	if len(messages) > 0 {
		m.prompt = messages[0].Content
	}
	return &types.ChatResponse{Content: m.response}, nil
}

func (m *templateCaptureChatModel) ChatStream(
	context.Context,
	[]chat.Message,
	*chat.ChatOptions,
) (<-chan types.StreamResponse, error) {
	return nil, nil
}

func (m *templateCaptureChatModel) GetModelName() string { return "capture" }
func (m *templateCaptureChatModel) GetModelID() string   { return "capture" }
