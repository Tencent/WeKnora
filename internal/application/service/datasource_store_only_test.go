package service

import (
	"context"
	"mime/multipart"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

// routingKnowledgeService records which create entry point ingestion chose for
// an item. Both methods are overridden, so a routing mistake is visible as the
// wrong counter moving rather than as a silent default.
type routingKnowledgeService struct {
	interfaces.KnowledgeService

	parsed   []string
	archived []string
}

func (k *routingKnowledgeService) CreateKnowledgeFromFile(
	_ context.Context, _ string, _ *multipart.FileHeader, _ map[string]string,
	_ *bool, customFileName string, _ []string, _ string, _ *types.KnowledgeProcessOverrides,
) (*types.Knowledge, error) {
	k.parsed = append(k.parsed, customFileName)
	return &types.Knowledge{ID: "parsed-" + customFileName, ParseStatus: types.ParseStatusPending}, nil
}

func (k *routingKnowledgeService) CreateStoredKnowledgeFromFile(
	_ context.Context, _ string, _ *multipart.FileHeader, _ map[string]string,
	customFileName string, _ []string, _ string,
) (*types.Knowledge, error) {
	k.archived = append(k.archived, customFileName)
	return &types.Knowledge{ID: "stored-" + customFileName, ParseStatus: types.ParseStatusStored}, nil
}

func newRoutingDataSource() *types.DataSource {
	return &types.DataSource{ID: "ds-1", TenantID: 1, KnowledgeBaseID: "kb-1", Type: "dingtalk"}
}

// An item a connector marked StoreWithoutIndexing must take the archive path:
// that is the whole point of the flag, and it is the only route that creates a
// "stored" row without a parse task.
func TestIngestItemRoutesStoreOnlyItemsToTheArchivePath(t *testing.T) {
	t.Parallel()

	ks := &routingKnowledgeService{}
	svc := &DataSourceService{knowledgeService: ks}
	item := &types.FetchedItem{
		Title:                "Lesson.mp4",
		Content:              []byte("ftypmp42"),
		FileName:             "Lesson.mp4",
		StoreWithoutIndexing: true,
		Metadata:             map[string]string{"channel": types.ChannelDingtalk},
	}

	_, err := svc.ingestItem(
		context.Background(), newRoutingDataSource(), item, nil,
	)

	require.NoError(t, err)
	require.Equal(t, []string{"Lesson.mp4"}, ks.archived)
	require.Empty(t, ks.parsed, "a store-only item must not be handed to the parsing path")
}

// The flag is what decides: the same bytes and file name without it keep the
// normal pipeline, so nothing changes for connectors that never set it.
func TestIngestItemKeepsTheParsingPathWhenTheFlagIsOff(t *testing.T) {
	t.Parallel()

	ks := &routingKnowledgeService{}
	svc := &DataSourceService{knowledgeService: ks}
	item := &types.FetchedItem{
		Title:    "Report.pdf",
		Content:  []byte("%PDF-1.7"),
		FileName: "Report.pdf",
		Metadata: map[string]string{"channel": types.ChannelDingtalk},
	}

	_, err := svc.ingestItem(
		context.Background(), newRoutingDataSource(), item, nil,
	)

	require.NoError(t, err)
	require.Equal(t, []string{"Report.pdf"}, ks.parsed)
	require.Empty(t, ks.archived)
}
