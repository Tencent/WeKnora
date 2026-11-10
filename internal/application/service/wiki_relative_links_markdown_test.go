package service

import "testing"

func TestNormalizeWikiRelativeLinks(t *testing.T) {
	for _, tt := range []struct{ name, input, want string }{
		{"inline", "[飞书](../lark.md) [query gateway](rdm/query-gateway.md)", "飞书 query gateway"},
		{"formatted label", "[**bold** and `code`](./a.md#part)", "**bold** and `code`"},
		{"nested brackets", "[label [nested]](../a.md)", "label [nested]"},
		{"empty label", "[](a.md)", ""},
		{"title and parentheses", "[label](<../a(b).MD?x=1> \"title\")", "label"},
		{"escaped punctuation", "[la\\]bel](a\\(b\\).md)", "la\\]bel"},
		{"encoded path", "[label](a%20b%2Emd)", "label"},
		{
			"external",
			"[a](https://example.com/a.md) [b](//example.com/a.md) [c](/docs/a.md) [d](#a.md)",
			"[a](https://example.com/a.md) [b](//example.com/a.md) [c](/docs/a.md) [d](#a.md)",
		},
		{"image", "![label](a.md) [![icon](a.png)](doc.md)", "![label](a.md) ![icon](a.png)"},
		{
			"code", "`[a](a.md)`\n\n```md\n[b](b.md)\n```\n\n    [c](c.md)\n",
			"`[a](a.md)`\n\n```md\n[b](b.md)\n```\n\n    [c](c.md)\n",
		},
		{"escaped opener", "\\[label](a.md)", "\\[label](a.md)"},
		{"wiki link", "[[concept/a|A]] [page](concept/a)", "[[concept/a|A]] [page](concept/a)"},
		{"reference", "[label][doc]\n\n[doc]: ../a.md \"title\"\n", "label\n\n\n"},
		{"shortcut", "[doc] [doc][]\n\n[doc]: ../a.md\n", "doc doc\n\n\n"},
		{
			"shared image definition", "[label][doc] ![image][doc]\n\n[doc]: ../a.md\n",
			"label ![image][doc]\n\n[doc]: ../a.md\n",
		},
		{"multiline", "[two\nlines](../a.md)\n", "two\nlines\n"},
		{"invalid", "[label](unclosed.md", "[label](unclosed.md"},
		{"html", "<a href=\"a.md\">label</a>", "<a href=\"a.md\">label</a>"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got := normalizeWikiRelativeLinks(tt.input)
			if got != tt.want {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
			if twice := normalizeWikiRelativeLinks(got); twice != got {
				t.Fatalf("not idempotent: %q", twice)
			}
		})
	}
}
