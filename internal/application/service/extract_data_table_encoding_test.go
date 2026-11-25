package service

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
	"golang.org/x/text/encoding/simplifiedchinese"
)

type encodingSummaryChat struct {
	tableSummaryChatAPI
	prompts []string
}

func (c *encodingSummaryChat) Chat(
	_ context.Context, messages []chat.Message, _ *chat.ChatOptions,
) (*types.ChatResponse, error) {
	for _, message := range messages {
		c.prompts = append(c.prompts, message.Content)
	}
	return &types.ChatResponse{Content: "产品名称和数量"}, nil
}

func TestDataTableSummaryHandlesGBKCSV(t *testing.T) {
	svc, task, f, _, index := newTableSummaryOutputFixture(t)
	before, err := f.svc.GetKnowledgeByID(f.ctx, "doc")
	require.NoError(t, err)
	data, err := simplifiedchinese.GBK.NewEncoder().Bytes([]byte("编号,产品,数量\n001,苹果,3\n002,香蕉,2\n"))
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(before.FilePath, data, 0o600))
	model := &encodingSummaryChat{}
	svc.modelService = tableSummaryModelService{model: model}

	require.NoError(t, svc.Handle(context.Background(), task))
	prompt := strings.Join(model.prompts, "\n")
	for _, want := range []string{"编号", "产品", "苹果", "香蕉", "001", "002"} {
		require.Contains(t, prompt, want, "the model must receive decoded headers and sample cells")
	}
	chunks, err := f.chunkRepo.ListChunksByKnowledgeIDAndTypes(f.ctx, 7, "doc", []types.ChunkType{
		types.ChunkTypeTableSummary, types.ChunkTypeTableColumn,
	})
	require.NoError(t, err)
	require.Len(t, chunks, 2)
	require.Len(t, index.indexed, 2)
	for _, chunk := range chunks {
		require.Equal(t, int(types.ChunkStatusIndexed), chunk.Status)
	}
	after, err := f.svc.GetKnowledgeByID(f.ctx, "doc")
	require.NoError(t, err)
	require.Equal(t, before.ParseStatus, after.ParseStatus)
	require.Equal(t, before.SummaryStatus, after.SummaryStatus)
	original, err := os.ReadFile(before.FilePath)
	require.NoError(t, err)
	require.Equal(t, data, original)
}
