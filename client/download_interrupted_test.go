package client

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestDownloadInterruptedPreservesExistingFile(t *testing.T) {
	for _, batch := range []bool{false, true} {
		name := "single"
		if batch {
			name = "batch"
		}
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Length", "100")
				_, _ = w.Write([]byte("partial"))
			}))
			defer server.Close()
			dest := filepath.Join(t.TempDir(), "saved.bin")
			if err := os.WriteFile(dest, []byte("previous complete file"), 0o640); err != nil {
				t.Fatal(err)
			}
			c := NewClient(server.URL)
			var err error
			if batch {
				err = c.DownloadKnowledgeFiles(context.Background(), "kb-test", []string{"doc-test"}, dest)
			} else {
				err = c.DownloadKnowledgeFile(context.Background(), "doc-test", dest)
			}
			if err == nil {
				t.Fatal("expected truncated response error")
			}
			got, err := os.ReadFile(dest)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != "previous complete file" {
				t.Fatalf("previous file changed: %q", got)
			}
			entries, err := os.ReadDir(filepath.Dir(dest))
			if err != nil {
				t.Fatal(err)
			}
			if len(entries) != 1 {
				t.Fatalf("left temporary files: %v", entries)
			}
		})
	}
}

func TestDownloadPreservesSymlink(t *testing.T) {
	for _, batch := range []bool{false, true} {
		for _, interrupted := range []bool{false, true} {
			name := "single-complete"
			if batch {
				name = "batch-complete"
			}
			if interrupted {
				name += "-interrupted"
			}
			t.Run(name, func(t *testing.T) {
				dir := t.TempDir()
				target := filepath.Join(dir, "target.bin")
				link := filepath.Join(dir, "link.bin")
				if err := os.WriteFile(target, []byte("original"), 0o640); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target, link); err != nil {
					t.Skipf("symlink unavailable: %v", err)
				}
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					if interrupted {
						w.Header().Set("Content-Length", "100")
					}
					_, _ = w.Write([]byte("replacement"))
				}))
				defer server.Close()
				c := NewClient(server.URL)
				var err error
				if batch {
					err = c.DownloadKnowledgeFiles(context.Background(), "kb-test", []string{"doc-test"}, link)
				} else {
					err = c.DownloadKnowledgeFile(context.Background(), "doc-test", link)
				}
				expected := "replacement"
				if interrupted {
					if err == nil {
						t.Fatal("expected truncated response error")
					}
					expected = "original"
				} else if err != nil {
					t.Fatal(err)
				}
				got, err := os.ReadFile(target)
				if err != nil {
					t.Fatal(err)
				}
				if string(got) != expected {
					t.Fatalf("target content: %q, want %q", got, expected)
				}
				resolved, err := os.Readlink(link)
				if err != nil {
					t.Fatal(err)
				}
				if resolved != target {
					t.Fatalf("symlink changed: %q", resolved)
				}
				entries, err := os.ReadDir(dir)
				if err != nil {
					t.Fatal(err)
				}
				if len(entries) != 2 {
					t.Fatalf("temporary file left: %v", entries)
				}
			})
		}
	}
}

func TestDownloadSuccessfulReplacement(t *testing.T) {
	for _, batch := range []bool{false, true} {
		name := "single"
		if batch {
			name = "batch"
		}
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			dest := filepath.Join(dir, "saved.bin")
			if err := os.WriteFile(dest, []byte("old"), 0o640); err != nil {
				t.Fatal(err)
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte("complete"))
			}))
			defer server.Close()
			c := NewClient(server.URL)
			var err error
			if batch {
				err = c.DownloadKnowledgeFiles(context.Background(), "kb-test", []string{"doc-test"}, dest)
			} else {
				err = c.DownloadKnowledgeFile(context.Background(), "doc-test", dest)
			}
			if err != nil {
				t.Fatal(err)
			}
			got, err := os.ReadFile(dest)
			if err != nil || string(got) != "complete" {
				t.Fatalf("download content %q: %v", got, err)
			}
			info, err := os.Stat(dest)
			if err != nil || info.Mode().Perm() != 0o640 {
				t.Fatalf("permissions changed: %v, %v", info, err)
			}
			entries, err := os.ReadDir(dir)
			if err != nil || len(entries) != 1 {
				t.Fatalf("temporary files remain: %v, %v", entries, err)
			}
		})
	}
}
