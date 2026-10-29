package service

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type adoptionRepo struct {
	interfaces.KnowledgeRepository
	documents  []*types.Knowledge
	listErr    error
	updatedIDs []string
	updates    map[string]map[string]interface{}
}

func (r *adoptionRepo) ListKnowledgeByKnowledgeBaseID(
	context.Context, uint64, string,
) ([]*types.Knowledge, error) {
	return r.documents, r.listErr
}

func (r *adoptionRepo) UpdateKnowledgeColumns(
	_ context.Context, id string, values map[string]interface{},
) error {
	r.updatedIDs = append(r.updatedIDs, id)
	r.updates[id] = values
	return nil
}

type adoptionKnowledgeService struct {
	interfaces.KnowledgeService
	repo    *adoptionRepo
	content map[string]string
	readErr map[string]error
}

func (s *adoptionKnowledgeService) GetRepository() interfaces.KnowledgeRepository {
	return s.repo
}

func (s *adoptionKnowledgeService) GetKnowledgeFile(
	_ context.Context, id string,
) (io.ReadCloser, string, error) {
	if err := s.readErr[id]; err != nil {
		return nil, "", err
	}
	return io.NopCloser(strings.NewReader(s.content[id])), id + ".md", nil
}

type markerAdopter struct{}

func (markerAdopter) ExternalIDFromDocument(
	_ context.Context, _ *types.DataSourceConfig, _ string, content []byte,
) string {
	const marker = "飞书词条 ID："
	text := string(content)
	index := strings.Index(text, marker)
	if index < 0 {
		return ""
	}
	fields := strings.Fields(text[index+len(marker):])
	if len(fields) == 0 {
		return ""
	}
	return fields[0]
}

func doc(id string, metadata string, title string) *types.Knowledge {
	raw := types.JSON(metadata)
	if metadata == "" {
		raw = types.JSON(`{}`)
	}
	return &types.Knowledge{ID: id, Title: title, Metadata: raw}
}

func TestAdoptExistingKnowledgeBindsLegacyDocuments(t *testing.T) {
	repo := &adoptionRepo{
		documents: []*types.Knowledge{
			doc("d1", "", "OT"),
			doc("d2", "", "NOID"),
			doc("d3", `{"datasource_id":"other-ds","external_id":"x"}`, "OTHER"),
			doc("d4", "", "DUPLICATE"),
			doc("d5", `{"datasource_id":"ds-1","external_id":"e5"}`, "BOUND"),
		},
		updates: map[string]map[string]interface{}{},
	}
	ks := &adoptionKnowledgeService{
		repo: repo,
		content: map[string]string{
			"d1": "# OT\n\n飞书词条 ID：e1\n",
			"d2": "# NOID\n\nno marker here\n",
			"d3": "# OTHER\n\n飞书词条 ID：e3\n",
			"d4": "# DUPLICATE\n\n飞书词条 ID：e1\n",
			"d5": "# BOUND\n\n飞书词条 ID：e5\n",
		},
	}
	svc := &DataSourceService{knowledgeService: ks}
	ds := &types.DataSource{ID: "ds-1", TenantID: 7, KnowledgeBaseID: "kb-1"}

	adopted, err := svc.adoptExistingKnowledge(context.Background(), ds, markerAdopter{}, &types.DataSourceConfig{})
	require.NoError(t, err)
	assert.Equal(t, 1, adopted)
	assert.Equal(t, []string{"d1"}, repo.updatedIDs)

	raw, ok := repo.updates["d1"]["metadata"].(types.JSON)
	require.True(t, ok, "metadata update must be types.JSON, got %T", repo.updates["d1"]["metadata"])
	var metadata map[string]string
	require.NoError(t, json.Unmarshal(raw, &metadata))
	assert.Equal(t, "ds-1", metadata["datasource_id"])
	assert.Equal(t, "e1", metadata["external_id"])
	assert.Equal(t, "true", metadata["external_id_adopted"])
}

func TestAdoptExistingKnowledgeToleratesUnreadableDocument(t *testing.T) {
	repo := &adoptionRepo{
		documents: []*types.Knowledge{
			doc("broken", "", "BROKEN"),
			doc("ok", "", "OT"),
		},
		updates: map[string]map[string]interface{}{},
	}
	ks := &adoptionKnowledgeService{
		repo:    repo,
		content: map[string]string{"ok": "飞书词条 ID：e-ok"},
		readErr: map[string]error{"broken": errors.New("no file")},
	}
	svc := &DataSourceService{knowledgeService: ks}

	adopted, err := svc.adoptExistingKnowledge(
		context.Background(),
		&types.DataSource{ID: "ds-1", TenantID: 7, KnowledgeBaseID: "kb-1"},
		markerAdopter{}, &types.DataSourceConfig{})
	require.NoError(t, err)
	assert.Equal(t, 1, adopted)
	assert.Equal(t, []string{"ok"}, repo.updatedIDs)
}

func TestAdoptExistingKnowledgeFailsWhenListingFails(t *testing.T) {
	repo := &adoptionRepo{listErr: errors.New("db down"), updates: map[string]map[string]interface{}{}}
	svc := &DataSourceService{knowledgeService: &adoptionKnowledgeService{repo: repo}}

	_, err := svc.adoptExistingKnowledge(
		context.Background(),
		&types.DataSource{ID: "ds-1", TenantID: 7, KnowledgeBaseID: "kb-1"},
		markerAdopter{}, &types.DataSourceConfig{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "list knowledge for adoption")
}
