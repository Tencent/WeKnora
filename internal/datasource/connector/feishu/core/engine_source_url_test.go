package core

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

type failedSourceOps struct{ NodeOps[string] }

func (failedSourceOps) List(context.Context, *Client, string) ([]string, error, error) {
	return []string{"document"}, nil, nil
}
func (failedSourceOps) Token(n string) string  { return n }
func (failedSourceOps) Title(string) string    { return "Document" }
func (failedSourceOps) EditTime(string) string { return "new" }
func (failedSourceOps) LogTag() string         { return "test" }
func (failedSourceOps) Fetch(context.Context, *Client, string, string) ([]*types.FetchedItem, error) {
	return nil, errors.New("download failed")
}

func (failedSourceOps) EncodeCursor(times map[string]map[string]string, _ time.Time) *types.SyncCursor {
	return &types.SyncCursor{ConnectorCursor: map[string]interface{}{"times": times}}
}

type linkedFailedSourceOps struct {
	failedSourceOps
	url string
}

func (o linkedFailedSourceOps) NodeURL(string) string { return o.url }

func TestFailedFetchPreservesOptionalSourceURL(t *testing.T) {
	for _, url := range []string{"", "https://feishu.cn/wiki/document", "https://larksuite.com/wiki/document"} {
		t.Run(url, func(t *testing.T) {
			var ops NodeOps[string] = failedSourceOps{}
			if url != "" {
				ops = linkedFailedSourceOps{url: url}
			}
			handler := &CollectHandler{}
			cursor, err := FetchStreamEngine(
				context.Background(),
				nil,
				&types.DataSourceConfig{ResourceIDs: []string{"space"}},
				nil,
				handler,
				ops,
			)
			require.NoError(t, err)
			require.Len(t, handler.items, 1)
			require.Equal(t, url, handler.items[0].URL)
			require.Equal(t, "document", handler.items[0].ExternalID)
			require.Equal(t, "space", handler.items[0].SourceResourceID)
			require.NotEmpty(t, handler.items[0].Metadata)
			require.Empty(t,
				cursor.ConnectorCursor["times"].(map[string]map[string]string)["space"],
				"failed fetch must remain retryable")
		})
	}
}
