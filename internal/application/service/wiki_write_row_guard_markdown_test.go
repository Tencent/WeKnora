package service

import (
	"reflect"
	"testing"
)

func TestWikiWriteTableRowIdentitiesMarkdownTables(t *testing.T) {
	cases := []struct {
		name    string
		content string
		want    []string
	}{
		{"optional outer pipes", "Name | Value\n--- | ---\nAlice | 1\nBob | 2\n", []string{"alice", "bob"}},
		{"mixed outer pipes", "| Name | Value\n--- | --- |\nAlice | 1 |\n| Bob | 2\n", []string{"alice", "bob"}},
		{"alignment and CRLF", "Name | Value\r\n:--- | ---:\r\nAlice | 1\r\n", []string{"alice"}},
		{"single column", "| Name |\n| --- |\n| Alice |\n", []string{"alice"}},
		{"empty header", "||\n|-|\n|Alice|\n", []string{"alice"}},
		{"empty trailing header", "|Name||\n|-|-|\n|Alice|1|\n", []string{"alice"}},
		{"empty headers with mismatched delimiter", "|||\n|-|-|-|\n|Alice|1|2|\n", nil},
		{"empty first cell", "Name | Value\n--- | ---\n | CERT-1\n", []string{"cert-1"}},
		{"escaped pipe in identity", "Name | Value\n--- | ---\nAlice\\|Bob | 1\n", []string{"alice\\|bob"}},
		{"header only", "Name | Value\n--- | ---\n", nil},
		{"pipe prose", "a | b\nc | d\n", nil},
		{"mismatched delimiter", "a | b\n--- | --- | ---\nc | d\n", nil},
		{"fenced example", "```md\n| Name | Value |\n| --- | --- |\n| Alice | 1 |\n```\n", nil},
		{"indented example", "    Name | Value\n    --- | ---\n    Alice | 1\n", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := wikiWriteTableRowIdentities(tc.content)
			if len(tc.want) == 0 && len(got) == 0 {
				return
			}
			if !reflect.DeepEqual(tc.want, got) {
				t.Fatalf("row identities: got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestWikiWriteMissingRowIdentitiesWithOptionalPipes(t *testing.T) {
	full := "| Name | Value |\n| --- | --- |\n| Alice | 1 |\n| Bob | 2 |\n"
	optional := "Name | Value\n--- | ---\nAlice | 1\nBob | 2\n"
	for _, pair := range [][2]string{{full, optional}, {optional, full}} {
		if got := wikiWriteMissingRowIdentities(pair[0], pair[1]); len(got) != 0 {
			t.Fatalf("equivalent formatting lost identities: %q", got)
		}
	}
	for _, rewritten := range []string{"Name | Value\n--- | ---\nAlice | 1\n", "No table remains."} {
		want := []string{"bob"}
		if rewritten == "No table remains." {
			want = []string{"alice", "bob"}
		}
		if got := wikiWriteMissingRowIdentities(optional, rewritten); !reflect.DeepEqual(want, got) {
			t.Fatalf("lost identities: got %q, want %q", got, want)
		}
	}
}
