package chatpipeline

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIntoChatMessagePreservesStandaloneImageHandles(t *testing.T) {
	const ocr = "Recognized scan text: account A123."
	const caption = "A chart of quarterly revenue."
	for _, vision := range []bool{false, true} {
		for _, faqPriority := range []bool{false, true} {
			cm := contextImagesManage(vision)
			cm.FAQPriorityEnabled = faqPriority
			cm.MergeResult = []*types.SearchResult{
				{
					ID: "ocr-hit-1", Content: ocr, ChunkType: string(types.ChunkTypeImageOCR),
					ImageInfo: `[{"url":"resource://scan","ocr_text":"` + ocr + `"}]`,
				},
				{
					ID: "caption-hit-1", Content: caption, ChunkType: string(types.ChunkTypeImageCaption),
					ImageInfo: `[{"url":"resource://chart","caption":"` + caption + `"}]`,
				},
				{
					ID: "text-hit-1", Content: "A plain text passage.", ChunkType: string(types.ChunkTypeText),
					ImageInfo: `[{"url":"resource://unrelated-thumbnail","caption":"orphan caption"}]`,
				},
				{ID: "faq-hit-1", Content: "A frequently asked question.", ChunkType: string(types.ChunkTypeFAQ)},
			}
			before, err := json.Marshal(cm.MergeResult)
			require.NoError(t, err)
			kb := &imageKBService{}
			plugin := &PluginIntoChatMessage{kbService: kb}
			require.Nil(t, plugin.OnEvent(context.Background(), types.INTO_CHAT_MESSAGE, cm,
				func() *PluginError { return nil }))
			messages, _ := prepareMessagesWithModelContext(context.Background(), cm)
			require.NotEmpty(t, messages)
			for _, content := range []string{cm.RenderedContexts, cm.UserContent, messages[len(messages)-1].Content} {
				assert.Contains(t, content, "![image](resource://scan)")
				assert.Contains(t, content, "![image](resource://chart)")
				assert.Equal(t, 1, strings.Count(content, ocr), "OCR body must appear only once")
				assert.Equal(t, 1, strings.Count(content, caption), "caption body must appear only once")
				assert.NotContains(t, content, "resource://unrelated-thumbnail")
				assert.NotContains(t, content, "orphan caption")
			}
			assert.Empty(t, kb.reads, "text retrieval must not trigger new image downloads")
			assert.Empty(t, cm.ContextImages)
			after, err := json.Marshal(cm.MergeResult)
			require.NoError(t, err)
			assert.JSONEq(t, string(before), string(after), "rendering must not alter retrieval evidence")
		}
	}
}

func TestImageChunkHandlesRespectExistingLinks(t *testing.T) {
	for _, tc := range []struct {
		name, content, info, want string
		chunkType                 types.ChunkType
	}{
		{
			name: "another inline image does not hide the hit", chunkType: types.ChunkTypeImageOCR,
			content: "![other](resource://other)", info: `[{"url":"resource://hit"}]`,
			want: "![other](resource://other)\n\n![image](resource://hit)",
		},
		{
			name: "existing markdown", chunkType: types.ChunkTypeImageCaption,
			content: "![existing](resource://hit)", info: `[{"url":"resource://hit"}]`,
			want: "![existing](resource://hit)",
		},
		{
			name: "existing HTML original URL", chunkType: types.ChunkTypeImageVector,
			content: `<img src="images/original.png">`,
			info:    `[{"url":"resource://hit","original_url":"images/original.png"}]`,
			want:    `<img src="images/original.png">`,
		},
		{
			name: "duplicate metadata and original URL fallback", chunkType: types.ChunkTypeImageVector,
			content: "A vector image hit.",
			info:    `[{"url":"resource://hit"},{"url":"resource://hit"},{"original_url":"resource://second"}]`,
			want:    "A vector image hit.\n\n![image](resource://hit)\n\n![image](resource://second)",
		},
		{
			name: "OCR keeps complementary caption", chunkType: types.ChunkTypeImageOCR,
			content: "Recognized text.",
			info:    `[{"url":"resource://scan","ocr_text":"Recognized text.","caption":"Page overview"}]`,
			want:    "Recognized text.\n\n![image](resource://scan)\n\n> **Image caption:** Page overview",
		},
		{
			name: "empty body still exposes its image", chunkType: types.ChunkTypeImageCaption,
			info: `[{"url":"resource://hit","caption":"A figure"}]`,
			want: "![image](resource://hit)\n\n> **Image caption:** A figure",
		},
		{
			name: "invalid metadata", chunkType: types.ChunkTypeImageOCR,
			content: "Recognized text.", info: `{broken`, want: "Recognized text.",
		},
		{
			name: "no image address", chunkType: types.ChunkTypeImageOCR,
			content: "Recognized text.", info: `[{"ocr_text":"Recognized text."}]`, want: "Recognized text.",
		},
		{
			name: "parent text does not expose extras", chunkType: types.ChunkTypeParentText,
			content: "![kept](resource://kept)", info: `[{"url":"resource://extra"}]`,
			want: "![kept](resource://kept)",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result := &types.SearchResult{Content: tc.content, ImageInfo: tc.info, ChunkType: string(tc.chunkType)}
			assert.Equal(t, tc.want, getEnrichedPassageForChat(context.Background(), result))
		})
	}
}
