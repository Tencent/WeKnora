package dingtalk

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// The shapes pinned in this file come from read-only probes of real documents:
// a code block carries {"syntax","text","title"} with an optional title, an
// attachment carries file metadata but no download URL, and a block the API
// could not map carries only {"rawType"}. All fixtures below are synthetic.

func TestRenderCodeBlockUsesSyntaxAsLanguage(t *testing.T) {
	result := renderDocument("测试文档", []json.RawMessage{
		rawJSON(`{"blockType":"code","code":{"syntax":"bash","text":"echo hello"}}`),
	})

	want := "# 测试文档\n\n```bash\necho hello\n```\n"
	if result.Markdown != want {
		t.Fatalf("Markdown = %q, want %q", result.Markdown, want)
	}
	if len(result.UnknownTypes) != 0 {
		t.Fatalf("UnknownTypes = %#v, want none", result.UnknownTypes)
	}
}

func TestRenderCodeBlockWithoutSyntaxOrTitle(t *testing.T) {
	result := renderDocument("", []json.RawMessage{
		rawJSON(`{"blockType":"code","code":{"text":"plain code"}}`),
	})

	want := "```\nplain code\n```\n"
	if result.Markdown != want {
		t.Fatalf("Markdown = %q, want %q", result.Markdown, want)
	}
	if len(result.UnknownTypes) != 0 {
		t.Fatalf("UnknownTypes = %#v, want none", result.UnknownTypes)
	}
}

func TestRenderCodeBlockWritesTitleAboveTheFence(t *testing.T) {
	result := renderDocument("", []json.RawMessage{
		rawJSON(`{"blockType":"code","code":{"syntax":"bash","title":"Example *script*","text":"ls"}}`),
	})

	want := "**Example \\*script\\***\n\n```bash\nls\n```\n"
	if result.Markdown != want {
		t.Fatalf("Markdown = %q, want %q", result.Markdown, want)
	}
	if len(result.UnknownTypes) != 0 {
		t.Fatalf("UnknownTypes = %#v, want none", result.UnknownTypes)
	}
}

// Code that itself contains a fence must not terminate the block early, so the
// fence grows past the longest backtick run in the text. The payload is written
// as an interpreted string because a raw one cannot hold the backticks.
func TestRenderCodeBlockLengthensFenceAroundEmbeddedFence(t *testing.T) {
	result := renderDocument("", []json.RawMessage{
		rawJSON("{\"blockType\":\"code\",\"code\":{\"syntax\":\"markdown\"," +
			"\"text\":\"before\\n```\\ninner\\n```\\nafter\"}}"),
	})

	want := "````markdown\nbefore\n```\ninner\n```\nafter\n````\n"
	if result.Markdown != want {
		t.Fatalf("Markdown = %q, want %q", result.Markdown, want)
	}
	if len(result.UnknownTypes) != 0 {
		t.Fatalf("UnknownTypes = %#v, want none", result.UnknownTypes)
	}
}

func TestCodeFenceLengthOutgrowsEmbeddedBacktickRuns(t *testing.T) {
	for _, test := range []struct {
		name string
		text string
		want int
	}{
		{name: "no backtick", text: "plain", want: 3},
		{name: "single backtick", text: "a `b` c", want: 3},
		{name: "two backticks", text: "a ``b`` c", want: 3},
		{name: "fence inside", text: "a ```b``` c", want: 4},
		{name: "long run", text: "a `````b````` c", want: 6},
		{name: "fence only", text: "```", want: 4},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := codeFenceLength(test.text); got != test.want {
				t.Fatalf("codeFenceLength(%q) = %d, want %d", test.text, got, test.want)
			}
		})
	}
}

// A code block without text has nothing to fence. An empty fence would only be
// noise, so the block stays reported as an unmodelled payload; children outside
// the documented shape are rendered rather than dropped.
func TestRenderCodeBlockWithoutTextIsReportedNotRendered(t *testing.T) {
	result := renderDocument("", []json.RawMessage{
		rawJSON(`{"blockType":"code","code":{"syntax":"bash","title":"Example"},` +
			`"children":[{"blockType":"paragraph","paragraph":{"text":"kept body"}}]}`),
	})

	if strings.Contains(result.Markdown, "```") || strings.Contains(result.Markdown, "**Example**") {
		t.Fatalf("empty code block rendered a fragment:\n%s", result.Markdown)
	}
	if !strings.Contains(result.Markdown, "kept body") {
		t.Fatalf("child content lost:\n%s", result.Markdown)
	}
	if want := []string{"code"}; !reflect.DeepEqual(result.UnknownTypes, want) {
		t.Fatalf("UnknownTypes = %#v, want %#v", result.UnknownTypes, want)
	}
}

// The attachment payload has no download URL, so the renderer only names the
// file. Sizes are byte counts with binary units and one decimal place; a size
// the API did not report is omitted instead of rendered as "0.0 B".
func TestRenderAttachmentFormatsNameAndSize(t *testing.T) {
	for _, test := range []struct {
		name    string
		payload string
		want    string
	}{
		{
			name:    "half a kilobyte stays in bytes",
			payload: `{"name":"example.pdf","size":512}`,
			want:    "Attachment: example.pdf (512.0 B)\n",
		},
		{
			name:    "just below a kilobyte",
			payload: `{"name":"example.pdf","size":1023}`,
			want:    "Attachment: example.pdf (1023.0 B)\n",
		},
		{
			name:    "kilobyte boundary",
			payload: `{"name":"example.pdf","size":1024}`,
			want:    "Attachment: example.pdf (1.0 KB)\n",
		},
		{
			name:    "kilobytes",
			payload: `{"name":"example.pdf","size":1229}`,
			want:    "Attachment: example.pdf (1.2 KB)\n",
		},
		{
			name:    "megabytes",
			payload: `{"name":"example.pdf","size":1258291}`,
			want:    "Attachment: example.pdf (1.2 MB)\n",
		},
		{
			name:    "gigabytes",
			payload: `{"name":"example.pdf","size":2147483648}`,
			want:    "Attachment: example.pdf (2.0 GB)\n",
		},
		{
			name:    "size omitted",
			payload: `{"name":"example.pdf"}`,
			want:    "Attachment: example.pdf\n",
		},
		{
			name:    "zero size omitted",
			payload: `{"name":"example.pdf","size":0}`,
			want:    "Attachment: example.pdf\n",
		},
		{
			name:    "negative size omitted",
			payload: `{"name":"example.pdf","size":-1}`,
			want:    "Attachment: example.pdf\n",
		},
		{
			name:    "without resource id",
			payload: `{"name":"example.pdf","size":1024,"type":"pdf","viewType":"preview"}`,
			want:    "Attachment: example.pdf (1.0 KB)\n",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			result := renderDocument("", []json.RawMessage{
				rawJSON(`{"blockType":"attachment","attachment":` + test.payload + `}`),
			})
			if result.Markdown != test.want {
				t.Fatalf("Markdown = %q, want %q", result.Markdown, test.want)
			}
			if len(result.UnknownTypes) != 0 {
				t.Fatalf("UnknownTypes = %#v, want none", result.UnknownTypes)
			}
		})
	}
}

// The name is document content, not a link target, and the opaque resourceId is
// never turned into a download URL because the payload has no resolvable one.
func TestRenderAttachmentEscapesNameAndBuildsNoLink(t *testing.T) {
	result := renderDocument("", []json.RawMessage{
		rawJSON(`{"blockType":"attachment","attachment":{"name":"example_[draft]*.pdf","size":1024,` +
			`"resourceId":"res-synthetic-1"}}`),
	})

	want := "Attachment: example\\_\\[draft\\]\\*.pdf (1.0 KB)\n"
	if result.Markdown != want {
		t.Fatalf("Markdown = %q, want %q", result.Markdown, want)
	}
	for _, forbidden := range []string{"](", "http", "res-synthetic-1"} {
		if strings.Contains(result.Markdown, forbidden) {
			t.Fatalf("attachment rendered %q:\n%s", forbidden, result.Markdown)
		}
	}
	if len(result.UnknownTypes) != 0 {
		t.Fatalf("UnknownTypes = %#v, want none", result.UnknownTypes)
	}
}

func TestRenderAttachmentWithoutNameIsReportedNotRendered(t *testing.T) {
	result := renderDocument("", []json.RawMessage{
		rawJSON(`{"blockType":"attachment","attachment":{"size":1024,"resourceId":"res-synthetic-1"},` +
			`"children":[{"blockType":"paragraph","paragraph":{"text":"kept body"}}]}`),
	})

	if strings.Contains(result.Markdown, "Attachment:") {
		t.Fatalf("nameless attachment rendered a line:\n%s", result.Markdown)
	}
	if !strings.Contains(result.Markdown, "kept body") {
		t.Fatalf("child content lost:\n%s", result.Markdown)
	}
	if want := []string{"attachment"}; !reflect.DeepEqual(result.UnknownTypes, want) {
		t.Fatalf("UnknownTypes = %#v, want %#v", result.UnknownTypes, want)
	}
}

// A block the API could not map carries no body text, only the real type name.
// Keeping that name in the marker is what makes the document metadata and the
// sync warning actionable, and children outside the documented shape are still
// rendered instead of dropped.
func TestRenderUnknownBlockKeepsRawTypeVisible(t *testing.T) {
	result := renderDocument("", []json.RawMessage{
		rawJSON(`{"blockType":"unknown","unknown":{"rawType":"card"},` +
			`"children":[{"blockType":"paragraph","paragraph":{"text":"card body"}}]}`),
		rawJSON(`{"blockType":"unknown","unknown":{}}`),
		rawJSON(`{"blockType":"unknown","unknown":{"rawType":"  "}}`),
	})

	if !strings.Contains(result.Markdown, "card body") {
		t.Fatalf("child content lost:\n%s", result.Markdown)
	}
	if want := []string{"unknown", "unknown:card"}; !reflect.DeepEqual(result.UnknownTypes, want) {
		t.Fatalf("UnknownTypes = %#v, want %#v", result.UnknownTypes, want)
	}
}
