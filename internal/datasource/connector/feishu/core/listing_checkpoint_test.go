package core

import (
	"context"
	"errors"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

type testListingPages map[string][]byte

func (s testListingPages) Load(_ context.Context, key string) ([]byte, error) { return s[key], nil }
func (s testListingPages) Save(_ context.Context, key string, page []byte) error {
	s[key] = page
	return nil
}

func TestListingResumesAfterSecondPageFailure(t *testing.T) {
	for _, wiki := range []bool{true, false} {
		path := "/open-apis/drive/v1/files"
		if wiki {
			path = "/open-apis/wiki/v2/spaces/root/nodes"
		}
		t.Run(path, func(t *testing.T) {
			t.Setenv("FEISHU_API_REQUESTS_PER_MINUTE", "600000")
			var first, second atomic.Int32
			var deny atomic.Bool
			ts, cfg := retryTestServer(t, path, func(w http.ResponseWriter, r *http.Request) {
				page := r.URL.Query().Get("page_token")
				more := page == ""
				if more {
					first.Add(1)
				} else if second.Add(1) == 1 {
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				if deny.Load() {
					w.WriteHeader(http.StatusForbidden)
					return
				}
				token := "b"
				if more {
					token = "a"
				}
				if wiki {
					writeJSON(w, WikiNodeListResponse{Data: WikiNodeListData{
						Items: []WikiNode{{NodeToken: token}}, PageToken: "next", HasMore: more,
					}})
				} else {
					writeJSON(w, DriveFileListResponse{Data: DriveFileListData{
						Files: []DriveFile{{Token: token}}, NextPageToken: "next", HasMore: more,
					}})
				}
			})
			defer ts.Close()
			store := testListingPages{}
			run := func() ([]string, error) {
				ctx, _ := startListing(WithListingPageStore(t.Context(), store), "root")
				client := NewClient(cfg)
				if wiki {
					nodes, err := client.ListWikiNodes(ctx, "root", "")
					return wikiNodeTokens(nodes), err
				}
				files, err := client.ListDriveFilesAllPages(ctx, "root")
				var names []string
				for _, f := range files {
					names = append(names, f.Token)
				}
				return names, err
			}
			_, err := run()
			require.Error(t, err)
			require.Len(t, store, 1)
			got, err := run()
			require.NoError(t, err)
			require.Equal(t, []string{"a", "b"}, got)
			require.EqualValues(t, 1, first.Load())
			require.EqualValues(t, 2, second.Load())
			deny.Store(true)
			got, err = run()
			require.Error(t, err)
			require.Empty(t, got)
		})
	}
}

type interruptedListingOps struct{ fullSyncOps }

func (*interruptedListingOps) List(ctx context.Context, _ *Client, _ string) ([]string, error, error) {
	nodes, err := listPages(ctx, []string{"wiki"}, "wiki", func(token string) (listingPage[string], error) {
		if token == "" {
			return listingPage[string]{Items: []string{"one"}, More: true, Next: "two"}, nil
		}
		return listingPage[string]{}, errors.New("offline")
	})
	// Some connector paths ignore a parent listing failure.
	return nodes, err, nil
}
func TestInterruptedListingPreservesCursorAndItems(t *testing.T) {
	ctx := WithListingPageStore(t.Context(), testListingPages{})
	ops := &interruptedListingOps{}
	cur := ops.EncodeCursor(map[string]map[string]string{"root": {"old": "1"}}, time.Now())
	h := &CollectHandler{}
	next, err := FetchStreamEngine(ctx, nil, &types.DataSourceConfig{ResourceIDs: []string{"root"}}, cur, h, ops)
	require.Error(t, err)
	require.Nil(t, next)
	require.Empty(t, h.items)
}

type resumedListingOps struct{ fullSyncOps }

func (*resumedListingOps) List(ctx context.Context, _ *Client, _ string) ([]string, error, error) {
	items, err := listPages(ctx, []string{"drive"}, "drive", func(token string) (listingPage[string], error) {
		if token == "" {
			return listingPage[string]{Items: []string{"a"}, More: true, Next: "next"}, nil
		}
		return listingPage[string]{Items: []string{"b"}}, nil
	})
	return items, nil, err
}

func TestRestoredListingDefersMissingItemDeletion(t *testing.T) {
	store := testListingPages{}
	ops := &resumedListingOps{}
	ctx := WithListingPageStore(t.Context(), store)
	cfg := &types.DataSourceConfig{ResourceIDs: []string{"root"}}
	listingCtx, _ := startListing(ctx, "root")
	_, _, err := ops.List(listingCtx, nil, "root")
	require.NoError(t, err)
	cur := ops.EncodeCursor(map[string]map[string]string{"root": {"old": "100"}}, time.Now())
	handler := &CollectHandler{}
	next, err := FetchStreamEngine(ctx, nil, cfg, cur, handler, ops)
	require.NoError(t, err)
	require.Len(t, handler.items, 2)
	for _, item := range handler.items {
		require.False(t, item.IsDeleted)
	}
	require.Contains(t, ops.DecodeCursorTimes(next.ConnectorCursor)["root"], "old")
	handler = &CollectHandler{}
	_, err = FetchStreamEngine(t.Context(), nil, cfg, next, handler, ops)
	require.NoError(t, err)
	require.Len(t, handler.items, 1)
	require.True(t, handler.items[0].IsDeleted)
	require.Equal(t, "old", handler.items[0].ExternalID)
}
