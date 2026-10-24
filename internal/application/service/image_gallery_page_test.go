package service

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

func TestProcessImage_页码来源不被模型观察覆盖(t *testing.T) {
	for _, tc := range []struct {
		name                string
		observed, malformed bool
	}{
		{"正常属性观察", true, false}, {"属性观察失败但OCR成功", true, true}, {"未开启属性观察", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := &attrsChunkRepo{}
			svc := newAttrsTestService(&attrsFileService{body: testPNG(t)}, repo)
			fake := &attrsFakeVLM{reply: func(prompt string, _ int) (string, error) {
				if strings.Contains(prompt, "OCR assistant") {
					return "测试文字", nil
				}
				if tc.malformed {
					return "模型没有提供结构化属性", nil
				}
				return "contain.text: block\ncontain.data_visual: false\nDESCRIPTION: 测试图片。", nil
			}}
			payload := &types.ImageMultimodalPayload{
				TenantID: 1, KnowledgeID: "doc-a", KnowledgeBaseID: "kb-a", ChunkID: "parent-a",
				ImageURL: "local://test.png", ImageIndex: 10,
				ImagePipelineParams: map[string]any{observeCaptionKey: true},
				ImageAttrsEnabled:   tc.observed, ImageActions: types.DefaultImageActions(),
				SourceLocators: types.SourceLocators{
					{Type: types.SourceLocatorSlide, Page: 1, Slide: 1},
					{Type: types.SourceLocatorPDF, Page: 10},
					{Type: types.SourceLocatorPDF, Page: 2},
					{Type: types.SourceLocatorPDF, Page: -1},
				},
			}
			runProcessImage(t, svc, payload, fake)
			require.NotEmpty(t, repo.created, "成功OCR必须产出图片chunk")
			for _, chunk := range repo.created {
				var images []types.ImageInfo
				require.NoError(t, json.Unmarshal([]byte(chunk.ImageInfo), &images), "图片记录应能读取")
				require.Len(t, images, 1, "专用图片chunk仅承载一张图片")
				require.Equal(t, float64(2), images[0].Attrs.Attrs["source.page"], "应持久化最早有效的PDF页码，不能再次加一")
				require.Equal(t, float64(10), images[0].Attrs.Attrs["source.image_index"], "应持久化解析顺序，不能使用异步完成时间")
				require.Equal(t, payload.SourceLocators, chunk.SourceLocators, "不得破坏原有引用定位")
			}
		})
	}
}

func TestProcessImage_未知PDF页码不伪造且零基图序号保留(t *testing.T) {
	repo := &attrsChunkRepo{}
	svc := newAttrsTestService(&attrsFileService{body: testPNG(t)}, repo)
	fake := &attrsFakeVLM{reply: func(string, int) (string, error) { return "测试图片文字", nil }}
	payload := &types.ImageMultimodalPayload{
		TenantID: 1, KnowledgeID: "doc-a", KnowledgeBaseID: "kb-a", ImageURL: "local://test.png", ImageIndex: 0,
		ImageActions: types.DefaultImageActions(),
		SourceLocators: types.SourceLocators{
			{Type: types.SourceLocatorPDF, Page: 0},
			{Type: types.SourceLocatorSlide, Page: 2, Slide: 2},
		},
	}
	runProcessImage(t, svc, payload, fake)
	require.NotEmpty(t, repo.created, "测试需要实际持久化的图片")
	for _, chunk := range repo.created {
		var images []types.ImageInfo
		require.NoError(t, json.Unmarshal([]byte(chunk.ImageInfo), &images))
		require.NotContains(t, images[0].Attrs.Attrs, "source.page", "缺少有效PDF定位时不得展示页码")
		require.Equal(t, float64(0), images[0].Attrs.Attrs["source.image_index"], "第一张图的零基序号也是有效数据")
	}
}

func TestBuildImageAssetQuery_页码字段和旧默认兼容(t *testing.T) {
	q := buildImageAssetQuery(nil, &types.Pagination{Page: 2, PageSize: 2})
	require.Equal(t, "created_at", q.SortField.Builtin, "未指定排序的API仍按创建时间")
	require.True(t, q.SortDesc, "未指定排序的API仍为降序")
	q = buildImageAssetQuery(&types.ImageListFilter{SortBy: "builtin:page", SortOrder: "asc"}, &types.Pagination{})
	require.Equal(t, "page", q.SortField.Builtin, "页码使用内置字段，不走通用文本属性排序")
	require.False(t, q.SortDesc, "显式升序必须传到数据库")
	var page *types.GalleryResolvedAttr
	for _, attr := range types.ResolveGalleryConfig("", nil, nil, nil).Attributes {
		if attr.ID == "builtin:page" {
			page = &attr
			break
		}
	}
	require.NotNil(t, page, "图库contract必须公开页码排序，否则handler会剔除它")
	require.True(t, page.Usage.InSortField, "页码应能排序")
	require.False(t, page.Usage.InSearchField, "不把页码加入全文搜索")
}

// pageRowsRepository 只提供数据库已校验的投影，验证响应层使用同一页码而不重猜定位。
type pageRowsRepository struct {
	interfaces.ChunkRepository
	rows []types.ImageAssetRow
}

func (r *pageRowsRepository) ListImageAssets(
	context.Context,
	uint64,
	string,
	*types.ImageAssetQuery,
) ([]types.ImageAssetRow, int64, error) {
	return r.rows, int64(len(r.rows)), nil
}

func TestListImages_旧图回退页码进入响应并剔除非法页码(t *testing.T) {
	page := int64(2)
	repo := &pageRowsRepository{rows: []types.ImageAssetRow{
		{ChunkID: "legacy", AttrsJSON: `{"contain.text":"block"}`, SourcePage: &page},
		{ChunkID: "invalid", AttrsJSON: `{"source.page":"invalid","contain.text":"none"}`},
	}}
	svc := &chunkService{chunkRepository: repo}
	result, err := svc.ListImagesByKnowledgeBaseID(
		types.WithExecutionTenant(context.Background(), 1), "kb", &types.Pagination{}, nil,
	)
	require.NoError(t, err, "图片响应应能构建")
	images, ok := result.Data.([]types.ImageAsset)
	require.True(t, ok, "分页内容应为图片资产")
	require.Equal(t, int64(2), images[0].Attrs["source.page"], "旧图卡片必须展示排序实际采用的页码")
	require.Equal(t, "block", images[0].Attrs["contain.text"], "来源元数据不能覆盖模型属性")
	require.NotContains(t, images[1].Attrs, "source.page", "非法页码应在响应中省略")
}

func TestProcessImage_来源属性只写持久化不污染观察轨迹(t *testing.T) {
	for _, tc := range []struct {
		name      string
		reply     string
		wantAttrs map[string]any
	}{
		{
			name:      "正常观察保留原属性",
			reply:     "contain.text: block\ncontain.data_visual: false\nDESCRIPTION: 测试图片。",
			wantAttrs: map[string]any{"contain.text": "block", "contain.data_visual": false},
		},
		{
			name:      "非结构化回答仍为空观察",
			reply:     "模型只返回了图片描述，没有结构化属性。",
			wantAttrs: map[string]any{},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := &attrsChunkRepo{}
			svc := newAttrsTestService(&attrsFileService{body: testPNG(t)}, repo)
			fake := &attrsFakeVLM{reply: func(prompt string, _ int) (string, error) {
				if strings.Contains(prompt, "OCR assistant") {
					return "测试图片中的文字", nil
				}
				return tc.reply, nil
			}}
			payload := &types.ImageMultimodalPayload{
				TenantID: 1, KnowledgeID: "doc-a", KnowledgeBaseID: "kb-a", ChunkID: "parent-a",
				ImageURL: "local://test.png", ImageIndex: 3,
				ImagePipelineParams: map[string]any{observeCaptionKey: true},
				ImageAttrsEnabled:   true, ImageActions: types.DefaultImageActions(),
				SourceLocators: types.SourceLocators{{Type: types.SourceLocatorPDF, Page: 2}},
			}

			out := runProcessImage(t, svc, payload, fake)
			traceAttrs, ok := out[keyImageAttrs].(map[string]any)
			require.True(t, ok, "处理轨迹必须保留模型观察属性表")
			require.Equal(t, tc.wantAttrs, traceAttrs, "补充来源位置不能改写已经记录的模型观察")
			require.NotContains(t, traceAttrs, types.ImageSourcePageAttr, "解析器页码不属于模型观察")
			require.NotContains(t, traceAttrs, types.ImageSourceIndexAttr, "解析顺序不属于模型观察")

			require.NotEmpty(t, repo.created, "保守OCR路径必须实际写入图片分块")
			for _, chunk := range repo.created {
				var images []types.ImageInfo
				require.NoError(t, json.Unmarshal([]byte(chunk.ImageInfo), &images), "持久化图片记录应能读取")
				require.Len(t, images, 1, "图片分块应保存一张图片的属性")
				require.Equal(t, float64(2), images[0].Attrs.Attrs[types.ImageSourcePageAttr], "持久化属性应包含真实页码")
				require.Equal(t, float64(3), images[0].Attrs.Attrs[types.ImageSourceIndexAttr], "持久化属性应包含解析顺序")
				for key, value := range tc.wantAttrs {
					require.Equal(t, value, images[0].Attrs.Attrs[key], "持久化来源属性不能覆盖模型属性 %s", key)
				}
			}
		})
	}
}

func TestSetImageSourceAttrs_复制属性后补充或替换来源(t *testing.T) {
	for _, tc := range []struct {
		name       string
		attrs      map[string]any
		schema     string
		page       int
		imageIndex int
		wantAttrs  map[string]any
		wantSchema string
	}{
		{
			name: "空指针属性表", page: 2, imageIndex: 0,
			wantAttrs:  map[string]any{types.ImageSourcePageAttr: 2, types.ImageSourceIndexAttr: 0},
			wantSchema: types.ImageAttrSchemaVersion,
		},
		{
			name: "已分配但没有观察结果", attrs: map[string]any{}, page: 2, imageIndex: 0,
			wantAttrs:  map[string]any{types.ImageSourcePageAttr: 2, types.ImageSourceIndexAttr: 0},
			wantSchema: types.ImageAttrSchemaVersion,
		},
		{
			name: "来源只在副本中替换",
			attrs: map[string]any{
				"contain.text": "block", types.ImageSourcePageAttr: 99, types.ImageSourceIndexAttr: 99,
			},
			schema: "existing-schema", page: 2, imageIndex: 3,
			wantAttrs: map[string]any{
				"contain.text": "block", types.ImageSourcePageAttr: 2, types.ImageSourceIndexAttr: 3,
			},
			wantSchema: "existing-schema",
		},
		{
			name: "没有有效定位时只剔除副本的旧来源",
			attrs: map[string]any{
				"contain.text": "block", types.ImageSourcePageAttr: 99, types.ImageSourceIndexAttr: 99,
			},
			page: 0, imageIndex: -1,
			wantAttrs:  map[string]any{"contain.text": "block"},
			wantSchema: types.ImageAttrSchemaVersion,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// 保留一份独立期望；直接拿原 map 作期望会同时被错误实现改写。
			var original map[string]any
			if tc.attrs != nil {
				original = make(map[string]any, len(tc.attrs))
				for key, value := range tc.attrs {
					original[key] = value
				}
			}
			imageInfo := types.ImageInfo{Attrs: types.ImageAttrs{Schema: tc.schema, Attrs: tc.attrs}}
			payload := &types.ImageMultimodalPayload{
				ImageIndex:     tc.imageIndex,
				SourceLocators: types.SourceLocators{{Type: types.SourceLocatorPDF, Page: tc.page}},
			}

			setImageSourceAttrs(&imageInfo, payload)

			require.Equal(t, original, tc.attrs, "轨迹持有的原属性表不能被新增、替换或删除来源键")
			require.Equal(t, tc.wantAttrs, imageInfo.Attrs.Attrs, "持久化副本应采用当前解析器来源")
			require.Equal(t, tc.wantSchema, imageInfo.Attrs.Schema, "仅在原schema为空时设置默认值")
		})
	}
}
