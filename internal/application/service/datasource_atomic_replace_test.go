package service

import (
	"context"
	"errors"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

type atomicIngestRepo struct {
	interfaces.KnowledgeRepository
	row *types.Knowledge
	err error
}

func (r *atomicIngestRepo) FindByDataSourceExternalID(
	context.Context, uint64, string, string, string,
) (*types.Knowledge, error) {
	return r.row, r.err
}

func (r *atomicIngestRepo) HardDeleteKnowledge(context.Context, uint64, string) error {
	r.row = nil
	return nil
}

type atomicIngestService struct {
	sweepFakeKS
	replacements int
}

func (s *atomicIngestService) ReplaceKnowledgeFromDataSource(
	context.Context, string, *types.FetchedItem, map[string]string, []string, string,
) (*types.Knowledge, error) {
	s.replacements++
	return nil, s.createErr
}

func (s *atomicIngestService) CreateKnowledgeFromURL(
	context.Context, string, string, string, string, *bool, string, []string, string, *types.KnowledgeProcessOverrides,
) (*types.Knowledge, error) {
	s.events = append(s.events, "create-url")
	return nil, s.createErr
}

func TestIngestItemReplacementFailureKeepsPreviousKnowledge(t *testing.T) {
	for _, item := range []*types.FetchedItem{
		{ExternalID: "doc-1", Content: []byte("new content"), FileName: "doc.md"},
		{ExternalID: "doc-1", URL: "https://example.com/doc"},
	} {
		t.Run(item.FileName+item.URL, func(t *testing.T) {
			old := &types.Knowledge{ID: "old-knowledge", Type: "file"}
			repo := &atomicIngestRepo{row: old}
			ks := &atomicIngestService{sweepFakeKS: sweepFakeKS{repo: repo, createErr: errors.New("insert failed")}}
			s := &DataSourceService{knowledgeService: ks}
			ds := &types.DataSource{ID: "ds-1", TenantID: 7, KnowledgeBaseID: "kb-1"}

			_, err := s.ingestItem(context.Background(), ds, item, nil)
			require.Error(t, err)
			require.Same(t, old, repo.row, "a failed replacement must retain the previous row")
			require.Empty(t, ks.deleted, "old resources must not be deleted before replacement commits")
			require.Equal(t, 1, ks.replacements)
		})
	}
}

func TestIngestItemLookupFailureDoesNotCreateDuplicate(t *testing.T) {
	repo := &atomicIngestRepo{err: errors.New("lookup failed")}
	ks := &atomicIngestService{sweepFakeKS: sweepFakeKS{repo: repo}}
	s := &DataSourceService{knowledgeService: ks}
	_, err := s.ingestItem(context.Background(), &types.DataSource{ID: "ds-1"},
		&types.FetchedItem{ExternalID: "doc-1", Content: []byte("content"), FileName: "doc.md"}, nil)
	require.ErrorContains(t, err, "lookup failed")
	require.Empty(t, ks.events)
}
