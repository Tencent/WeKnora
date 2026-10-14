package utils

import (
	"strings"
	"testing"
	"unicode/utf8"

	"golang.org/x/text/encoding/simplifiedchinese"
)

func mustGBK(t *testing.T, utf8Name string) string {
	t.Helper()
	encoded, err := simplifiedchinese.GBK.NewEncoder().Bytes([]byte(utf8Name))
	if err != nil {
		t.Fatalf("GBK encode %q: %v", utf8Name, err)
	}
	// Mimic Go's multipart.FileHeader: invalid UTF-8 stored in a Go string.
	return string(encoded)
}

func TestNormalizeUploadFilenameUTF8Passthrough(t *testing.T) {
	t.Parallel()
	name := "基于统一场论的月行九道天文数学原理研究.pdf"
	if got := NormalizeUploadFilename(name); got != name {
		t.Fatalf("UTF-8 name changed: got %q want %q", got, name)
	}
	ascii := "K_Neutrino_Capture.pdf"
	if got := NormalizeUploadFilename(ascii); got != ascii {
		t.Fatalf("ASCII name changed: got %q want %q", got, ascii)
	}
}

func TestNormalizeUploadFilenameGBKChinese(t *testing.T) {
	t.Parallel()
	want := "基于统一场论的月行九道天文数学原理研究.pdf"
	gbkName := mustGBK(t, want)
	if utf8.ValidString(gbkName) {
		t.Fatal("expected GBK bytes to be invalid UTF-8")
	}
	if _, ok := ValidateInput(gbkName); ok {
		t.Fatal("raw GBK filename should fail ValidateInput before normalization")
	}

	got := NormalizeUploadFilename(gbkName)
	if got != want {
		t.Fatalf("GBK normalize: got %q want %q", got, want)
	}
	safe, ok := ValidateInput(got)
	if !ok {
		t.Fatal("normalized GBK filename should pass ValidateInput")
	}
	if safe != want {
		t.Fatalf("validated name: got %q want %q", safe, want)
	}
}

func TestNormalizeUploadFilenameGBKShortCJK(t *testing.T) {
	t.Parallel()
	want := "测试文档.pdf"
	got := NormalizeUploadFilename(mustGBK(t, want))
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestNormalizeUploadFilenameRejectsUndecodable(t *testing.T) {
	t.Parallel()
	// Lone invalid byte sequences that are not valid GBK either.
	raw := string([]byte{0xff, 0xfe, 0xfd})
	got := NormalizeUploadFilename(raw)
	if got != raw {
		t.Fatalf("undecodable input should be unchanged, got %q", got)
	}
	if utf8.ValidString(got) {
		t.Fatal("undecodable input unexpectedly became valid UTF-8")
	}
}

func TestNormalizeUploadFilenameStillBlocksXSSAfterGBK(t *testing.T) {
	t.Parallel()
	// GBK-encoded script tag must still fail ValidateInput after decode.
	payload := "<script>alert(1)</script>.pdf"
	gbkName := mustGBK(t, payload)
	normalized := NormalizeUploadFilename(gbkName)
	if normalized != payload {
		t.Fatalf("normalize: got %q want %q", normalized, payload)
	}
	if _, ok := ValidateInput(normalized); ok {
		t.Fatal("XSS payload must still be rejected after GBK normalization")
	}
}

func TestValidateInputAcceptsNormalizedChinese(t *testing.T) {
	t.Parallel()
	name := "自动同步端到端自测.pdf"
	safe, ok := ValidateInput(NormalizeUploadFilename(mustGBK(t, name)))
	if !ok {
		t.Fatal("expected valid")
	}
	if !strings.HasSuffix(safe, ".pdf") || safe != name {
		t.Fatalf("got %q want %q", safe, name)
	}
}
