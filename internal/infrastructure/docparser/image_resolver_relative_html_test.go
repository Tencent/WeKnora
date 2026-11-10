package docparser

import (
	"context"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

func TestResolveAndStoreRelativeHTMLImages(t *testing.T) {
	png := createTestPNG(200, 150)
	result := &types.ReadResult{
		MarkdownContent: `<table><tr><td><img src="images/profile.jpg" alt="profile"/></td></tr></table>`,
		ImageRefs: []types.ImageRef{
			{
				Filename:    "profile.jpg",
				OriginalRef: "images/profile.jpg",
				MimeType:    "image/png",
				ImageData:   png,
			},
		},
	}

	svc := &captureSaveBytes{}
	r := NewImageResolver()
	out, imgs, err := r.ResolveAndStore(context.Background(), result, svc, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(imgs) != 1 {
		t.Fatalf("expected 1 stored image but got %d", len(imgs))
	}
	if len(svc.saved) != 1 {
		t.Fatalf("expected SaveBytes to be called once but got %d", len(svc.saved))
	}
	if strings.Contains(out, `src="images/profile.jpg"`) {
		t.Fatalf("expected relative html img src to be replaced, got: %s", out)
	}
	if !strings.Contains(out, `src="local://test/`) {
		t.Fatalf("expected stored file url in html img src, got: %s", out)
	}
}

// TestResolveAndStoreDedupsSameImageRefVariants ensures that when the same
// MinerU image is referenced under multiple path forms (e.g. "images/foo.png"
// and "./images/foo.png"), the bytes are uploaded to object storage only once.
func TestResolveAndStoreDedupsSameImageRefVariants(t *testing.T) {
	png := createTestPNG(200, 150)
	result := &types.ReadResult{
		MarkdownContent: strings.Join([]string{
			"![](images/foo.png)",
			`<img src="./images/foo.png"/>`,
		}, "\n"),
		ImageRefs: []types.ImageRef{
			{
				Filename:    "foo.png",
				OriginalRef: "images/foo.png",
				MimeType:    "image/png",
				ImageData:   png,
			},
			{
				Filename:    "foo.png",
				OriginalRef: "./images/foo.png",
				MimeType:    "image/png",
				ImageData:   png,
			},
		},
	}

	svc := &captureSaveBytes{}
	r := NewImageResolver()
	_, _, err := r.ResolveAndStore(context.Background(), result, svc, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(svc.saved) != 1 {
		t.Fatalf("expected SaveBytes to be called once for duplicate refs, got %d", len(svc.saved))
	}
}
