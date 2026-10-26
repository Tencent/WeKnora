package im

import (
	"io"
	"testing"
)

func TestInMemoryFileHeaderFilenameRoundTrip(t *testing.T) {
	for _, name := range []string{"report.md", "季度报告.md", "Q4 \"Final\".md", "Q4; Final.md"} {
		t.Run(name, func(t *testing.T) {
			fh := newInMemoryFileHeader(name, []byte("body"))
			if fh.Filename != name {
				t.Fatalf("Filename = %q, want %q", fh.Filename, name)
			}
			f, err := fh.Open()
			if err != nil {
				t.Fatalf("Open: %v", err)
			}
			defer func() {
				if err := f.Close(); err != nil {
					t.Errorf("Close: %v", err)
				}
			}()
			got, err := io.ReadAll(f)
			if err != nil {
				t.Fatalf("ReadAll: %v", err)
			}
			if string(got) != "body" {
				t.Fatalf("content = %q, want body", got)
			}
		})
	}
}
