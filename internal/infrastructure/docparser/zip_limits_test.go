package docparser

import (
	"archive/zip"
	"bytes"
	"errors"
	"strings"
	"testing"
)

// buildZip returns an in-memory archive holding the given name -> content pairs.
func buildZip(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, content := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
		if _, err := w.Write([]byte(content)); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}
	return buf.Bytes()
}

// openEntry returns the first archive entry matching name.
func openEntry(t *testing.T, zipData []byte, name string) *zip.File {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(zipData), int64(len(zipData)))
	if err != nil {
		t.Fatalf("open zip: %v", err)
	}
	for _, f := range zr.File {
		if f.Name == name {
			return f
		}
	}
	t.Fatalf("entry %q not found", name)
	return nil
}

func TestReadResultPackage(t *testing.T) {
	t.Run("rejects over the cap", func(t *testing.T) {
		const limit = 1 << 10
		_, err := readResultPackage(strings.NewReader(strings.Repeat("a", limit+1)), limit)
		if !errors.Is(err, errResultPackageTooLarge) {
			t.Fatalf("err = %v, want errResultPackageTooLarge", err)
		}
	})

	t.Run("accepts exactly the cap", func(t *testing.T) {
		const limit = 1 << 10
		got, err := readResultPackage(strings.NewReader(strings.Repeat("a", limit)), limit)
		if err != nil {
			t.Fatalf("readResultPackage() = %v, want nil at exactly the cap", err)
		}
		if len(got) != limit {
			t.Errorf("len = %d, want %d", len(got), limit)
		}
	})

	t.Run("non-positive limit falls back to the production cap", func(t *testing.T) {
		if _, err := readResultPackage(strings.NewReader("zip"), 0); err != nil {
			t.Fatalf("readResultPackage(_, 0) = %v, want nil", err)
		}
	})
}

// The declared UncompressedSize64 comes from the archive and is allowed to
// understate the expansion, so the read has to be capped as well as the header
// checked. A header that understates it is the actual bomb.
func TestReadZipEntryCappedRejectsBombs(t *testing.T) {
	t.Run("declares over the cap", func(t *testing.T) {
		data := buildZip(t, map[string]string{"big.txt": strings.Repeat("a", 4096)})
		entry := openEntry(t, data, "big.txt")

		_, err := readZipEntryCapped(entry, 1024)
		if !errors.Is(err, errResultPackageTooLarge) {
			t.Fatalf("err = %v, want errResultPackageTooLarge", err)
		}
		if !strings.Contains(err.Error(), "declares") {
			t.Errorf("error = %q, want it to name the declared size", err)
		}
	})

	t.Run("expands past the cap even with an honest header", func(t *testing.T) {
		// 64 KiB of zeroes deflates to well under 1 KiB, so the declared size is
		// truthful and passes the header check; only the capped read catches it.
		data := buildZip(t, map[string]string{"bomb.txt": strings.Repeat("\x00", 64<<10)})
		entry := openEntry(t, data, "bomb.txt")
		if entry.UncompressedSize64 > 1024 {
			t.Skipf("fixture is not compressed enough to exercise the read cap")
		}

		got, err := readZipEntryCapped(entry, 1024)
		if !errors.Is(err, errResultPackageTooLarge) {
			t.Fatalf("err = %v, want errResultPackageTooLarge (read %d bytes)", err, len(got))
		}
		if !strings.Contains(err.Error(), "expands past") {
			t.Errorf("error = %q, want it to name the expansion", err)
		}
	})

	t.Run("accepts an entry at the cap", func(t *testing.T) {
		data := buildZip(t, map[string]string{"ok.txt": strings.Repeat("a", 2048)})
		entry := openEntry(t, data, "ok.txt")

		got, err := readZipEntryCapped(entry, 2048)
		if err != nil {
			t.Fatalf("readZipEntryCapped() = %v, want nil at exactly the cap", err)
		}
		if len(got) != 2048 {
			t.Errorf("len = %d, want 2048", len(got))
		}
	})
}

// Every extracted image is held in memory at once, so a package of many small
// entries expands without ever tripping a per-entry cap.
func TestExtractMarkdownZipEnforcesTotalImageBudget(t *testing.T) {
	files := map[string]string{
		"doc.md": "![a](img/a.png)\n![b](img/b.png)\n![c](img/c.png)\n![d](img/d.png)",
	}
	for _, name := range []string{"img/a.png", "img/b.png", "img/c.png", "img/d.png"} {
		files[name] = strings.Repeat("i", 1024)
	}
	data := buildZip(t, files)

	t.Run("refuses a package over the budget", func(t *testing.T) {
		_, _, err := extractMarkdownZipBounded(data, "Test", 3*1024)
		if !errors.Is(err, errResultPackageTooLarge) {
			t.Fatalf("err = %v, want errResultPackageTooLarge", err)
		}
	})

	t.Run("still extracts a package within budget", func(t *testing.T) {
		md, refs, err := extractMarkdownZipBounded(data, "Test", 8*1024)
		if err != nil {
			t.Fatalf("extractMarkdownZipBounded() = %v, want nil", err)
		}
		if len(refs) != 4 {
			t.Errorf("extracted %d images, want 4", len(refs))
		}
		for _, r := range refs {
			if len(r.ImageData) != 1024 {
				t.Errorf("%s: %d bytes, want 1024", r.Filename, len(r.ImageData))
			}
		}
		if !strings.Contains(md, "img/a.png") {
			t.Errorf("markdown = %q, want the image references preserved", md)
		}
	})
}
