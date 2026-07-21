package onedrive

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/datasource"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func newConnectorTestRepo(t *testing.T) (*gorm.DB, *types.DataSource, *Connector) {
	t.Helper()
	t.Setenv("SYSTEM_AES_KEY", "0123456789abcdef0123456789abcdef")
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&types.DataSource{}, &types.DataSourceItem{}))
	ds := &types.DataSource{TenantID: 7, KnowledgeBaseID: "kb", Name: "drive", Type: types.ConnectorTypeOneDrive}
	require.NoError(t, db.Create(ds).Error)
	items := repository.NewDataSourceItemRepository(db)
	return db, ds, NewConnector(items)
}

func TestResourceRefRoundTrip(t *testing.T) {
	want := resourceRef{DriveID: "drive/with spaces", ItemID: "item:stable"}
	got, err := decodeRef(encodeRef(want))
	require.NoError(t, err)
	require.Equal(t, want, got)
}

func TestConnectorFullThenIncrementalDelta(t *testing.T) {
	_, ds, connector := newConnectorTestRepo(t)
	phase := "full"
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "Bearer token", r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/me/drive":
			fmt.Fprint(w, `{"id":"drive","name":"My Drive","webUrl":"https://example.test/drive","owner":{"user":{"id":"user","displayName":"User"}}}`)
		case "/drives/drive/root/delta":
			require.Equal(t, "latest", r.URL.Query().Get("token"))
			fmt.Fprintf(w, `{"value":[],"@odata.deltaLink":%q}`, server.URL+"/delta/start")
		case "/drives/drive/root/children":
			fmt.Fprintf(w, `{"value":[{"id":"folder","name":"Folder","folder":{"childCount":1},"parentReference":{"driveId":"drive","id":"root"}}],"@odata.nextLink":%q}`, server.URL+"/children/page2")
		case "/children/page2":
			fmt.Fprint(w, `{"value":[{"id":"root-file","name":"root.txt","size":4,"file":{"mimeType":"text/plain"},"parentReference":{"driveId":"drive","id":"root"}}]}`)
		case "/drives/drive/items/folder/children":
			fmt.Fprint(w, `{"value":[{"id":"nested","name":"nested.md","size":6,"file":{"mimeType":"text/markdown"},"parentReference":{"driveId":"drive","id":"folder"}}]}`)
		case "/drives/drive/items/root-file/content":
			fmt.Fprint(w, "root")
		case "/drives/drive/items/nested/content":
			if phase == "full" {
				fmt.Fprint(w, "nested")
			} else {
				fmt.Fprint(w, "changed")
			}
		case "/delta/start":
			fmt.Fprintf(w, `{"value":[],"@odata.deltaLink":%q}`, server.URL+"/delta/next")
		case "/delta/next":
			fmt.Fprintf(w, `{"value":[{"id":"nested","name":"nested.md","size":7,"file":{"mimeType":"text/markdown"},"parentReference":{"driveId":"drive","id":"folder"}},{"id":"root-file","deleted":{"state":"deleted"}}],"@odata.deltaLink":%q}`, server.URL+"/delta/final")
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	connector.graphBase = server.URL
	connector.httpClient = server.Client()

	root := encodeRef(resourceRef{DriveID: "drive", ItemID: rootItemID})
	config := &types.DataSourceConfig{
		Type: types.ConnectorTypeOneDrive, ResourceIDs: []string{root},
		Runtime: &types.DataSourceRuntime{
			DataSourceID: ds.ID, TenantID: ds.TenantID, ConnectionVersion: ds.ConnectionVersion,
			AccessToken: func(context.Context) (string, error) { return "token", nil },
		},
	}
	full, err := connector.FetchAllResult(context.Background(), config, config.ResourceIDs)
	require.NoError(t, err)
	require.Len(t, full.Items, 2)
	require.NotNil(t, full.NextCursor)

	for _, item := range full.Items {
		require.NoError(t, connector.items.SetIngested(context.Background(), ds.TenantID, ds.ID,
			ds.ConnectionVersion, item.Metadata["drive_id"], item.Metadata["item_id"], true))
	}
	phase = "incremental"
	incremental, err := connector.FetchIncrementalResult(context.Background(), config, full.NextCursor)
	require.NoError(t, err)
	require.Len(t, incremental.Items, 2)
	byID := make(map[string]types.FetchedItem)
	for _, item := range incremental.Items {
		byID[item.Metadata["item_id"]] = item
	}
	require.Equal(t, []byte("changed"), byID["nested"].Content)
	require.True(t, byID["root-file"].IsDeleted)
}

func TestConnectorSkipsUnsupportedAndOversizedFilesWithWarnings(t *testing.T) {
	_, ds, connector := newConnectorTestRepo(t)
	t.Setenv("MAX_FILE_SIZE_MB", "1")
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/me/drive":
			fmt.Fprint(w, `{"id":"drive","owner":{"user":{"id":"user"}}}`)
		case "/drives/drive/root/delta":
			fmt.Fprintf(w, `{"value":[],"@odata.deltaLink":%q}`, server.URL+"/delta")
		case "/drives/drive/root/children":
			fmt.Fprint(w, `{"value":[{"id":"bin","name":"raw.bin","size":10,"file":{},"parentReference":{"driveId":"drive","id":"root"}},{"id":"big","name":"big.pdf","size":2000000,"file":{},"parentReference":{"driveId":"drive","id":"root"}}]}`)
		case "/delta":
			fmt.Fprintf(w, `{"value":[],"@odata.deltaLink":%q}`, server.URL+"/delta-next")
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	connector.graphBase, connector.httpClient = server.URL, server.Client()
	root := encodeRef(resourceRef{DriveID: "drive", ItemID: rootItemID})
	result, err := connector.FetchAllResult(context.Background(), &types.DataSourceConfig{
		Type: types.ConnectorTypeOneDrive, ResourceIDs: []string{root},
		Runtime: &types.DataSourceRuntime{DataSourceID: ds.ID, TenantID: ds.TenantID, ConnectionVersion: 1,
			AccessToken: func(context.Context) (string, error) { return "token", nil }},
	}, []string{root})
	require.NoError(t, err)
	require.Empty(t, result.Items)
	require.Len(t, result.Warnings, 2)
}

func TestNormalizeRefsRemovesChildrenCoveredBySelectedParent(t *testing.T) {
	_, _, connector := newConnectorTestRepo(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/drives/drive/items/parent":
			fmt.Fprint(w, `{"id":"parent","folder":{},"parentReference":{"driveId":"drive","id":"root"}}`)
		case "/drives/drive/items/child":
			fmt.Fprint(w, `{"id":"child","file":{},"parentReference":{"driveId":"drive","id":"parent"}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client := newGraphClient(server.URL, server.Client(), func(context.Context) (string, error) {
		return "token", nil
	}, nil)
	parent := encodeRef(resourceRef{DriveID: "drive", ItemID: "parent"})
	child := encodeRef(resourceRef{DriveID: "drive", ItemID: "child"})
	refs, canonical, err := connector.normalizeRefs(context.Background(), client, []string{child, parent, child}, "drive")
	require.NoError(t, err)
	require.Equal(t, []resourceRef{{DriveID: "drive", ItemID: "parent"}}, refs)
	require.Equal(t, []string{parent}, canonical)
}

func TestGraphClientRefreshes401OnceAndFailsClosedOnSecond401(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer server.Close()
	refreshes := 0
	client := newGraphClient(server.URL, server.Client(), func(context.Context) (string, error) {
		return "expired", nil
	}, func(context.Context) (string, error) {
		refreshes++
		return "refreshed", nil
	})
	_, err := client.getDrive(context.Background())
	require.ErrorIs(t, err, datasource.ErrOAuthReauthorizationRequired)
	require.Equal(t, 2, requests)
	require.Equal(t, 1, refreshes)
}

func TestGraphPaginationCycleIsRejected(t *testing.T) {
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"value":[],"@odata.nextLink":%q}`, server.URL+"/page")
	}))
	defer server.Close()
	client := newGraphClient(server.URL, server.Client(), func(context.Context) (string, error) {
		return "token", nil
	}, nil)
	_, err := client.listAll(context.Background(), server.URL+"/page")
	require.ErrorContains(t, err, "cycle")
}

func TestExpiredDeltaCursorFallsBackToControlledFullScan(t *testing.T) {
	_, ds, connector := newConnectorTestRepo(t)
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/me/drive":
			fmt.Fprint(w, `{"id":"drive","owner":{"user":{"id":"user"}}}`)
		case "/expired":
			w.WriteHeader(http.StatusGone)
			fmt.Fprint(w, `{"error":{"code":"syncStateNotFound","message":"expired"}}`)
		case "/drives/drive/root/delta":
			require.Equal(t, "latest", r.URL.Query().Get("token"))
			fmt.Fprintf(w, `{"value":[],"@odata.deltaLink":%q}`, server.URL+"/catch-up")
		case "/drives/drive/root/children":
			fmt.Fprint(w, `{"value":[]}`)
		case "/catch-up":
			fmt.Fprintf(w, `{"value":[],"@odata.deltaLink":%q}`, server.URL+"/fresh")
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	connector.graphBase, connector.httpClient = server.URL, server.Client()
	root := encodeRef(resourceRef{DriveID: "drive", ItemID: rootItemID})
	config := &types.DataSourceConfig{
		Type: types.ConnectorTypeOneDrive, ResourceIDs: []string{root},
		Runtime: &types.DataSourceRuntime{DataSourceID: ds.ID, TenantID: ds.TenantID, ConnectionVersion: 1,
			AccessToken: func(context.Context) (string, error) { return "token", nil }},
	}
	cursor, err := buildCursor(server.URL+"/expired", selectionHash([]string{root}), 1)
	require.NoError(t, err)
	result, err := connector.FetchIncrementalResult(context.Background(), config, cursor)
	require.NoError(t, err)
	require.NotNil(t, result.NextCursor)
	state, err := parseCursor(result.NextCursor)
	require.NoError(t, err)
	require.Equal(t, server.URL+"/fresh", state.DeltaLink)
}
