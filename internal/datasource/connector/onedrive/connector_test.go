//nolint:errcheck,lll // Test handlers write compact JSON fixtures to in-memory response recorders.
package onedrive

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

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
	return db, ds, NewConnector(items, nil)
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
		Runtime: &types.DataSourceRuntime{
			DataSourceID: ds.ID, TenantID: ds.TenantID, ConnectionVersion: 1,
			AccessToken: func(context.Context) (string, error) { return "token", nil },
		},
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
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
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
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
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
		Runtime: &types.DataSourceRuntime{
			DataSourceID: ds.ID, TenantID: ds.TenantID, ConnectionVersion: 1,
			AccessToken: func(context.Context) (string, error) { return "token", nil },
		},
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

func TestConnectorSingleFileSelection(t *testing.T) {
	_, ds, connector := newConnectorTestRepo(t)
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/me/drive":
			fmt.Fprint(w, `{"id":"drive","owner":{"user":{"id":"user"}}}`)
		case "/drives/drive/items/file":
			fmt.Fprint(w, `{"id":"file","name":"only.md","size":4,"file":{"mimeType":"text/markdown"},"parentReference":{"driveId":"drive","id":"root"}}`)
		case "/drives/drive/root/delta":
			fmt.Fprintf(w, `{"value":[],"@odata.deltaLink":%q}`, server.URL+"/delta")
		case "/drives/drive/items/file/content":
			fmt.Fprint(w, "only")
		case "/delta":
			fmt.Fprintf(w, `{"value":[],"@odata.deltaLink":%q}`, server.URL+"/next")
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	connector.graphBase, connector.httpClient = server.URL, server.Client()
	selected := encodeRef(resourceRef{DriveID: "drive", ItemID: "file"})
	result, err := connector.FetchAllResult(context.Background(), &types.DataSourceConfig{
		Type: types.ConnectorTypeOneDrive, ResourceIDs: []string{selected},
		Runtime: &types.DataSourceRuntime{
			DataSourceID: ds.ID, TenantID: ds.TenantID, ConnectionVersion: ds.ConnectionVersion,
			AccessToken: func(context.Context) (string, error) { return "token", nil },
		},
	}, []string{selected})
	require.NoError(t, err)
	require.Len(t, result.Items, 1)
	require.Equal(t, "file", result.Items[0].Metadata["item_id"])
	require.Equal(t, []byte("only"), result.Items[0].Content)
}

func TestConnectorFolderMoveOutDeletesIndexedDescendants(t *testing.T) {
	_, ds, connector := newConnectorTestRepo(t)
	selected := encodeRef(resourceRef{DriveID: "drive", ItemID: "selected"})
	for _, item := range []*types.DataSourceItem{
		{
			TenantID: ds.TenantID, DataSourceID: ds.ID, ConnectionVersion: ds.ConnectionVersion,
			DriveID: "drive", ItemID: "selected", ParentItemID: rootItemID, ItemType: "folder",
			SelectedRootID: selected, ExternalID: externalID("drive", "selected"),
		},
		{
			TenantID: ds.TenantID, DataSourceID: ds.ID, ConnectionVersion: ds.ConnectionVersion,
			DriveID: "drive", ItemID: "moved", ParentItemID: "selected", ItemType: "folder",
			SelectedRootID: selected, ExternalID: externalID("drive", "moved"),
		},
		{
			TenantID: ds.TenantID, DataSourceID: ds.ID, ConnectionVersion: ds.ConnectionVersion,
			DriveID: "drive", ItemID: "child", ParentItemID: "moved", ItemType: "file",
			SelectedRootID: selected, ExternalID: externalID("drive", "child"), Ingested: true,
		},
	} {
		require.NoError(t, connector.items.Upsert(context.Background(), item))
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/drives/drive/items/outside" {
			fmt.Fprint(w, `{"id":"outside","folder":{},"parentReference":{"driveId":"drive","id":"root"}}`)
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()
	client := newGraphClient(server.URL, server.Client(), func(context.Context) (string, error) { return "token", nil }, nil)
	config := &types.DataSourceConfig{Runtime: &types.DataSourceRuntime{
		DataSourceID: ds.ID, TenantID: ds.TenantID, ConnectionVersion: ds.ConnectionVersion,
	}}
	result := &types.FetchResult{}
	change := driveItem{ID: "moved", Name: "Moved"}
	change.Folder = &struct {
		ChildCount int `json:"childCount"`
	}{}
	change.ParentReference.DriveID = "drive"
	change.ParentReference.ID = "outside"
	err := connector.applyChanges(context.Background(), client, config, "drive",
		[]resourceRef{{DriveID: "drive", ItemID: "selected"}}, "",
		[]driveItem{change}, result)
	require.NoError(t, err)
	require.Len(t, result.Items, 1)
	require.True(t, result.Items[0].IsDeleted)
	require.Equal(t, "child", result.Items[0].Metadata["item_id"])
}

func TestConnectorFolderMoveIntoSelectionWalksDescendants(t *testing.T) {
	_, ds, connector := newConnectorTestRepo(t)
	selected := encodeRef(resourceRef{DriveID: "drive", ItemID: "selected"})
	require.NoError(t, connector.items.Upsert(context.Background(), &types.DataSourceItem{
		TenantID: ds.TenantID, DataSourceID: ds.ID, ConnectionVersion: ds.ConnectionVersion,
		DriveID: "drive", ItemID: "selected", ParentItemID: rootItemID, ItemType: "folder",
		SelectedRootID: selected, ExternalID: externalID("drive", "selected"),
	}))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/drives/drive/items/moved/children":
			fmt.Fprint(w, `{"value":[{"id":"child","name":"child.txt","size":5,"file":{"mimeType":"text/plain"},"parentReference":{"driveId":"drive","id":"moved"}}]}`)
		case "/drives/drive/items/child/content":
			fmt.Fprint(w, "child")
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client := newGraphClient(server.URL, server.Client(), func(context.Context) (string, error) { return "token", nil }, nil)
	config := &types.DataSourceConfig{Runtime: &types.DataSourceRuntime{
		DataSourceID: ds.ID, TenantID: ds.TenantID, ConnectionVersion: ds.ConnectionVersion,
	}}
	result := &types.FetchResult{}
	change := driveItem{ID: "moved", Name: "Moved"}
	change.Folder = &struct {
		ChildCount int `json:"childCount"`
	}{}
	change.ParentReference.DriveID = "drive"
	change.ParentReference.ID = "selected"
	err := connector.applyChanges(context.Background(), client, config, "drive",
		[]resourceRef{{DriveID: "drive", ItemID: "selected"}}, "generation",
		[]driveItem{change}, result)
	require.NoError(t, err)
	require.Len(t, result.Items, 1)
	require.Equal(t, "child", result.Items[0].Metadata["item_id"])
	require.Equal(t, []byte("child"), result.Items[0].Content)
}

func TestGraphDownloadFollowsRedirectAndEnforcesStreamingLimit(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/drives/drive/items/file/content":
			http.Redirect(w, r, "/download", http.StatusFound)
		case "/download":
			w.Header().Set("Content-Length", "")
			_, _ = io.WriteString(w, strings.Repeat("x", 9))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client := newGraphClient(server.URL, server.Client(), func(context.Context) (string, error) { return "token", nil }, nil)
	_, err := client.download(context.Background(), "drive", "file", 8)
	require.ErrorContains(t, err, "exceeds maximum size")
}

func TestDecodeGraphErrorSanitizesOpaqueURLAndParsesRetryAfter(t *testing.T) {
	response := &http.Response{
		StatusCode: http.StatusTooManyRequests,
		Header:     http.Header{"Retry-After": []string{"3"}},
		Body: io.NopCloser(strings.NewReader(
			`{"error":{"code":"throttled","message":"retry https://graph.test/delta?$deltatoken=secret"}}`,
		)),
	}
	err := decodeGraphError(response)
	require.Equal(t, 3*time.Second, err.RetryAfter)
	require.Equal(t, "request rejected by Microsoft Graph", err.Message)
	require.NotContains(t, err.Error(), "secret")
}

func TestGraphPaginationRejectsCrossOriginNextLinkWithoutSendingToken(t *testing.T) {
	attackerRequests := 0
	attacker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		attackerRequests++
		w.WriteHeader(http.StatusNoContent)
	}))
	defer attacker.Close()

	graph := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"value":[],"@odata.nextLink":%q}`, attacker.URL+"/steal")
	}))
	defer graph.Close()
	client := newGraphClient(graph.URL, graph.Client(), func(context.Context) (string, error) {
		return "secret-token", nil
	}, nil)

	_, err := client.listAll(context.Background(), graph.URL+"/page")
	require.ErrorContains(t, err, "changed origin")
	require.Zero(t, attackerRequests)
}

func TestFullWalkPreservesExistingIngestedStateUntilReplacementSucceeds(t *testing.T) {
	_, ds, connector := newConnectorTestRepo(t)
	require.NoError(t, connector.items.Upsert(context.Background(), &types.DataSourceItem{
		TenantID: ds.TenantID, DataSourceID: ds.ID, ConnectionVersion: ds.ConnectionVersion,
		DriveID: "drive", ItemID: "file", ParentItemID: rootItemID, ItemType: "file",
		SelectedRootID: "selected", ExternalID: externalID("drive", "file"), Ingested: true,
	}))
	config := &types.DataSourceConfig{Runtime: &types.DataSourceRuntime{
		TenantID: ds.TenantID, DataSourceID: ds.ID, ConnectionVersion: ds.ConnectionVersion,
	}}
	item := &driveItem{ID: "file", Name: "unsupported.bin", File: &struct {
		MimeType string `json:"mimeType"`
	}{}}
	item.ParentReference.ID = rootItemID
	result := &types.FetchResult{}

	err := connector.walk(context.Background(), nil, config, "drive", item, "selected", "generation", result)
	require.NoError(t, err)
	require.Len(t, result.Warnings, 1)
	indexed, err := connector.items.Find(
		context.Background(), ds.TenantID, ds.ID, ds.ConnectionVersion, "drive", "file",
	)
	require.NoError(t, err)
	require.True(t, indexed.Ingested)
}

func TestFolderDeletionDoesNotDeleteChildReparentedInSameDeltaBatch(t *testing.T) {
	_, ds, connector := newConnectorTestRepo(t)
	deletedRoot := encodeRef(resourceRef{DriveID: "drive", ItemID: "deleted-folder"})
	survivingRoot := encodeRef(resourceRef{DriveID: "drive", ItemID: "surviving-folder"})
	for _, item := range []*types.DataSourceItem{
		{
			TenantID: ds.TenantID, DataSourceID: ds.ID, ConnectionVersion: ds.ConnectionVersion,
			DriveID: "drive", ItemID: "deleted-folder", ParentItemID: rootItemID,
			ItemType: "folder", SelectedRootID: deletedRoot,
		},
		{
			TenantID: ds.TenantID, DataSourceID: ds.ID, ConnectionVersion: ds.ConnectionVersion,
			DriveID: "drive", ItemID: "child", ParentItemID: "deleted-folder", ItemType: "file",
			SelectedRootID: deletedRoot, ExternalID: externalID("drive", "child"), Ingested: true,
		},
	} {
		require.NoError(t, connector.items.Upsert(context.Background(), item))
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/drives/drive/items/child/content" {
			fmt.Fprint(w, "updated")
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()
	client := newGraphClient(server.URL, server.Client(), func(context.Context) (string, error) {
		return "token", nil
	}, nil)
	config := &types.DataSourceConfig{Runtime: &types.DataSourceRuntime{
		TenantID: ds.TenantID, DataSourceID: ds.ID, ConnectionVersion: ds.ConnectionVersion,
	}}
	deleted := driveItem{ID: "deleted-folder", Deleted: &struct {
		State string `json:"state"`
	}{State: "deleted"}}
	moved := driveItem{ID: "child", Name: "child.txt", Size: 7, File: &struct {
		MimeType string `json:"mimeType"`
	}{MimeType: "text/plain"}}
	moved.ParentReference.DriveID = "drive"
	moved.ParentReference.ID = "surviving-folder"
	result := &types.FetchResult{}

	err := connector.applyChanges(
		context.Background(), client, config, "drive",
		[]resourceRef{
			{DriveID: "drive", ItemID: "deleted-folder"},
			{DriveID: "drive", ItemID: "surviving-folder"},
		},
		"generation", []driveItem{deleted, moved}, result,
	)
	require.NoError(t, err)
	require.Len(t, result.Items, 1)
	require.False(t, result.Items[0].IsDeleted)
	require.Equal(t, []byte("updated"), result.Items[0].Content)
	indexedRoot, err := connector.items.Find(
		context.Background(), ds.TenantID, ds.ID, ds.ConnectionVersion, "drive", "deleted-folder",
	)
	require.NoError(t, err)
	require.NotNil(t, indexedRoot.DeletedAt)
	indexedChild, err := connector.items.Find(
		context.Background(), ds.TenantID, ds.ID, ds.ConnectionVersion, "drive", "child",
	)
	require.NoError(t, err)
	require.Nil(t, indexedChild.DeletedAt)
	require.Equal(t, survivingRoot, indexedChild.SelectedRootID)
}
