//go:build anydoc && cgo

package anydoc

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	upstream "github.com/firecrawl/anydoc/go"
)

// ANYDOC_UPSTREAM_DIR points to the checked-out release tag. This opt-in
// upgrade check runs the full upstream corpus through our actual Go ABI and
// compares Markdown/error details to the upstream Rust snapshots.
func TestUpstreamCorpus(t *testing.T) {
	root := os.Getenv("ANYDOC_UPSTREAM_DIR")
	if root == "" {
		t.Skip("set ANYDOC_UPSTREAM_DIR to an anydoc release checkout")
	}
	snapshots, err := filepath.Glob(filepath.Join(root, "tests/snapshots/snapshots__*.snap"))
	if err != nil || len(snapshots) == 0 {
		t.Fatalf("no snapshots: %v", err)
	}
	for _, path := range snapshots {
		name := strings.TrimSuffix(strings.TrimPrefix(filepath.Base(path), "snapshots__"), ".snap")
		parts := strings.Split(name, "__")
		if len(parts) != 2 {
			continue
		}
		t.Run(name, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join(root, "tests/fixtures", parts[0], parts[1]))
			if err != nil {
				t.Fatal(err)
			}
			snapshot, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			_, expected, ok := strings.Cut(string(snapshot[4:]), "---\n")
			if !ok {
				t.Fatal("invalid snapshot header")
			}
			format, ok := upstream.FormatFromExtension(strings.TrimPrefix(filepath.Ext(parts[1]), "."))
			if !ok {
				t.Fatal("unrecognized fixture extension")
			}
			// The Go binding rejects empty slices before entering C, retaining
			// its established empty-input error instead of the ZIP diagnostic.
			if len(data) == 0 {
				if !strings.HasPrefix(expected, "ERROR:") {
					t.Fatal("empty fixture should fail")
				}
				expected = "ERROR: empty input"
			}
			actual, err := upstream.ToMarkdownBytes(data, &format)
			if err != nil {
				var typed *upstream.ConvertError
				if !errors.As(err, &typed) {
					t.Fatal(err)
				}
				actual = "ERROR: " + typed.Detail
			}
			if strings.TrimSpace(actual) != strings.TrimSpace(expected) {
				t.Fatalf("snapshot mismatch\nwant: %s\ngot: %s", expected, actual)
			}
			if err == nil && format != upstream.FormatPdf {
				document, err := upstream.ToDocument(data, &format)
				if err != nil {
					t.Fatalf("document ABI: %v", err)
				}
				markdown, err := upstream.ToMarkdownWithAssetLinks(data, &format)
				if err != nil {
					t.Fatalf("asset renderer: %v", err)
				}
				combined, combinedMarkdown, err := upstream.ToDocumentWithAssetLinks(data, &format)
				if err != nil {
					t.Fatalf("combined ABI: %v", err)
				}
				if !reflect.DeepEqual(combined, document) || combinedMarkdown != markdown {
					t.Fatal("single-parse output differs from independent document/Markdown APIs")
				}
			}
		})
	}
}
