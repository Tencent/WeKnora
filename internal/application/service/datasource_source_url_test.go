package service

import (
	"context"
	"mime/multipart"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

type sourceURLCaptureKnowledgeService struct {
	*sweepFakeKS
	metadata map[string]string
}

func (s *sourceURLCaptureKnowledgeService) CreateKnowledgeFromFile(_ context.Context, _ string, _ *multipart.FileHeader,
	metadata map[string]string, _ *bool, _ string, _ []string, _ string, _ *types.KnowledgeProcessOverrides,
) (*types.Knowledge, error) {
	s.metadata = metadata
	return &types.Knowledge{ID: "created"}, nil
}

func TestIngestItemPersistsOriginalSourceURLForDownloadedContent(t *testing.T) {
	for _, tc := range []struct {
		name     string
		url      string
		metadata map[string]string
		want     string
	}{
		{
			"connector URL overrides metadata",
			" https://tenant.feishu.cn/wiki/Node?from=share ",
			map[string]string{
				"source_url": "https://docs.example.test/stale",
				"sourceUrl":  "https://docs.example.test/alias",
			},
			"https://tenant.feishu.cn/wiki/Node?from=share",
		},
		{
			"safe metadata survives unavailable URL",
			"",
			map[string]string{"source_url": "https://docs.example.test/original"},
			"https://docs.example.test/original",
		},
		{
			"credential links are discarded",
			"https://files.example.test/file?X-Amz-Signature=secret",
			map[string]string{
				"source_url": "resource://internal",
				"sourceUrl":  "https://files.example.test/doc?access_token=secret",
			},
			"",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			knowledgeService := &sourceURLCaptureKnowledgeService{
				sweepFakeKS: &sweepFakeKS{repo: &deletionLookupKnowledgeRepo{}},
			}
			service := &DataSourceService{knowledgeService: knowledgeService}
			_, err := service.ingestItem(context.Background(),
				&types.DataSource{
					ID:              "source",
					TenantID:        1,
					KnowledgeBaseID: "kb",
					Type:            types.ConnectorTypeFeishu,
				},
				&types.FetchedItem{
					ExternalID: "node",
					URL:        tc.url,
					Metadata:   tc.metadata,
					FileName:   "doc.md",
					Content:    []byte("document"),
				},
				nil)
			require.NoError(t, err)
			require.Equal(t, tc.want, knowledgeService.metadata["source_url"])
			if tc.want == "" {
				require.NotContains(t, knowledgeService.metadata, "source_url")
				require.NotContains(t, knowledgeService.metadata, "sourceUrl")
			}
		})
	}
}
