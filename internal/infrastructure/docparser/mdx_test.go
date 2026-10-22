package docparser

import (
	"context"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

func TestDowngradeMDX(t *testing.T) {
	src := `---
title: Getting started
---

import Tabs from '@theme/Tabs';
export const version = '2.0';

# Installation

Run <Code language="bash">npm install</Code> to install.

<Tabs>
  <TabItem value="apple" label="Apple">Use Homebrew.</TabItem>
</Tabs>

<Component withProps={{ nested: true }} />

1. Pick ` + "`a < b`" + ` or ` + "`x > y`" + ` in prose.

` + "```" + `mdx
import KeptInsideFence from 'somewhere';
<div>this stays verbatim</div>
` + "```" + `

## Next steps

export default function Page() { return null; }
`
	out := DowngradeMDX(src)

	for _, want := range []string{
		"# Installation",
		"npm install",   // inner text of <Code>
		"Use Homebrew.", // inner text of <TabItem>
		"1. Pick `a < b` or `x > y` in prose.",
		"import KeptInsideFence from 'somewhere';", // fence untouched
		"<div>this stays verbatim</div>",           // fence untouched
		"## Next steps",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
	for _, banned := range []string{
		"import Tabs from",
		"export const version",
		"<Tabs>", "</Tabs>", "<TabItem", "</TabItem>",
		"<Code", "</Code>",
		"<Component",
		"export default function",
	} {
		if strings.Contains(out, banned) {
			t.Errorf("output still contains %q:\n%s", banned, out)
		}
	}
}

func TestSimpleFormatReaderMDX(t *testing.T) {
	reader := &SimpleFormatReader{}
	res, err := reader.Read(context.Background(), &types.ReadRequest{
		FileName:    "guide.mdx",
		FileType:    "mdx",
		FileContent: []byte("import X from 'x';\n\n# Title\n\n<Kbd>Ctrl</Kbd> saves.\n"),
	})
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	for _, want := range []string{"# Title", "Ctrl saves."} {
		if !strings.Contains(res.MarkdownContent, want) {
			t.Errorf("markdown missing %q: %s", want, res.MarkdownContent)
		}
	}
	if strings.Contains(res.MarkdownContent, "import X") || strings.Contains(res.MarkdownContent, "<Kbd>") {
		t.Errorf("mdx syntax leaked: %s", res.MarkdownContent)
	}
}

func TestIsSimpleFormatMDX(t *testing.T) {
	if !IsSimpleFormat("mdx") || !IsSimpleFormat(".MDX") {
		t.Fatal("mdx should be a simple format")
	}
}
