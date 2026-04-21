package nutstore

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestParseResponse_RootDirSelfReference(t *testing.T) {
	// When basePath is "/", the self-referencing "/" entry must be skipped
	client := &Client{}

	r := response{
		Href: "/dav/",
		Propstat: propstat{
			Prop: prop{
				DisplayName:  "",
				ResourceType: resourceType{Collection: &struct{}{}},
			},
		},
	}

	fi := client.parseResponse(r, "/")
	if fi != nil {
		t.Errorf("expected root self-reference to be skipped, got %+v", fi)
	}
}

func TestParseResponse_NonRootDirSelfReference(t *testing.T) {
	client := &Client{}

	r := response{
		Href: "/dav/sub1/",
		Propstat: propstat{
			Prop: prop{
				DisplayName:  "sub1",
				ResourceType: resourceType{Collection: &struct{}{}},
			},
		},
	}

	fi := client.parseResponse(r, "/sub1/")
	if fi != nil {
		t.Errorf("expected dir self-reference to be skipped, got %+v", fi)
	}
}

func TestListDirectoryRecursive_ManualWalk(t *testing.T) {
	// Mock directory structure:
	// /root/
	//   ├── sub1/
	//   │   └── a.pdf
	//   ├── sub2/
	//   │   ├── deep/
	//   │   │   └── c.xlsx
	//   │   └── b.docx
	//   └── root.txt

	responses := map[string]string{
		"/dav/root/": `<?xml version="1.0" encoding="UTF-8"?>
<d:multistatus xmlns:d="DAV:">
  <d:response><d:href>/dav/root/</d:href><d:propstat><d:prop><d:displayname>root</d:displayname><d:resourcetype><d:collection/></d:resourcetype></d:prop><d:status>HTTP/1.1 200 OK</d:status></d:propstat></d:response>
  <d:response><d:href>/dav/root/sub1/</d:href><d:propstat><d:prop><d:displayname>sub1</d:displayname><d:resourcetype><d:collection/></d:resourcetype></d:prop><d:status>HTTP/1.1 200 OK</d:status></d:propstat></d:response>
  <d:response><d:href>/dav/root/sub2/</d:href><d:propstat><d:prop><d:displayname>sub2</d:displayname><d:resourcetype><d:collection/></d:resourcetype></d:prop><d:status>HTTP/1.1 200 OK</d:status></d:propstat></d:response>
  <d:response><d:href>/dav/root/root.txt</d:href><d:propstat><d:prop><d:displayname>root.txt</d:displayname><d:getcontentlength>100</d:getcontentlength><d:resourcetype/></d:prop><d:status>HTTP/1.1 200 OK</d:status></d:propstat></d:response>
</d:multistatus>`,
		"/dav/root/sub1/": `<?xml version="1.0" encoding="UTF-8"?>
<d:multistatus xmlns:d="DAV:">
  <d:response><d:href>/dav/root/sub1/</d:href><d:propstat><d:prop><d:displayname>sub1</d:displayname><d:resourcetype><d:collection/></d:resourcetype></d:prop><d:status>HTTP/1.1 200 OK</d:status></d:propstat></d:response>
  <d:response><d:href>/dav/root/sub1/a.pdf</d:href><d:propstat><d:prop><d:displayname>a.pdf</d:displayname><d:getcontentlength>200</d:getcontentlength><d:resourcetype/></d:prop><d:status>HTTP/1.1 200 OK</d:status></d:propstat></d:response>
</d:multistatus>`,
		"/dav/root/sub2/": `<?xml version="1.0" encoding="UTF-8"?>
<d:multistatus xmlns:d="DAV:">
  <d:response><d:href>/dav/root/sub2/</d:href><d:propstat><d:prop><d:displayname>sub2</d:displayname><d:resourcetype><d:collection/></d:resourcetype></d:prop><d:status>HTTP/1.1 200 OK</d:status></d:propstat></d:response>
  <d:response><d:href>/dav/root/sub2/deep/</d:href><d:propstat><d:prop><d:displayname>deep</d:displayname><d:resourcetype><d:collection/></d:resourcetype></d:prop><d:status>HTTP/1.1 200 OK</d:status></d:propstat></d:response>
  <d:response><d:href>/dav/root/sub2/b.docx</d:href><d:propstat><d:prop><d:displayname>b.docx</d:displayname><d:getcontentlength>300</d:getcontentlength><d:resourcetype/></d:prop><d:status>HTTP/1.1 200 OK</d:status></d:propstat></d:response>
</d:multistatus>`,
		"/dav/root/sub2/deep/": `<?xml version="1.0" encoding="UTF-8"?>
<d:multistatus xmlns:d="DAV:">
  <d:response><d:href>/dav/root/sub2/deep/</d:href><d:propstat><d:prop><d:displayname>deep</d:displayname><d:resourcetype><d:collection/></d:resourcetype></d:prop><d:status>HTTP/1.1 200 OK</d:status></d:propstat></d:response>
  <d:response><d:href>/dav/root/sub2/deep/c.xlsx</d:href><d:propstat><d:prop><d:displayname>c.xlsx</d:displayname><d:getcontentlength>400</d:getcontentlength><d:resourcetype/></d:prop><d:status>HTTP/1.1 200 OK</d:status></d:propstat></d:response>
</d:multistatus>`,
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "PROPFIND" {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if depth := r.Header.Get("Depth"); depth != "1" {
			t.Errorf("expected Depth:1, got %q", depth)
		}
		body, ok := responses[r.URL.Path]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "text/xml")
		w.WriteHeader(207)
		w.Write([]byte(body))
	}))
	defer server.Close()

	client := &Client{
		baseURL: server.URL,
		http:    &http.Client{Timeout: 10 * time.Second},
	}

	files, err := client.ListDirectoryRecursive(context.Background(), "/root/")
	if err != nil {
		t.Fatalf("ListDirectoryRecursive failed: %v", err)
	}

	fileNames := make(map[string]bool)
	dirNames := make(map[string]bool)
	for _, f := range files {
		if f.IsDir {
			dirNames[f.Name] = true
		} else {
			fileNames[f.Name] = true
		}
	}

	for _, name := range []string{"root.txt", "a.pdf", "b.docx", "c.xlsx"} {
		if !fileNames[name] {
			t.Errorf("missing file %q in results", name)
		}
	}
	for _, name := range []string{"sub1", "sub2", "deep"} {
		if !dirNames[name] {
			t.Errorf("missing directory %q in results", name)
		}
	}
	if len(files) != 7 {
		t.Errorf("expected 7 items (3 dirs + 4 files), got %d", len(files))
		for _, f := range files {
			t.Logf("  %s (dir=%v)", f.Path, f.IsDir)
		}
	}
}

func TestListDirectoryRecursive_SubdirErrorReturnsError(t *testing.T) {
	responses := map[string]string{
		"/dav/root/": `<?xml version="1.0" encoding="UTF-8"?>
<d:multistatus xmlns:d="DAV:">
  <d:response><d:href>/dav/root/</d:href><d:propstat><d:prop><d:displayname>root</d:displayname><d:resourcetype><d:collection/></d:resourcetype></d:prop><d:status>HTTP/1.1 200 OK</d:status></d:propstat></d:response>
  <d:response><d:href>/dav/root/broken/</d:href><d:propstat><d:prop><d:displayname>broken</d:displayname><d:resourcetype><d:collection/></d:resourcetype></d:prop><d:status>HTTP/1.1 200 OK</d:status></d:propstat></d:response>
</d:multistatus>`,
		// /dav/root/broken/ is NOT registered — will return 404
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, ok := responses[r.URL.Path]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "text/xml")
		w.WriteHeader(207)
		w.Write([]byte(body))
	}))
	defer server.Close()

	client := &Client{
		baseURL: server.URL,
		http:    &http.Client{Timeout: 10 * time.Second},
	}

	_, err := client.ListDirectoryRecursive(context.Background(), "/root/")
	if err == nil {
		t.Fatal("expected error when subdirectory listing fails, got nil")
	}
}

func TestListDirectoryRecursive_RootPath(t *testing.T) {
	responses := map[string]string{
		"/dav/": `<?xml version="1.0" encoding="UTF-8"?>
<d:multistatus xmlns:d="DAV:">
  <d:response><d:href>/dav/</d:href><d:propstat><d:prop><d:displayname></d:displayname><d:resourcetype><d:collection/></d:resourcetype></d:prop><d:status>HTTP/1.1 200 OK</d:status></d:propstat></d:response>
  <d:response><d:href>/dav/file.txt</d:href><d:propstat><d:prop><d:displayname>file.txt</d:displayname><d:getcontentlength>50</d:getcontentlength><d:resourcetype/></d:prop><d:status>HTTP/1.1 200 OK</d:status></d:propstat></d:response>
</d:multistatus>`,
	}

	var requestCount int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&requestCount, 1)
		if atomic.LoadInt32(&requestCount) > 10 {
			// Don't t.Fatal in handler goroutine — just return 500 to break the loop
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		body, ok := responses[r.URL.Path]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "text/xml")
		w.WriteHeader(207)
		w.Write([]byte(body))
	}))
	defer server.Close()

	client := &Client{
		baseURL: server.URL,
		http:    &http.Client{Timeout: 10 * time.Second},
	}

	files, err := client.ListDirectoryRecursive(context.Background(), "/")
	if err != nil {
		t.Fatalf("ListDirectoryRecursive failed: %v", err)
	}
	if len(files) != 1 {
		t.Errorf("expected 1 file, got %d", len(files))
	}
	count := atomic.LoadInt32(&requestCount)
	if count != 1 {
		t.Errorf("expected 1 PROPFIND request, got %d (possible self-loop)", count)
	}
}
