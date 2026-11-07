package docparser

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestStandaloneImageFilenamesResolveAndStore(t *testing.T) {
	filenames := []string{
		"diagram.png", "diagram).png", "diagram(.png", "diagram[1].png",
		"diagram].png", "diagram](extra).png", `diagram\.png`, "diagram .png",
		"100%20complete.png", "diagram#1?.png", "第 1 张图片(终稿).png",
		"line\nbreak.png", "line\r\nbreak.png", `quote".png`,
	}
	for _, filename := range filenames {
		for _, fallback := range []bool{false, true} {
			name := "simple/" + filename
			if fallback {
				name = "fallback/" + filename
			}
			t.Run(name, func(t *testing.T) {
				data := createTestPNG(64, 64)
				req := &types.ReadRequest{FileName: filename, FileType: "png", FileContent: data}
				result, err := (&SimpleFormatReader{}).Read(context.Background(), req)
				require.NoError(t, err)
				if fallback {
					result.MarkdownContent, result.ImageRefs = ensureOriginalImageRef(req, "OCR text", nil)
				}
				require.Len(t, result.ImageRefs, 1)
				require.Equal(t, filename, result.ImageRefs[0].Filename)
				svc := &captureSaveBytes{}
				markdown, images, err := NewImageResolver().ResolveAndStore(context.Background(), result, svc, 1)
				require.NoError(t, err)
				require.Len(t, svc.saved, 1, "image must reach storage: %s", result.MarkdownContent)
				require.Equal(t, data, svc.saved[0])
				require.Len(t, images, 1)
				require.Contains(t, markdown, "]("+svc.urls[0]+")")
				require.NotContains(t, markdown, result.ImageRefs[0].OriginalRef)
				if fallback {
					require.Contains(t, markdown, "OCR text")
				}

				// Running the fallback on its own generated reference must not add a second image.
				again, refs := ensureOriginalImageRef(req, result.MarkdownContent, result.ImageRefs)
				require.Equal(t, result.MarkdownContent, again)
				require.Len(t, refs, 1)
			})
		}
	}
}

func TestStandaloneImageMarkdownEscapesSyntax(t *testing.T) {
	result := imageToResult(`a[b]\c).png`, []byte("image"))
	require.Equal(t, `![a\[b\]\\c).png](images/a%5Bb%5D%5Cc%29.png)`, result.MarkdownContent)
	require.Equal(t, "images/a%5Bb%5D%5Cc%29.png", result.ImageRefs[0].OriginalRef)
}
