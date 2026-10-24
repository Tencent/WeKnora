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
			// Excel writes a semicolon where the comma is the decimal separator.
			// Read with commas, the file was one column, cut at every decimal comma.
			name: "semicolon separated with decimal commas",
			in:   "Produkt;Preis;Menge\nApfel;3,50;2\nBirne;2;10\n",
			want: "| Produkt | Preis | Menge |\n| --- | --- | --- |\n| Apfel | 3,50 | 2 |\n| Birne | 2 | 10 |\n",
		},
		{
			// Read with commas, every data row splits in two while the header
			// stays whole, so the comma cannot be the separator.
			name: "semicolon separated, a decimal comma on every row",
			in:   "Produkt;Preis\nApfel;3,50\nBirne;2,10\n",
			want: "| Produkt | Preis |\n| --- | --- |\n| Apfel | 3,50 |\n| Birne | 2,10 |\n",
		},
		{
			name: "tab separated",
			in:   "name\tqty\npen\t2\n",
			want: "| name | qty |\n| --- | --- |\n| pen | 2 |\n",
		},
		{
			name: "pipe separated",
			in:   "name|qty\npen|2\n",
			want: "| name | qty |\n| --- | --- |\n| pen | 2 |\n",
		},
		{
			name: "semicolon inside a comma file stays in its cell",
			in:   "name,note\npen,\"a;b\"\nink,c\n",
			want: "| name | note |\n| --- | --- |\n| pen | a;b |\n| ink | c |\n",
		},
		{
			// Both separators give two fields on every line; the comma wins.
			name: "comma wins a tie",
			in:   "a,b;c\nd,e;f\n",
			want: "| a | b;c |\n| --- | --- |\n| d | e;f |\n",
		},
		{
			name: "single column",
			in:   "name\npen\n",
			want: "| name |\n| --- |\n| pen |\n",
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
