package docparser

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	secutils "github.com/Tencent/WeKnora/internal/utils"
	"github.com/stretchr/testify/require"
)

func TestSortImagesByPosition_远程和本地图按最终文档顺序合并(t *testing.T) {
	for _, tc := range []struct {
		name     string
		markdown string
		refs     []types.ImageRef
		wantRefs []string
	}{
		{name: "同页两张远程图", markdown: "![A](%s/a.png)\n![B](%s/b.png)", wantRefs: []string{"/a.png", "/b.png"}},
		{
			name: "同页HTML与Markdown远图", markdown: `<img src="%s/a.png">` + "\n![B](%s/b.png)",
			wantRefs: []string{"/a.png", "/b.png"},
		},
		{
			name: "同页远图与本地图交错", markdown: "![A](%s/a.png)\n![local](images/local.png)\n![B](%s/b.png)",
			refs: []types.ImageRef{{
				Filename: "local.png", OriginalRef: "images/local.png",
				MimeType: "image/png", ImageData: createTestPNG(200, 150),
			}},
			wantRefs: []string{"/a.png", "images/local.png", "/b.png"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// 先注册缓存清理，让它在 Setenv 恢复环境后执行，避免影响后续 SSRF 测试。
			t.Cleanup(secutils.ResetSSRFWhitelistForTest)
			allowLocalhost(t)
			server, _ := remoteImageServer(t)
			result := &types.ReadResult{
				MarkdownContent: fmt.Sprintf(tc.markdown, server.URL, server.URL), ImageRefs: tc.refs,
			}
			resolver := NewImageResolver()
			fileSvc := &mockFileService{}
			markdown, local, err := resolver.ResolveAndStore(context.Background(), result, fileSvc, 1)
			require.NoError(t, err, "本地图解析应成功")
			markdown, remote, err := resolver.ResolveRemoteImages(context.Background(), markdown, fileSvc, 1)
			require.NoError(t, err, "远程图解析应成功")
			images := append(local, remote...)
			require.Len(t, images, len(tc.wantRefs), "合并不得丢失图片")
			// 来源定位附在图片对象上，排序后必须继续属于同一张图。
			for i := range images {
				images[i].SourceLocators = types.SourceLocators{{
					Type: types.SourceLocatorPDF, Page: 2, Quote: images[i].OriginalRef,
				}}
			}
			images = SortImagesByPosition(markdown, images)
			last := -1
			for i, image := range images {
				require.True(t, strings.HasSuffix(image.OriginalRef, tc.wantRefs[i]), "合并后的序号必须跟随文档，而非下载批次")
				position := strings.Index(markdown, image.ServingURL)
				require.Greater(t, position, last, "同页图序号必须跟随最终Markdown中的实际出现顺序")
				require.Equal(t, image.OriginalRef, image.SourceLocators[0].Quote, "排序不能错配图片来源定位")
				require.Equal(t, 2, image.SourceLocators[0].Page, "页码必须保持原有一基值")
				last = position
			}
		})
	}
}

func TestSortImagesByPosition_未知位置与重复引用保持稳定(t *testing.T) {
	images := []StoredImage{
		{OriginalRef: "未知甲", ServingURL: "local://missing-a"},
		{OriginalRef: "第二张", ServingURL: "local://b"},
		{OriginalRef: "第一张", ServingURL: "local://a"},
		{OriginalRef: "未知乙"},
	}
	images = SortImagesByPosition("![A](local://a)\n![B](local://b)\n![A](local://a)", images)
	require.Equal(t,
		[]string{"第一张", "第二张", "未知甲", "未知乙"},
		[]string{images[0].OriginalRef, images[1].OriginalRef, images[2].OriginalRef, images[3].OriginalRef},
		"重复引用取首次位置，未知位置保留相对顺序",
	)
}

// 合并排序只能调整顺序，不能把超额图片重新送入多模态处理或丢失清理标记。
func TestSortImagesByPosition_保留限额与上传清理标记(t *testing.T) {
	images := []StoredImage{
		{ServingURL: "local://late", Inline: true, SkipMultimodal: true},
		{ServingURL: "local://early", NewlyStored: true},
		{ServingURL: "local://middle", Inline: true},
	}
	expected := []StoredImage{images[1], images[2], images[0]}
	images = SortImagesByPosition("![A](local://early)\n![B](local://middle)\n![C](local://late)", images)
	require.Equal(t, expected, images, "排序后每张图仍应保留原有的处理及清理标记")
}
