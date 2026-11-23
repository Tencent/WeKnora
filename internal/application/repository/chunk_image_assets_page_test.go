package repository

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// 添加逆序时间戳和固定编号，避免页码测试恰好被原来的时间排序满足。
func addPageImage(
	t *testing.T,
	fx *imageFixture,
	seq int,
	doc, kind string,
	attrs map[string]any,
	locators types.SourceLocators,
) *types.Chunk {
	t.Helper()
	raw, err := json.Marshal([]fixtureImage{{URL: fmt.Sprintf("local://%s/%d.png", fx.kbID, seq), Attrs: attrs}})
	require.NoError(t, err, "图片测试数据应能序列化")
	c := &types.Chunk{
		ID: fmt.Sprintf("%s%06d", fx.kbID[:30], seq), TenantID: 1,
		KnowledgeBaseID: fx.kbID, KnowledgeID: doc, Content: "测试图片",
		ChunkType: kind, IsEnabled: true, ImageInfo: string(raw), SourceLocators: locators,
		CreatedAt: fx.base.Add(-time.Duration(seq) * time.Minute),
		UpdatedAt: fx.base.Add(-time.Duration(seq) * time.Minute),
	}
	require.NoError(t, fx.db.Create(c).Error, "测试图片应能通过真实投影触发器入库")
	return c
}

func TestListImageAssets_页码和同页序号按数字排序(t *testing.T) {
	imageAssetBackends(t, func(t *testing.T, db *gorm.DB) {
		fx := newImageFixture(t, db)
		p10 := addPageImage(
			t, fx, 3, "文档甲", "image_caption",
			map[string]any{"source.page": 10, "source.image_index": 0}, nil,
		)
		p2i10 := addPageImage(
			t, fx, 1, "文档甲", "image_caption",
			map[string]any{"source.page": 2, "source.image_index": 10}, nil,
		)
		p2i2 := addPageImage(
			t, fx, 2, "文档甲", "image_caption",
			map[string]any{"source.page": 2, "source.image_index": 2}, nil,
		)
		for _, tc := range []struct {
			name string
			desc bool
			want []string
		}{
			{"升序", false, []string{p2i2.ID, p2i10.ID, p10.ID}},
			{"降序仍保持同页阅读顺序", true, []string{p10.ID, p2i2.ID, p2i10.ID}},
		} {
			t.Run(tc.name, func(t *testing.T) {
				rows, total := fx.list(types.ImageAssetQuery{
					SortField: types.ImageAssetField{Builtin: "page"},
					SortDesc:  tc.desc,
				})
				require.EqualValues(t, 3, total, "页码排序不得改变总数")
				require.Equal(t, tc.want, pageRowIDs(rows), "页码和图序号必须按数字排序")
			})
		}
	})
}

func TestListImageAssets_缺失和非法页码始终放在文档末尾(t *testing.T) {
	imageAssetBackends(t, func(t *testing.T, db *gorm.DB) {
		fx := newImageFixture(t, db)
		var missing []string
		for i, value := range []any{nil, 0, -1, "2", "invalid", true, 2.5, 1e30, []any{2}, map[string]any{"page": 2}} {
			c := addPageImage(t, fx, i+1, "文档甲", "image_caption", map[string]any{"source.page": value}, nil)
			missing = append(missing, c.ID)
		}
		absent := addPageImage(t, fx, 11, "文档甲", "image_caption", nil, nil)
		missing = append(missing, absent.ID)
		valid := addPageImage(t, fx, 12, "文档甲", "image_caption", map[string]any{"source.page": 2}, nil)
		for _, desc := range []bool{false, true} {
			rows, total := fx.list(types.ImageAssetQuery{
				SortField: types.ImageAssetField{Builtin: "page"},
				SortDesc:  desc,
			})
			require.EqualValues(t, 12, total, "非法来源页码不能使图片丢失")
			require.Equal(t, append([]string{valid.ID}, missing...), pageRowIDs(rows), "未知页码在升降序下都应稳定置后")
		}
	})
}

func TestListImageAssets_按文档分组且跨分页没有重复遗漏(t *testing.T) {
	imageAssetBackends(t, func(t *testing.T, db *gorm.DB) {
		fx := newImageFixture(t, db)
		b10 := addPageImage(t, fx, 1, "doc-b", "image_caption", map[string]any{"source.page": 10}, nil)
		aMissing := addPageImage(t, fx, 2, "doc-a", "image_caption", nil, nil)
		a10 := addPageImage(t, fx, 3, "doc-a", "image_caption", map[string]any{"source.page": 10}, nil)
		b2 := addPageImage(t, fx, 4, "doc-b", "image_caption", map[string]any{"source.page": 2}, nil)
		a2 := addPageImage(t, fx, 5, "doc-a", "image_caption", map[string]any{"source.page": 2}, nil)
		bMissing := addPageImage(t, fx, 6, "doc-b", "image_caption", nil, nil)
		want := []string{a2.ID, a10.ID, aMissing.ID, b2.ID, b10.ID, bMissing.ID}
		for repeat := 0; repeat < 2; repeat++ {
			var got []string
			for offset := 0; offset <= 6; offset += 2 {
				rows, total := fx.list(types.ImageAssetQuery{
					SortField: types.ImageAssetField{Builtin: "page"},
					Offset:    offset, Limit: 2,
				})
				require.EqualValues(t, 6, total, "跨页查询总数应一致")
				got = append(got, pageRowIDs(rows)...)
			}
			require.Equal(t, want, got, "每个文档内部按页码排序，分页不得重排或漏图")
		}
		rows, total := fx.list(types.ImageAssetQuery{
			SortField:    types.ImageAssetField{Builtin: "page"},
			KnowledgeIDs: []string{"doc-b"}, Offset: 1, Limit: 2,
		})
		require.EqualValues(t, 3, total, "来源文档筛选应先于分页")
		require.Equal(t, []string{b10.ID, bMissing.ID}, pageRowIDs(rows), "筛选后的页码顺序应保持一致")
	})
}

func TestListImageAssets_旧图片回退到可靠PDF定位但不猜文本页码(t *testing.T) {
	imageAssetBackends(t, func(t *testing.T, db *gorm.DB) {
		fx := newImageFixture(t, db)
		loc := func(page int) types.SourceLocators {
			return types.SourceLocators{{Type: types.SourceLocatorPDF, Page: page}}
		}
		text := addPageImage(t, fx, 1, "doc-a", "text", nil, loc(1))
		nonPDF := addPageImage(
			t, fx, 2, "doc-a", "image_caption", nil,
			types.SourceLocators{{Type: types.SourceLocatorSlide, Page: 1, Slide: 1}},
		)
		p10 := addPageImage(t, fx, 3, "doc-a", "image_ocr", nil, loc(10))
		p2 := addPageImage(t, fx, 4, "doc-a", "image_caption", nil, append(loc(10), loc(2)...))
		p1 := addPageImage(t, fx, 5, "doc-a", "image_caption", map[string]any{"source.page": 1}, loc(20))
		rows, total := fx.list(types.ImageAssetQuery{SortField: types.ImageAssetField{Builtin: "page"}})
		require.EqualValues(t, 5, total, "旧图回退不得改变资产数")
		require.Equal(
			t, []string{p1.ID, p2.ID, p10.ID, text.ID, nonPDF.ID}, pageRowIDs(rows),
			"仅专用图片chunk的PDF定位可作回退，显式逐图页码优先",
		)
	})
}

// 返回真实查询结果的稳定标识，断言不依赖模型文案或图片 URL。
func pageRowIDs(rows []types.ImageAssetRow) []string {
	ids := make([]string, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, row.ChunkID)
	}
	return ids
}

func TestListImageAssets_旧定位非法结构不会导致查询失败(t *testing.T) {
	imageAssetBackends(t, func(t *testing.T, db *gorm.DB) {
		fx := newImageFixture(t, db)
		var missing []string
		values := []string{
			`null`, `{}`, `true`, `[]`,
			`[null,2,"bad",{"type":"pdf","page":"2"},{"type":"pdf","page":0},` +
				`{"type":"pdf","page":1e30},{"type":"pdf","page":2.5}]`,
		}
		if db.Name() == "sqlite" {
			values = append(values, "invalid-json")
		}
		for i, raw := range values {
			c := addPageImage(t, fx, i+1, "doc-a", "image_caption", nil, nil)
			require.NoError(
				t, db.Exec("UPDATE chunks SET source_locators = ? WHERE id = ?", raw, c.ID).Error,
				"准备历史定位数据",
			)
			missing = append(missing, c.ID)
		}
		valid := addPageImage(
			t, fx, 20, "doc-a", "image_caption", nil,
			types.SourceLocators{{Type: types.SourceLocatorPDF, Page: 2}},
		)
		rows, total := fx.list(types.ImageAssetQuery{SortField: types.ImageAssetField{Builtin: "page"}})
		require.EqualValues(t, len(values)+1, total, "无定位图片不能丢失")
		require.Equal(t, append([]string{valid.ID}, missing...), pageRowIDs(rows), "历史格式异常应降级为未知页码")
		require.NotNil(t, rows[0].SourcePage, "可靠定位必须供响应层显示")
		require.EqualValues(t, 2, *rows[0].SourcePage, "页码不重复加一")
		for _, row := range rows[1:] {
			require.Nil(t, row.SourcePage, "未知页码不能伪装成0")
		}
	})
}

func TestListImageAssets_旧向量图胜出仍从同图图片分块回退页码(t *testing.T) {
	imageAssetBackends(t, func(t *testing.T, db *gorm.DB) {
		for _, kind := range []string{"image_ocr", "image_caption"} {
			t.Run(kind, func(t *testing.T) {
				fx := newImageFixture(t, db)
				key := "local://" + fx.kbID + "/same.png"
				// 一份副本只有 original_url，仍应使用投影表已有的同一 canonical image_key。
				source := fx.addRaw(kind, 0, fmt.Sprintf(`[{"url":"","original_url":%q}]`, key))
				setPageImageLocators(t, db, source.ID, `[{"type":"pdf","page":2}]`)
				winner := fx.add("image_vector", 10, fixtureImage{URL: key})
				require.NoError(t, db.Model(winner).UpdateColumn("chunk_index", 200).Error, "图块序号不能充当页码")
				next := fx.add("image_caption", 1, fixtureImage{
					URL:   "local://" + fx.kbID + "/next.png",
					Attrs: map[string]any{"source.page": 10},
				})
				for _, filtered := range []bool{false, true} {
					query := types.ImageAssetQuery{SortField: types.ImageAssetField{Builtin: "page"}, Limit: 1}
					if filtered {
						query.KnowledgeIDs = []string{winner.KnowledgeID}
					}
					first, total := fx.list(query)
					require.EqualValues(t, 2, total, "回退页码不得增加去重后的图数")
					require.Equal(t, []string{winner.ID}, pageRowIDs(first), "向量图仍须胜出且按同图可靠页码在分页前排序")
					require.NotNil(t, first[0].SourcePage, "胜出的旧向量图应读取同图OCR或caption页码")
					require.EqualValues(t, 2, *first[0].SourcePage, "读取一基PDF页码，不能使用图块索引")
					query.Offset = 1
					second, total := fx.list(query)
					require.EqualValues(t, 2, total, "跨分页总数应保持一致")
					require.Equal(t, []string{next.ID}, pageRowIDs(second), "跨分页不得因副本回退而重复或漏图")
				}
				rows, _ := fx.list(types.ImageAssetQuery{
					SortField: types.ImageAssetField{Builtin: "created_at"},
					SortDesc:  true,
				})
				require.Equal(t, winner.ID, rows[0].ChunkID, "旧创建时间排序仍选原胜出副本")
				require.NotNil(t, rows[0].SourcePage, "旧排序下的卡片也必须能显示可靠回退页码")
				require.EqualValues(t, 2, *rows[0].SourcePage, "排序和展示应使用同一页码")
			})
		}
	})
}

func TestListImageAssets_同图页码回退严格隔离范围和来源(t *testing.T) {
	imageAssetBackends(t, func(t *testing.T, db *gorm.DB) {
		for _, scenario := range []string{
			"其他租户", "其他知识库", "其他文档",
			"不同图片但原始URL相同", "没有URL的不同图片",
			"软删除图片", "陈旧投影软删除图片", "硬删除图片",
			"普通文本定位", "向量图定位", "非PDF定位", "非法PDF页码",
		} {
			t.Run(scenario, func(t *testing.T) {
				fx := newImageFixture(t, db)
				key := "local://" + fx.kbID + "/same.png"
				candidate := fx.add("image_caption", 0, fixtureImage{URL: key})
				setPageImageLocators(t, db, candidate.ID, `[{"type":"pdf","page":2}]`)
				winner := fx.add("image_vector", 10, fixtureImage{URL: key})
				switch scenario {
				case "其他租户":
					require.NoError(t, db.Model(candidate).UpdateColumn("tenant_id", 2).Error, "准备跨租户副本")
				case "其他知识库":
					require.NoError(
						t, db.Model(candidate).UpdateColumn("knowledge_base_id", newImageFixture(t, db).kbID).Error,
						"准备跨知识库副本",
					)
				case "其他文档":
					require.NoError(
						t, db.Model(candidate).UpdateColumn("knowledge_id", "other-document").Error,
						"准备跨文档副本",
					)
				case "不同图片但原始URL相同":
					raw := fmt.Sprintf(`[{"url":%q,"original_url":%q}]`, key+"?different-image", key)
					require.NoError(
						t, db.Model(candidate).UpdateColumn("image_info", raw).Error,
						"非空url优先于original_url，不能按原始URL误认同图",
					)
				case "没有URL的不同图片":
					require.NoError(t, db.Model(candidate).UpdateColumn("image_info", `[{}]`).Error, "准备无URL的来源图片")
					require.NoError(
						t, db.Model(winner).UpdateColumn("image_info", `[{}]`).Error,
						"无URL时canonical key应包含各自图块ID",
					)
				case "软删除图片":
					require.NoError(t, db.Delete(candidate).Error, "准备软删除副本")
				case "陈旧投影软删除图片":
					// 模拟历史投影残留：复制当前图片，再软删并单独恢复其投影行。
					// 这样页码回退必须检查源 chunk 的 deleted_at，而不能只依赖投影同步。
					var stale map[string]any
					require.NoError(
						t, db.Table("chunk_images").
							Where("chunk_id = ? AND image_index = ?", candidate.ID, 0).
							Take(&stale).Error,
						"保存删除前的单张图片投影",
					)
					require.NoError(t, db.Delete(candidate).Error, "软删来源图片并让正常触发器清理投影")
					var remaining int64
					require.NoError(
						t, db.Table("chunk_images").Where("chunk_id = ?", candidate.ID).Count(&remaining).Error,
						"确认只检查当前来源图片",
					)
					require.Zero(t, remaining, "正常软删应先清除当前图片投影")
					restored := db.Table("chunk_images").Create(stale)
					require.NoError(t, restored.Error, "仅在测试库恢复这条陈旧投影，不改动chunks或触发器")
					require.EqualValues(t, 1, restored.RowsAffected, "仅恢复当前图片的一条投影")
				case "硬删除图片":
					require.NoError(t, db.Unscoped().Delete(candidate).Error, "准备硬删除副本")
				case "普通文本定位":
					require.NoError(t, db.Model(candidate).UpdateColumn("chunk_type", "text").Error, "文本分块的跨页定位不能作逐图来源")
				case "向量图定位":
					require.NoError(
						t, db.Model(candidate).UpdateColumn("chunk_type", "image_vector").Error,
						"仅OCR和caption定位可作为历史回退",
					)
				case "非PDF定位":
					setPageImageLocators(t, db, candidate.ID, `[{"type":"slide","page":2,"slide":2}]`)
				case "非法PDF页码":
					setPageImageLocators(
						t, db, candidate.ID,
						`[{"type":"pdf","page":"2"},{"type":"pdf","page":0},{"type":"pdf","page":1e30}]`,
					)
				}
				for _, filtered := range []bool{false, true} {
					query := types.ImageAssetQuery{SortField: types.ImageAssetField{Builtin: "page"}}
					if filtered {
						query.KnowledgeIDs = []string{winner.KnowledgeID}
					}
					rows, _ := fx.list(query)
					found := false
					for _, row := range rows {
						if row.ChunkID == winner.ID {
							found = true
							require.Nil(t, row.SourcePage, "不能从范围外、另一图片、删除记录或不可靠定位借用页码")
						}
					}
					require.True(t, found, "页码未知不能让胜出图片消失")
				}
			})
		}
	})
}

// 仅更新来源定位，避免测试夹具的修改时间意外改变副本胜出顺序。
func setPageImageLocators(t *testing.T, db *gorm.DB, chunkID, raw string) {
	t.Helper()
	require.NoError(t, db.Exec("UPDATE chunks SET source_locators = ? WHERE id = ?", raw, chunkID).Error, "准备图片的来源定位")
}
