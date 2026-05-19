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
