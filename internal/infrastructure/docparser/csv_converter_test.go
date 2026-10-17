package docparser

import "testing"

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
