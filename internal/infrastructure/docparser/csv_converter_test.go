package docparser

import (
	"encoding/csv"
	"strings"
	"testing"
)

func TestCSVToMarkdown(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "rectangular",
			in:   "name,qty\npen,2\n",
			want: "| name | qty |\n| --- | --- |\n| pen | 2 |\n",
		},
		{
			// A short row failed the whole file with "wrong number of fields".
			name: "short row is padded",
			in:   "name,qty,note\npen,2\nink,1,blue\n",
			want: "| name | qty | note |\n| --- | --- | --- |\n| pen | 2 |  |\n| ink | 1 | blue |\n",
		},
		{
			name: "long row widens the table",
			in:   "name,qty\npen,2,spare\n",
			want: "| name | qty |  |\n| --- | --- | --- |\n| pen | 2 | spare |\n",
		},
		{
			name: "pipe in a cell is escaped",
			in:   "expr,means\n\"a|b\",a or b\n",
			want: "| expr | means |\n| --- | --- |\n| a\\|b | a or b |\n",
		},
		{
			name: "backslash before pipe preserves both literal characters",
			in:   "expr,note\na\\|b,literal\n",
			want: "| expr | note |\n| --- | --- |\n| " + `a\\\|b` + " | literal |\n",
		},
		{
			name: "multiple and trailing backslashes stay inside the cell",
			in:   "expr,note\na\\\\|b\\,literal\n",
			want: "| expr | note |\n| --- | --- |\n| " + `a\\\\\|b\\` + " | literal |\n",
		},
		{
			name: "multi-line field stays in its row",
			in:   "name,address\nAnn,\"1 Main St\r\nSpringfield\"\n",
			want: "| name | address |\n| --- | --- |\n| Ann | 1 Main St<br />Springfield |\n",
		},
		{
			name: "byte order mark is not part of the first header",
			in:   "\xef\xbb\xbfname,qty\npen,2\n",
			want: "| name | qty |\n| --- | --- |\n| pen | 2 |\n",
		},
		{
			name: "empty file",
			in:   "",
			want: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := csvToMarkdown([]byte(tc.in))
			if err != nil {
				t.Fatalf("csvToMarkdown(%q) error: %v", tc.in, err)
			}
			if got != tc.want {
				t.Errorf("csvToMarkdown(%q)\n got: %q\nwant: %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestCSVToMarkdownRejectsExcessivePadding(t *testing.T) {
	// 20,000 one-column rows plus one 20,000-column row: 80 KB of input
	// whose table would be padded into over a gigabyte of mostly-empty
	// cells. The budget must reject it before any of that is rendered.
	var in strings.Builder
	in.WriteString(strings.Repeat("x\n", 20000))
	in.WriteString(strings.Repeat(",", 19999))
	in.WriteString("x\n")

	md, err := csvToMarkdown([]byte(in.String()))
	if err == nil {
		t.Fatalf("csvToMarkdown rendered %d bytes, want a size-limit error", len(md))
	}
	if md != "" {
		t.Errorf("csvToMarkdown on rejection returned %d bytes of output, want none", len(md))
	}
	if !strings.Contains(err.Error(), "would render") || !strings.Contains(err.Error(), "limit") {
		t.Errorf("csvToMarkdown error %q does not name the rendered size and the limit", err)
	}
}

func TestCSVMarkdownSizeMatchesRender(t *testing.T) {
	// Cells chosen to exercise every csvCellReplacer escape: a pipe, a
	// backslash before it, a quoted newline, and a lone carriage return.
	in := "a|b,\"x\\|y\nz\",w\r\n1,p|q,\"lone\rcr\"\n"

	records, width := parseCSVForBudget(t, in)
	contentBytes := 0
	for _, record := range records {
		for _, cell := range record {
			contentBytes += len(csvCellReplacer.Replace(cell))
		}
	}
	projected, err := csvMarkdownSize(len(records), width, contentBytes, maxCSVMarkdownBytes)
	if err != nil {
		t.Fatalf("csvMarkdownSize: %v", err)
	}

	md, err := csvToMarkdown([]byte(in))
	if err != nil {
		t.Fatalf("csvToMarkdown(%q) error: %v", in, err)
	}
	if len(md) != projected {
		t.Errorf("csvMarkdownSize = %d, rendered table is %d bytes", projected, len(md))
	}
}

// parseCSVForBudget re-parses input with the same reader settings
// csvToMarkdown uses, so the projection can be checked against a real
// render.
func parseCSVForBudget(t *testing.T, in string) ([][]string, int) {
	t.Helper()
	reader := csv.NewReader(strings.NewReader(in))
	reader.LazyQuotes = true
	reader.TrimLeadingSpace = true
	reader.FieldsPerRecord = -1
	records, err := reader.ReadAll()
	if err != nil {
		t.Fatalf("parseCSVForBudget(%q) error: %v", in, err)
	}
	width := 0
	for _, record := range records {
		width = max(width, len(record))
	}
	return records, width
}
