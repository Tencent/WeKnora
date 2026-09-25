package searchutil

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

func imageInfoJSON(t *testing.T, infos ...types.ImageInfo) string {
	t.Helper()
	data, err := json.Marshal(infos)
	if err != nil {
		t.Fatalf("marshal image info: %v", err)
	}
	return string(data)
}

func TestImageURLsInContent_CollectsHTMLSrc(t *testing.T) {
	cases := []struct {
		name    string
		content string
		want    string
	}{
		{"markdown", "![a](local://images/a.png)", "local://images/a.png"},
		{"html double quotes", `<img src="local://images/b.png">`, "local://images/b.png"},
		{"html single quotes", `<img src='local://images/c.png'>`, "local://images/c.png"},
		{"html with attributes", `<img class="x" src="local://images/d.png" alt="y">`, "local://images/d.png"},
		{"html uppercase", `<IMG SRC="local://images/e.png">`, "local://images/e.png"},
		{"html padded", `<img src="  local://images/f.png  ">`, "local://images/f.png"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			urls := ImageURLsInContent(tc.content)
			if !urls[tc.want] {
				t.Errorf("expected %q to be collected from %q, got %v", tc.want, tc.content, urls)
			}
		})
	}
}

func TestFilterImageInfoByContentURLs_KeepsHTMLReferencedEntry(t *testing.T) {
	info := types.ImageInfo{
		URL:     "local://images/shot.png",
		Caption: "the login screen",
		OCRText: "Sign in with SSO",
	}

	// A passage whose images are HTML tags used to look image-free, which made
	// the filter drop every entry it was given.
	got := FilterImageInfoByContentURLs(
		`<p><img src="local://images/shot.png" alt="login"></p>`,
		imageInfoJSON(t, info),
	)
	if got == "" {
		t.Fatal("expected the entry to be kept for an HTML-only passage")
	}

	var kept []types.ImageInfo
	if err := json.Unmarshal([]byte(got), &kept); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(kept) != 1 || kept[0].URL != info.URL {
		t.Errorf("unexpected filter result: %+v", kept)
	}
}

func TestFilterImageInfoByContentURLs_StillDropsUnreferenced(t *testing.T) {
	got := FilterImageInfoByContentURLs(
		`<p><img src="local://images/present.png"></p>`,
		imageInfoJSON(t, types.ImageInfo{URL: "local://images/absent.png", OCRText: "elsewhere"}),
	)
	if got != "" {
		t.Errorf("expected an unreferenced entry to be dropped, got %s", got)
	}
}

func TestEnrichContentWithImageInfoForChat_HTMLTag(t *testing.T) {
	info := types.ImageInfo{
		URL:     "local://images/shot.png",
		Caption: "the login screen",
		OCRText: "Sign in with SSO",
	}
	content := `<p><img src="local://images/shot.png" alt="login"></p>`

	got := EnrichContentWithImageInfoForChat(content, imageInfoJSON(t, info))

	if !strings.Contains(got, "Sign in with SSO") {
		t.Errorf("OCR text was not injected for an HTML image: %s", got)
	}
	if !strings.Contains(got, "the login screen") {
		t.Errorf("caption was not injected for an HTML image: %s", got)
	}
	if !strings.Contains(got, `<img src="local://images/shot.png"`) {
		t.Errorf("the original tag should be preserved: %s", got)
	}
}

func TestEnrichContentWithImageInfoForChat_MarkdownUnchanged(t *testing.T) {
	info := types.ImageInfo{
		URL:     "local://images/shot.png",
		OCRText: "Sign in with SSO",
	}
	content := "![login](local://images/shot.png)"

	got := EnrichContentWithImageInfoForChat(content, imageInfoJSON(t, info))

	if !strings.Contains(got, "![login](local://images/shot.png)") {
		t.Errorf("markdown image should be preserved: %s", got)
	}
	if !strings.Contains(got, "Sign in with SSO") {
		t.Errorf("markdown enrichment regressed: %s", got)
	}
}

func TestFilterImageInfoByMatchRange_HTMLTagInsideWindow(t *testing.T) {
	info := types.ImageInfo{URL: "local://images/shot.png", OCRText: "Sign in with SSO"}
	parent := `intro text
<img src="local://images/shot.png">
tail text`

	// Window covering the tag: the entry survives.
	got := FilterImageInfoByMatchRange(parent, 0, 0, len([]rune(parent)), imageInfoJSON(t, info))
	if got == "" {
		t.Error("expected the entry to survive a window containing the whole tag")
	}

	// Window that cuts the tag in half: nothing matches, which is the known
	// limitation — an HTML tag is several times longer than ![](url) and
	// straddles a child window far more often.
	if got := FilterImageInfoByMatchRange(parent, 0, 0, 20, imageInfoJSON(t, info)); got != "" {
		t.Errorf("expected a truncated tag not to match, got %s", got)
	}
}

func TestEnrichContentWithImageInfoForChat_DoesNotRescanInjectedMetadata(t *testing.T) {
	// The OCR text of a screenshot that documents an <img> tag contains one.
	// Scanning the injected metadata would splice a second block into the middle
	// of the first.
	info := types.ImageInfo{
		URL:     "local://images/x.png",
		OCRText: `use <img src="local://images/x.png"> to embed`,
	}
	content := "![a](local://images/x.png)"

	got := EnrichContentWithImageInfoForChat(content, imageInfoJSON(t, info))

	if n := strings.Count(got, "**Image text (OCR):**"); n != 1 {
		t.Errorf("expected exactly 1 metadata block, got %d:\n%s", n, got)
	}
	if !strings.Contains(got, "to embed") {
		t.Errorf("OCR sentence was truncated:\n%s", got)
	}
}

func TestEnrichContentWithImageInfoForChat_BothSyntaxesOneBlockEach(t *testing.T) {
	info := types.ImageInfo{URL: "local://images/x.png", Caption: "shot"}
	content := "![a](local://images/x.png)\n\n<img src=\"local://images/x.png\">"

	got := EnrichContentWithImageInfoForChat(content, imageInfoJSON(t, info))

	if n := strings.Count(got, "**Image caption:** shot"); n != 2 {
		t.Errorf("expected one block per reference (2), got %d:\n%s", n, got)
	}
}

func TestHTMLImageSrcRegex_HyphenatedAttributeIsNotSrc(t *testing.T) {
	urls := ImageURLsInContent(`<img data-src="local://images/placeholder.png" src="local://images/real.png">`)
	if urls["local://images/placeholder.png"] {
		t.Error("data-src must not be collected as src")
	}
	if !urls["local://images/real.png"] {
		t.Errorf("src was not collected: %v", urls)
	}
}

func TestEnrichContentWithImageInfoForChat_UnmatchedImageStaysBare(t *testing.T) {
	// The chat variant deliberately enriches only images that appear in the
	// passage, so an entry for some other page must not be appended.
	info := types.ImageInfo{URL: "local://images/other.png", OCRText: "another page"}
	content := `<p><img src="local://images/shot.png"></p>`

	got := EnrichContentWithImageInfoForChat(content, imageInfoJSON(t, info))

	if got != content {
		t.Errorf("expected content untouched, got %s", got)
	}
	if strings.Contains(got, "another page") {
		t.Errorf("an orphan entry leaked into the passage: %s", got)
	}
}
