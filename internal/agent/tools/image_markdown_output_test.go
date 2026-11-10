package tools

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

func TestLLMToolOutputsUseMarkdownImages(t *testing.T) {
	imageInfo, err := json.Marshal([]types.ImageInfo{
		{
			URL:     "resource://AbCdEfGhIjKlMnOpQrStUv",
			Caption: "目标说话人提取流程图",
			OCRText: "输入\n输出",
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	chunk := &types.Chunk{
		ID:         "chunk-1",
		ChunkIndex: 0,
		ChunkType:  types.ChunkTypeText,
		Content:    "测试阶段主要流程",
		ImageInfo:  string(imageInfo),
	}

	readOutput := (&ReadDocumentTool{}).buildOutput(
		&types.Knowledge{ID: "knowledge-1", Title: "测试文档"}, 1, []readChunkRow{{chunk: chunk}}, "",
	)
	enrichedOutput := enrichChunkContent(chunk)
	for name, output := range map[string]string{
		"read_document":        readOutput,
		"enrich_chunk_content": enrichedOutput,
	} {
		t.Run(name, func(t *testing.T) {
			if !strings.Contains(output, "![目标说话人提取流程图](resource://AbCdEfGhIjKlMnOpQrStUv)") {
				t.Fatalf("expected Markdown image in tool output:\n%s", output)
			}
			if strings.Contains(output, "<image") || strings.Contains(output, "<caption>") || strings.Contains(output, "<ocr_text>") {
				t.Fatalf("tool output leaked legacy image XML:\n%s", output)
			}
		})
	}
}
