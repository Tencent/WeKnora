package docparser

import (
	"errors"
	"io"
	"strings"
	"testing"
)

// fillerReader streams size bytes without allocating them, so a test can hand
// the cap a body far larger than it without spending the memory.
type fillerReader struct {
	remaining int64
}

func (f *fillerReader) Read(p []byte) (int, error) {
	if f.remaining <= 0 {
		return 0, io.EOF
	}
	if int64(len(p)) > f.remaining {
		p = p[:f.remaining]
	}
	for i := range p {
		p[i] = 'a'
	}
	f.remaining -= int64(len(p))
	return len(p), nil
}

// The result cap has to cover the same payload the ZIP path accepts. The image
// budget is expressed in decoded bytes, while the JSON result carries them as
// base64, so a cap equal to the raw image total would reject a legitimate
// scanned PDF.
func TestResultResponseCapCoversInlineImageBudget(t *testing.T) {
	need := int64(maxZipImagesTotalBytes)*4/3 + maxZipEntryBytes
	if maxResultResponseBytes < need {
		t.Errorf("maxResultResponseBytes = %d, want at least %d for inline images + markdown",
			maxResultResponseBytes, need)
	}
	if maxResultResponseBytes <= maxJSONResponseBytes {
		t.Errorf("result cap %d must be larger than the control-plane cap %d",
			maxResultResponseBytes, maxJSONResponseBytes)
	}
}

func TestReadCapped(t *testing.T) {
	t.Run("rejects over the cap", func(t *testing.T) {
		const limit = 1 << 10
		_, err := readCapped(&fillerReader{remaining: limit + 1}, limit)
		if !errors.Is(err, errResponseTooLarge) {
			t.Fatalf("err = %v, want errResponseTooLarge", err)
		}
		if !strings.Contains(err.Error(), "exceeds maximum size") {
			t.Errorf("error text = %q, want the canonical over-size message", err)
		}
	})

	t.Run("accepts exactly the cap", func(t *testing.T) {
		const limit = 1 << 10
		got, err := readCapped(strings.NewReader(strings.Repeat("a", limit)), limit)
		if err != nil {
			t.Fatalf("readCapped() = %v, want nil at exactly the cap", err)
		}
		if len(got) != limit {
			t.Errorf("len = %d, want %d", len(got), limit)
		}
	})

	t.Run("non-positive limit falls back to the production cap", func(t *testing.T) {
		if _, err := readCapped(strings.NewReader(`{"ok":true}`), 0); err != nil {
			t.Fatalf("readCapped(_, 0) = %v, want nil", err)
		}
	})
}

// A truncated document would be parsed as if it were the whole reply, turning an
// oversized response into a plausible-looking short one - a poll that reports
// "finished" because the tail happened to decode. So the refusal has to come
// back with nothing to parse, not with a prefix.
func TestReadCappedNeverTruncates(t *testing.T) {
	const limit = 1 << 10
	got, err := readCapped(&fillerReader{remaining: limit * 8}, limit)
	if err == nil {
		t.Fatalf("expected refusal, got %d bytes", len(got))
	}
	if got != nil {
		t.Errorf("returned %d bytes alongside the error; a caller could parse them", len(got))
	}
}

// An error preview lands in a log line and in the error the caller sees. Every
// call site below used io.ReadAll(resp.Body) on a non-200 branch, so the error
// carried the whole body and a failing endpoint could push an unbounded blob
// into both.
func TestErrorPreviewIsBoundedAndNotEmpty(t *testing.T) {
	t.Run("is bounded", func(t *testing.T) {
		if got := readErrorPreview(&fillerReader{remaining: 1 << 20}); len(got) != maxErrorPreviewBytes {
			t.Errorf("preview is %d bytes, want %d", len(got), maxErrorPreviewBytes)
		}
	})

	t.Run("keeps the start of a large body", func(t *testing.T) {
		if readErrorPreview(&fillerReader{remaining: 1 << 20}) == "" {
			t.Error("preview is empty; the start of the body is the diagnostic part")
		}
	})

	t.Run("passes a small body through", func(t *testing.T) {
		if got := readErrorPreview(strings.NewReader("upstream said no")); got != "upstream said no" {
			t.Errorf("preview = %q, want it unchanged", got)
		}
	})
}

// paddleocr_vl_cloud_converter downloads one image crop per page, so it takes
// the remote-image ceiling image_resolver.go already applies to the images it
// fetches, not the JSON one. Both paths handle the same kind of bytes, so they
// share a number.
func TestPerPageImageDownloadUsesRemoteImageCap(t *testing.T) {
	const limit = 1 << 10
	if maxRemoteImageSize != 10*1024*1024 {
		t.Fatalf("maxRemoteImageSize = %d; update this test if the cap changes", maxRemoteImageSize)
	}

	// Under the ceiling: a page crop reads through.
	got, err := readCapped(&fillerReader{remaining: limit}, maxRemoteImageSize)
	if err != nil {
		t.Fatalf("readCapped(%d bytes) = %v, want nil under maxRemoteImageSize", limit, err)
	}
	if len(got) != limit {
		t.Errorf("len = %d, want %d", len(got), limit)
	}

	// Over it: refused rather than buffered.
	over := &fillerReader{remaining: maxRemoteImageSize + 1}
	if _, err := readCapped(over, maxRemoteImageSize); !errors.Is(err, errResponseTooLarge) {
		t.Errorf("readCapped(over maxRemoteImageSize) = %v, want errResponseTooLarge", err)
	}
}
