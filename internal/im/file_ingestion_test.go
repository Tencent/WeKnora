package im

import (
	"context"
	"io"
	"mime/multipart"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

type fileIngestionTenantService struct {
	interfaces.TenantService
}

func (*fileIngestionTenantService) GetTenantByID(_ context.Context, id uint64) (*types.Tenant, error) {
	return &types.Tenant{ID: id}, nil
}

type fileIngestionKnowledgeService struct {
	interfaces.KnowledgeService
	t       *testing.T
	want    string
	content []byte
	calls   int
}

func (s *fileIngestionKnowledgeService) CreateKnowledgeFromFile(
	ctx context.Context, kbID string, file *multipart.FileHeader,
	_ map[string]string, _ *bool, _ string, _ []string, channel string, _ *types.KnowledgeProcessOverrides,
) (*types.Knowledge, error) {
	s.calls++
	require.Equal(s.t, uint64(7), ctx.Value(types.TenantIDContextKey))
	require.Equal(s.t, uint64(7), ctx.Value(types.TenantInfoContextKey).(*types.Tenant).ID)
	require.Equal(s.t, "inbox", kbID)
	require.Equal(s.t, types.ChannelWechat, channel)
	require.Equal(s.t, s.want, file.Filename)

	reader, err := file.Open()
	require.NoError(s.t, err)
	defer func() { require.NoError(s.t, reader.Close()) }()
	content, err := io.ReadAll(reader)
	require.NoError(s.t, err)
	require.Equal(s.t, s.content, content)
	return &types.Knowledge{ID: "document"}, nil
}

func TestProcessDownloadedFileToKnowledgeBaseFileTypes(t *testing.T) {
	tests := []struct {
		fileName string
		want     int
	}{
		{"index.html", 1},
		{"index.htm", 1},
		{"index.mhtml", 1},
		{"INDEX.HTML", 1},
		{"report.pdf", 1},
		{"report.xlsx", 1},
		{"report.xlsm", 1},
		{"photo.webp", 1},
		{"notes.json", 1},
		{"book.epub", 1},
		{"recording.mp3", 1},
		{"program.exe", 0},
		{"report.pdf.exe", 0},
		{"video.mp4", 0},
		{"no-extension", 0},
	}
	for _, tt := range tests {
		t.Run(tt.fileName, func(t *testing.T) {
			content := []byte("attachment bytes")
			knowledge := &fileIngestionKnowledgeService{t: t, want: tt.fileName, content: content}
			service := &Service{
				tenantService:    &fileIngestionTenantService{},
				knowledgeService: knowledge,
			}
			service.processDownloadedFileToKnowledgeBase(context.Background(), &IMChannel{
				TenantID: 7, KnowledgeBaseID: "inbox", Platform: "wechat",
			}, &imDownloadedAttachment{fileName: tt.fileName, content: content})
			require.Equal(t, tt.want, knowledge.calls, "knowledge ingestion calls for %s", tt.fileName)
		})
	}
}
