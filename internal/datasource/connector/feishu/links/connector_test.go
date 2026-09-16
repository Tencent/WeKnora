package links

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/datasource/connector/feishu/core"
	"github.com/Tencent/WeKnora/internal/types"
)

type fakeAPI struct {
	nodes         map[string]core.WikiNode
	forbidden     map[string]bool
	metas         []core.DriveDocMeta
	failed        []core.DriveMetaFailedItem
	listSpacesHit int
	listFilesHit  int
	getNodeHit    int
	blocksMode    string
	docToken      string
}

func startFake(t *testing.T, api *fakeAPI) (*httptest.Server, *core.Config) {
	t.Helper()
	if api.nodes == nil {
		api.nodes = map[string]core.WikiNode{}
	}
	if api.forbidden == nil {
		api.forbidden = map[string]bool{}
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/open-apis/auth/v3/tenant_access_token/internal", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, core.TokenResponse{ApiResponse: core.ApiResponse{Code: 0}, TenantAccessToken: "fake-token", Expire: 7200})
	})
	mux.HandleFunc("GET /open-apis/wiki/v2/spaces", func(w http.ResponseWriter, r *http.Request) {
		api.listSpacesHit++
		writeJSON(w, core.WikiSpaceListResponse{ApiResponse: core.ApiResponse{Code: 0}})
	})
	mux.HandleFunc("GET /open-apis/wiki/v2/spaces/get_node", func(w http.ResponseWriter, r *http.Request) {
		api.getNodeHit++
		token := r.URL.Query().Get("token")
		if api.forbidden[token] {
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"code":1061004,"msg":"forbidden"}`))
			return
		}
		node, ok := api.nodes[token]
		if !ok {
			writeJSON(w, core.WikiNodeInfoResponse{ApiResponse: core.ApiResponse{Code: 131006, Msg: "not found"}})
			return
		}
		writeJSON(w, core.WikiNodeInfoResponse{
			ApiResponse: core.ApiResponse{Code: 0},
			Data:        core.WikiNodeInfoData{Node: node},
		})
	})
	mux.HandleFunc("/open-apis/drive/v1/metas/batch_query", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, map[string]interface{}{
			"code": 0,
			"data": map[string]interface{}{
				"metas":       api.metas,
				"failed_list": api.failed,
			},
		})
	})
	mux.HandleFunc("/open-apis/drive/v1/files", func(w http.ResponseWriter, r *http.Request) {
		api.listFilesHit++
		http.NotFound(w, r)
	})

	if api.docToken != "" {
		blocksPath := "/open-apis/docx/v1/documents/" + api.docToken + "/blocks"
		mux.HandleFunc(blocksPath, func(w http.ResponseWriter, _ *http.Request) {
			if api.blocksMode == "fail" {
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = w.Write([]byte(`{"code":1,"msg":"fail"}`))
				return
			}
			writeJSON(w, core.DocxBlocksResponse{
				ApiResponse: core.ApiResponse{Code: 0},
				Data: core.DocxBlocksData{
					Items: []core.DocxBlock{
						{BlockID: "p", BlockType: core.BlockTypePage},
						{BlockID: "t", BlockType: core.BlockTypeText, Text: &core.BlockText{
							Elements: []core.TextElement{{TextRun: &core.TextRun{Content: "hello from blocks"}}},
						}},
					},
				},
			})
		})
	}

	mux.HandleFunc("/open-apis/drive/v1/export_tasks", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			writeJSON(w, core.ExportTaskCreateResponse{
				ApiResponse: core.ApiResponse{Code: 0},
				Data:        core.ExportTaskCreateData{Ticket: "ticket-links"},
			})
			return
		}
		http.NotFound(w, r)
	})
	mux.HandleFunc("/open-apis/drive/v1/export_tasks/ticket-links", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, core.ExportTaskStatusResponse{
			ApiResponse: core.ApiResponse{Code: 0},
			Data: core.ExportTaskStatusData{
				Result: core.ExportTaskResult{FileToken: "ft-links", FileSize: 10, JobStatus: 0, FileName: "doc.docx"},
			},
		})
	})
	mux.HandleFunc("/open-apis/drive/v1/export_tasks/file/ft-links/download", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write([]byte("fake-export"))
	})

	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts, &core.Config{AppID: "id", AppSecret: "sec", BaseURL: ts.URL}
}

func TestListResources_WikiSingleNodeNoRecursion(t *testing.T) {
	api := &fakeAPI{
		nodes: map[string]core.WikiNode{
			"P0uPwE6DgiAG44kVoZucLxbSnC6": wikiNodeJSON("P0uPwE6DgiAG44kVoZucLxbSnC6", "objDOC", "docx", "产品说明", "1700000000"),
		},
	}
	ts, cfg := startFake(t, api)
	c := NewConnector(core.RegionFeishuLinks)
	res, err := c.ListResources(context.Background(), makeLinksConfig(cfg, []string{
		"https://ruijie.feishu.cn/wiki/P0uPwE6DgiAG44kVoZucLxbSnC6?from=from_copylink",
		"https://xxx.feishu.cn/wiki/space/SPC1",
		"https://xxx.feishu.cn/drive/folder/FLD1",
	}, nil), "")
	if err != nil {
		t.Fatal(err)
	}
	if api.listSpacesHit != 0 {
		t.Fatalf("ListWikiSpaces was called %d times; links connector must not enumerate spaces", api.listSpacesHit)
	}
	if api.listFilesHit != 0 {
		t.Fatalf("Drive file list was called %d times", api.listFilesHit)
	}
	if api.getNodeHit != 1 {
		t.Fatalf("get_node hits = %d, want 1", api.getNodeHit)
	}

	var ok, wikiSpace, folder int
	for _, r := range res {
		switch r.Type {
		case "docx":
			ok++
			if r.Name != "产品说明" {
				t.Errorf("title = %q", r.Name)
			}
			if r.HasChildren {
				t.Error("HasChildren must be false even when wiki node has children")
			}
			if r.ExternalID != "docx:objDOC" {
				t.Errorf("external_id = %q", r.ExternalID)
			}
		case "link_error":
			code, _ := r.Metadata["error_code"].(string)
			switch code {
			case core.RejectWikiSpace:
				wikiSpace++
			case core.RejectDriveFolder:
				folder++
			default:
				t.Errorf("unexpected error_code %q", code)
			}
		}
	}
	if ok != 1 || wikiSpace != 1 || folder != 1 {
		t.Fatalf("ok=%d wikiSpace=%d folder=%d resources=%d", ok, wikiSpace, folder, len(res))
	}
	_ = ts
}

func TestListResources_DedupeWikiAndDocxSameObj(t *testing.T) {
	api := &fakeAPI{
		nodes: map[string]core.WikiNode{
			"NODE1": wikiNodeJSON("NODE1", "objSAME", "docx", "同一篇", "1"),
		},
		metas: []core.DriveDocMeta{
			{DocToken: "objSAME", DocType: "docx", Title: "同一篇", LatestModifyTime: "1"},
		},
	}
	_, cfg := startFake(t, api)
	c := NewConnector(core.RegionFeishuLinks)
	res, err := c.ListResources(context.Background(), makeLinksConfig(cfg, []string{
		"https://x.feishu.cn/wiki/NODE1",
		"https://x.feishu.cn/docx/objSAME",
	}, nil), "")
	if err != nil {
		t.Fatal(err)
	}
	var docs int
	for _, r := range res {
		if r.Type == "docx" {
			docs++
		}
		if r.Type == "link_error" {
			t.Errorf("unexpected error resource: %+v", r.Metadata)
		}
	}
	if docs != 1 {
		t.Fatalf("docs = %d, want 1 (deduped)", docs)
	}
}

func TestListResources_NoPermission(t *testing.T) {
	api := &fakeAPI{forbidden: map[string]bool{"SECRET": true}}
	_, cfg := startFake(t, api)
	c := NewConnector(core.RegionFeishuLinks)
	res, err := c.ListResources(context.Background(), makeLinksConfig(cfg, []string{
		"https://x.feishu.cn/wiki/SECRET",
	}, nil), "")
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 1 || res[0].Type != "link_error" {
		t.Fatalf("got %+v", res)
	}
	if res[0].Metadata["error_code"] != "no_permission" {
		t.Errorf("error_code = %v", res[0].Metadata["error_code"])
	}
}

func TestFetchStream_ExportAndBlocks(t *testing.T) {
	api := &fakeAPI{
		nodes: map[string]core.WikiNode{
			"NODE1": wikiNodeJSON("NODE1", "objDOC", "docx", "标题", "100"),
		},
		docToken: "objDOC",
	}
	_, cfg := startFake(t, api)
	c := NewConnector(core.RegionFeishuLinks)
	ds := makeLinksConfig(cfg, []string{"https://x.feishu.cn/wiki/NODE1"}, []string{"docx:objDOC"})

	t.Setenv("FEISHU_DOCX_PARSE_MODE", "export")
	h := &recordingHandler{}
	if _, err := c.FetchStream(context.Background(), ds, nil, h); err != nil {
		t.Fatal(err)
	}
	if len(h.emitted) == 0 {
		t.Fatal("export path emitted nothing")
	}
	if !strings.Contains(string(h.emitted[0].Content), "fake-export") && h.emitted[0].ContentType != "application/octet-stream" {
		// export returns binary
		if len(h.emitted[0].Content) == 0 {
			t.Errorf("empty export content: %+v", h.emitted[0])
		}
	}

	t.Setenv("FEISHU_DOCX_PARSE_MODE", "blocks")
	h2 := &recordingHandler{}
	if _, err := c.FetchStream(context.Background(), ds, nil, h2); err != nil {
		t.Fatal(err)
	}
	foundMD := false
	for _, it := range h2.emitted {
		if it.ContentType == "text/markdown" && strings.Contains(string(it.Content), "hello from blocks") {
			foundMD = true
		}
	}
	if !foundMD {
		t.Fatalf("blocks path did not emit markdown, items=%d first=%+v", len(h2.emitted), itemTitles(h2.emitted))
	}
}

func TestFetchStream_SkipUnchanged(t *testing.T) {
	api := &fakeAPI{
		nodes: map[string]core.WikiNode{
			"NODE1": wikiNodeJSON("NODE1", "objDOC", "docx", "标题", "100"),
		},
		docToken: "objDOC",
	}
	_, cfg := startFake(t, api)
	c := NewConnector(core.RegionFeishuLinks)
	ds := makeLinksConfig(cfg, []string{"https://x.feishu.cn/wiki/NODE1"}, []string{"docx:objDOC"})
	t.Setenv("FEISHU_DOCX_PARSE_MODE", "export")

	cur, err := c.FetchStream(context.Background(), ds, nil, &recordingHandler{})
	if err != nil {
		t.Fatal(err)
	}
	h := &recordingHandler{}
	if _, err := c.FetchStream(context.Background(), ds, cur, h); err != nil {
		t.Fatal(err)
	}
	for _, it := range h.emitted {
		if !it.IsDeleted && len(it.Content) > 0 {
			t.Fatalf("expected skip on unchanged edit time, got %+v", it)
		}
	}
}

func TestFetchStream_DeletedLink(t *testing.T) {
	api := &fakeAPI{
		nodes: map[string]core.WikiNode{
			"NODE1": wikiNodeJSON("NODE1", "objDOC", "docx", "标题", "100"),
			"NODE2": wikiNodeJSON("NODE2", "objTWO", "docx", "另一篇", "100"),
		},
		docToken: "objDOC",
	}
	_, cfg := startFake(t, api)
	c := NewConnector(core.RegionFeishuLinks)
	ds := makeLinksConfig(cfg, []string{
		"https://x.feishu.cn/wiki/NODE1",
		"https://x.feishu.cn/wiki/NODE2",
	}, []string{"docx:objDOC", "docx:objTWO"})
	t.Setenv("FEISHU_DOCX_PARSE_MODE", "export")
	cur, err := c.FetchStream(context.Background(), ds, nil, &recordingHandler{})
	if err != nil {
		t.Fatal(err)
	}

	ds2 := makeLinksConfig(cfg, []string{"https://x.feishu.cn/wiki/NODE1"}, []string{"docx:objDOC"})
	h := &recordingHandler{}
	if _, err := c.FetchStream(context.Background(), ds2, cur, h); err != nil {
		t.Fatal(err)
	}
	var deleted bool
	for _, it := range h.emitted {
		if it.IsDeleted && it.ExternalID == "objTWO" {
			deleted = true
		}
	}
	if !deleted {
		t.Fatalf("expected IsDeleted for removed link, got %+v", h.emitted)
	}
}

func TestConnectorType(t *testing.T) {
	if NewConnector(core.RegionFeishuLinks).Type() != types.ConnectorTypeFeishuLinks {
		t.Fatal("feishu type")
	}
	if NewConnector(core.RegionLarkLinks).Type() != types.ConnectorTypeLarkLinks {
		t.Fatal("lark type")
	}
}

func itemTitles(items []types.FetchedItem) []string {
	out := make([]string, len(items))
	for i, it := range items {
		out[i] = it.Title + "/" + it.ContentType
	}
	return out
}
