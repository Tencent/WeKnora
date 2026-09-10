package docparser

import (
	"archive/zip"
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Tencent/WeKnora/internal/utils"
)

func TestDownloadAndExtractZipExtractsHTMLImageReferences(t *testing.T) {
	utils.SetSSRFWhitelistFromRaw("127.0.0.1")
	t.Cleanup(func() {
		utils.SetSSRFWhitelistFromRaw("")
	})

	zipData := buildMinerUCloudTestZip(t, map[string][]byte{
		"full.md":            []byte(`<table><tr><td><img src="images/diagram.jpg" alt="diagram"/></td></tr></table>`),
		"images/diagram.jpg": []byte("jpeg bytes"),
	})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/zip")
		_, _ = w.Write(zipData)
	}))
	t.Cleanup(server.Close)

	_, imageRefs, err := downloadAndExtractZip(server.URL)
	if err != nil {
		t.Fatalf("downloadAndExtractZip() error: %v", err)
	}
	if len(imageRefs) != 1 {
		t.Fatalf("image refs = %d, want 1", len(imageRefs))
	}

	ref := imageRefs[0]
	if ref.OriginalRef != "images/diagram.jpg" {
		t.Errorf("OriginalRef = %q, want %q", ref.OriginalRef, "images/diagram.jpg")
	}
	if ref.Filename != "diagram.jpg" {
		t.Errorf("Filename = %q, want %q", ref.Filename, "diagram.jpg")
	}
	if ref.MimeType != "image/jpeg" {
		t.Errorf("MimeType = %q, want %q", ref.MimeType, "image/jpeg")
	}
	if !bytes.Equal(ref.ImageData, []byte("jpeg bytes")) {
		t.Errorf("ImageData = %q, want %q", ref.ImageData, "jpeg bytes")
	}
}

func buildMinerUCloudTestZip(t *testing.T, files map[string][]byte) []byte {
	t.Helper()

	var buf bytes.Buffer
	writer := zip.NewWriter(&buf)
	for name, data := range files {
		file, err := writer.Create(name)
		if err != nil {
			t.Fatalf("create zip entry %q: %v", name, err)
		}
		if _, err := file.Write(data); err != nil {
			t.Fatalf("write zip entry %q: %v", name, err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}
	return buf.Bytes()
}
