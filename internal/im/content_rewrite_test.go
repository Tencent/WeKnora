package im

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestStripIMCitationTags(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"no tags", "hello world", "hello world"},
		{"kb tag", `text <kb id="1" title="doc"/> more`, "text  more"},
		{"web tag", `see <web url="http://x.com"/> here`, "see  here"},
		{"multiple", `<kb id="1"/><web url="x"/>text`, "text"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, stripIMCitationTags(tt.in))
		})
	}
}

func TestStripImageXMLTags(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			"with image_original",
			`<image url="local://1/img.png">
<image_original>![alt](local://1/img.png)</image_original>
<image_caption>a cat</image_caption>
</image>`,
			"![alt](local://1/img.png)",
		},
		{
			"without image_original",
			`<image url="local://1/img.png">
<image_caption>a cat</image_caption>
</image>`,
			"",
		},
		{
			"no image tags",
			"just plain text ![img](http://example.com/img.png)",
			"just plain text ![img](http://example.com/img.png)",
		},
		{
			"mixed content",
			`before <image url="x"><image_original>![a](b)</image_original></image> after`,
			"before ![a](b) after",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, stripImageXMLTags(tt.in))
		})
	}
}

func TestFindIncompleteStorageURL(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want int // expected return; -1 means no match expected
	}{
		{
			"complete URL terminated by )",
			"![img](local://1/abc/img.png)",
			// The URL `local://1/abc/img.png` ends with `)` which is a terminator,
			// but the regex [^\s)\]>"]* matches up to `)` — the `)` is NOT included.
			// So the URL portion is `local://1/abc/img.png` and `)` terminates it.
			// The match does NOT reach end of string → should return -1.
			-1,
		},
		{
			"complete URL terminated by space",
			"text local://1/abc/img.png more text",
			-1,
		},
		{
			"truncated URL at end",
			"text ![img](local://1/abc/im",
			12, // starts at `l` in `local://`
		},
		{
			"just scheme at end",
			"text minio://",
			5,
		},
		{
			"no storage URL",
			"just plain text http://example.com",
			-1,
		},
		{
			"URL at very end",
			"local://1/img.png",
			0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := findIncompleteStorageURL(tt.in)
			assert.Equal(t, tt.want, got, "findIncompleteStorageURL(%q)", tt.in)
		})
	}
}

func TestFindIncompleteXMLTag(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want int
	}{
		{
			"complete tag",
			`text <kb id="1"/> more`,
			-1,
		},
		{
			"incomplete kb tag",
			`text <kb id="1`,
			5,
		},
		{
			"incomplete image tag",
			`text <image url="local://1/img`,
			5,
		},
		{
			"incomplete image_original",
			`<image_original>![alt](local://1/`,
			-1, // the tag itself is complete (has >), the content is truncated
		},
		{
			"incomplete opening image_original",
			`text <image_original`,
			5,
		},
		{
			"no XML",
			"just plain text",
			-1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := findIncompleteXMLTag(tt.in)
			assert.Equal(t, tt.want, got, "findIncompleteXMLTag(%q)", tt.in)
		})
	}
}

func TestHoldbackCutoff(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want int // expected cutoff (len(in) means no holdback)
	}{
		{
			"no holdback needed",
			"plain text with complete ![img](local://1/img.png) content",
			// URL is terminated by `)` then space → no holdback
			-1, // placeholder, computed below
		},
		{
			"truncated URL",
			"text ![img](local://1/abc/im",
			12,
		},
		{
			"truncated XML",
			"text <kb id=",
			5,
		},
		{
			"both truncated, URL earlier",
			"<image url=\"local://1/im",
			0, // <image at 0 is earlier than local:// at 12
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := holdbackCutoff(tt.in)
			if tt.want == -1 {
				assert.Equal(t, len(tt.in), got, "holdbackCutoff(%q) should be len(in)", tt.in)
			} else {
				assert.Equal(t, tt.want, got, "holdbackCutoff(%q)", tt.in)
			}
		})
	}
}
