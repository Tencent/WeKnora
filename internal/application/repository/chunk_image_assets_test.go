package repository

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// imageAssetBackends runs fn against in-memory SQLite and, when
// WEKNORA_REPOSITORY_TEST_POSTGRES_DSN points at a disposable database, against
// PostgreSQL too — the gallery query is spelled differently per dialect, so
// both spellings need the same assertions. For example:
//
//	docker run -d --rm -e POSTGRES_PASSWORD=pg -e POSTGRES_DB=weknora -p 55432:5432 \
//	  paradedb/paradedb:v0.22.6-pg17
//	WEKNORA_REPOSITORY_TEST_POSTGRES_DSN=postgres://postgres:pg@localhost:55432/weknora?sslmode=disable
func imageAssetBackends(t *testing.T, fn func(t *testing.T, db *gorm.DB)) {
	t.Run("sqlite", func(t *testing.T) { fn(t, setupChunkTestDB(t)) })
	t.Run("postgres", func(t *testing.T) {
		dsn := os.Getenv("WEKNORA_REPOSITORY_TEST_POSTGRES_DSN")
		if dsn == "" {
			t.Skip("set WEKNORA_REPOSITORY_TEST_POSTGRES_DSN to run the PostgreSQL gallery query")
		}
		db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
		require.NoError(t, err)
		require.NoError(t, db.AutoMigrate(&types.Chunk{}))
		fn(t, db)
	})
}

// imageFixture writes chunks for one fresh knowledge base; every test gets its
// own kbID so the postgres run needs no cleanup between tests.
type imageFixture struct {
	t    *testing.T
	db   *gorm.DB
	kbID string
	base time.Time
}

func newImageFixture(t *testing.T, db *gorm.DB) *imageFixture {
	return &imageFixture{t: t, db: db, kbID: uuid.NewString(), base: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
}

// fixtureImage is one image_info entry, written in the persisted shape: the
// observed attributes nest under attrs.attrs.
type fixtureImage struct {
	URL     string
	Caption string
	OCRText string
	Attrs   map[string]any
}

func (f fixtureImage) MarshalJSON() ([]byte, error) {
	out := map[string]any{"url": f.URL, "original_url": f.URL, "caption": f.Caption, "ocr_text": f.OCRText}
	if f.Attrs != nil {
		out["attrs"] = map[string]any{"schema": "attrs/2", "attrs": f.Attrs}
	}
	return json.Marshal(out)
}

// add inserts one chunk carrying images, updated minute minutes after base.
func (fx *imageFixture) add(chunkType string, minute int, images ...fixtureImage) *types.Chunk {
	fx.t.Helper()
	raw, err := json.Marshal(images)
	require.NoError(fx.t, err)
	return fx.addRaw(chunkType, minute, string(raw))
}

func (fx *imageFixture) addRaw(chunkType string, minute int, imageInfo string) *types.Chunk {
	fx.t.Helper()
	at := fx.base.Add(time.Duration(minute) * time.Minute)
	c := &types.Chunk{
		ID:              uuid.NewString(),
		TenantID:        1,
		KnowledgeBaseID: fx.kbID,
		KnowledgeID:     "doc-" + fx.kbID[:8],
		Content:         "x",
		ChunkType:       chunkType,
		IsEnabled:       true,
		ImageInfo:       imageInfo,
		CreatedAt:       at,
		UpdatedAt:       at,
	}
	require.NoError(fx.t, fx.db.Create(c).Error)
	return c
}

func (fx *imageFixture) list(q types.ImageAssetQuery) ([]types.ImageAssetRow, int64) {
	fx.t.Helper()
	if q.Limit == 0 {
		q.Limit = 100
	}
	rows, total, err := NewChunkRepository(fx.db).ListImageAssets(context.Background(), 1, fx.kbID, &q)
	require.NoError(fx.t, err)
	return rows, total
}

func rowURLs(t *testing.T, rows []types.ImageAssetRow) []string {
	t.Helper()
	urls := make([]string, 0, len(rows))
	for _, r := range rows {
		var img struct {
			URL string `json:"url"`
		}
		require.NoError(t, json.Unmarshal([]byte(r.ImageJSON), &img))
		urls = append(urls, img.URL)
	}
	return urls
}

var (
	fieldCaption = types.ImageAssetField{Builtin: "caption"}
	fieldOCR     = types.ImageAssetField{Builtin: "ocr_text"}
	fieldText    = types.ImageAssetField{Attr: "contain.text"}
	fieldVisual  = types.ImageAssetField{Attr: "contain.data_visual"}
)

func TestListImageAssets_DedupsAndExpandsArrays(t *testing.T) {
	imageAssetBackends(t, func(t *testing.T, db *gorm.DB) {
		fx := newImageFixture(t, db)
		// The multimodal pipeline writes the same image to its OCR and caption
		// children; the most recently updated copy is the one listed.
		fx.add("image_ocr", 1, fixtureImage{URL: "local://a.png", Caption: "old"})
		caption := fx.add("image_caption", 2, fixtureImage{URL: "local://a.png", Caption: "new"})
		// A pre-2026-02 text chunk carrying several images, one of them shared.
		legacy := fx.add("text", 0,
			fixtureImage{URL: "local://b.png"}, fixtureImage{URL: "local://c.png"}, fixtureImage{URL: "local://a.png"})

		rows, total := fx.list(types.ImageAssetQuery{SortField: types.ImageAssetField{Builtin: "created_at"}})
		require.EqualValues(t, 3, total)
		require.ElementsMatch(t, []string{"local://a.png", "local://b.png", "local://c.png"}, rowURLs(t, rows))
		for _, r := range rows {
			switch rowURLs(t, []types.ImageAssetRow{r})[0] {
			case "local://a.png":
				require.Equal(t, caption.ID, r.ChunkID)
				require.Contains(t, r.ImageJSON, `"new"`)
			case "local://c.png":
				require.Equal(t, legacy.ID, r.ChunkID)
				require.Equal(t, 1, r.ImageIndex)
			}
		}
	})
}

func TestListImageAssets_SkipsForeignDeletedAndMalformedRows(t *testing.T) {
	imageAssetBackends(t, func(t *testing.T, db *gorm.DB) {
		fx := newImageFixture(t, db)
		fx.add("image_caption", 0, fixtureImage{URL: "local://keep.png"})
		deleted := fx.add("image_caption", 0, fixtureImage{URL: "local://deleted.png"})
		require.NoError(t, db.Delete(deleted).Error)
		fx.addRaw("text", 0, "")
		fx.addRaw("text", 0, `{"url":"local://object.png"}`)
		other := newImageFixture(t, db)
		other.add("image_caption", 0, fixtureImage{URL: "local://other-kb.png"})
		if db.Name() == "sqlite" {
			// postgres rejects malformed JSON at write time in practice
			// (image_info is always produced by json.Marshal); sqlite must not
			// abort the whole listing on one bad row.
			fx.addRaw("text", 0, `[{"url": broken`)
		}

		rows, total := fx.list(types.ImageAssetQuery{})
		require.EqualValues(t, 1, total)
		require.Equal(t, []string{"local://keep.png"}, rowURLs(t, rows))
	})
}

func TestListImageAssets_PagesStablyThroughTies(t *testing.T) {
	imageAssetBackends(t, func(t *testing.T, db *gorm.DB) {
		fx := newImageFixture(t, db)
		// Every image of one chunk shares its timestamps: a desc sort on
		// created_at is all ties and must still page without repeats or gaps.
		var imgs []fixtureImage
		for _, name := range []string{"p0", "p1", "p2", "p3", "p4"} {
			imgs = append(imgs, fixtureImage{URL: "local://" + name + ".png"})
		}
		fx.add("text", 0, imgs...)

		var seen []string
		for offset := 0; offset < 6; offset += 2 {
			rows, total := fx.list(types.ImageAssetQuery{
				SortField: types.ImageAssetField{Builtin: "created_at"}, SortDesc: true, Offset: offset, Limit: 2,
			})
			require.EqualValues(t, 5, total, "offset %d", offset)
			seen = append(seen, rowURLs(t, rows)...)
		}
		require.Equal(t, []string{
			"local://p0.png", "local://p1.png", "local://p2.png", "local://p3.png", "local://p4.png",
		}, seen)

		rows, total := fx.list(types.ImageAssetQuery{Offset: 10, Limit: 2})
		require.Empty(t, rows)
		require.EqualValues(t, 5, total, "a page past the end still reports the total")
	})
}

func TestListImageAssets_KeywordSearch(t *testing.T) {
	imageAssetBackends(t, func(t *testing.T, db *gorm.DB) {
		fx := newImageFixture(t, db)
		fx.add("image_caption", 0, fixtureImage{URL: "local://chart.png", Caption: "Quarterly Revenue chart"})
		fx.add("image_caption", 1, fixtureImage{URL: "local://scan.png", OCRText: "growth 50% year over year"})
		fx.add("image_caption", 2, fixtureImage{URL: "local://logo.png", Caption: "company logo"})

		search := func(kw string, fields ...types.ImageAssetField) []string {
			rows, _ := fx.list(types.ImageAssetQuery{Keyword: kw, SearchFields: fields})
			return rowURLs(t, rows)
		}
		require.Equal(t, []string{"local://chart.png"}, search("revenue", fieldCaption, fieldOCR),
			"matching is case-insensitive")
		require.Equal(t, []string{"local://scan.png"}, search("50%", fieldCaption, fieldOCR),
			"LIKE wildcards in the keyword are literal")
		require.Empty(t, search("50%", fieldCaption), "only the requested fields are searched")
		require.Empty(t, search("local://"), "no searchable field means no match")
	})
}

func TestListImageAssets_AttributeFiltersAndRules(t *testing.T) {
	imageAssetBackends(t, func(t *testing.T, db *gorm.DB) {
		fx := newImageFixture(t, db)
		fx.add("image_caption", 0, fixtureImage{
			URL:   "local://block.png",
			Attrs: map[string]any{"contain.text": "block", "contain.data_visual": true},
		})
		fx.add("image_caption", 1, fixtureImage{
			URL:   "local://none.png",
			Attrs: map[string]any{"contain.text": "none", "contain.data_visual": false},
		})
		// Never observed: no attribute keys at all.
		fx.add("image_caption", 2, fixtureImage{URL: "local://blank.png"})

		list := func(q types.ImageAssetQuery) []string {
			q.SortField = types.ImageAssetField{Builtin: "created_at"}
			rows, _ := fx.list(q)
			return rowURLs(t, rows)
		}

		require.Equal(t, []string{"local://block.png", "local://blank.png"}, list(types.ImageAssetQuery{
			AttrFilters: []types.ImageAssetValueSet{{Field: fieldText, Values: []string{"block"}}},
		}), "a filter narrows observed images and leaves unobserved ones alone")

		require.Equal(t, []string{"local://block.png", "local://blank.png"}, list(types.ImageAssetQuery{
			AttrFilters: []types.ImageAssetValueSet{{Field: fieldVisual, Values: []string{"true"}}},
		}), "JSON booleans compare as \"true\"/\"false\" on both backends")

		require.Equal(t, []string{"local://none.png", "local://blank.png"}, list(types.ImageAssetQuery{
			OffRules: []types.ImageAssetValueSet{{Field: fieldText, Values: []string{"block"}}},
		}), "off hides the images carrying the value and nothing else")

		require.Equal(t, []string{"local://block.png", "local://blank.png"}, list(types.ImageAssetQuery{
			OffRules: []types.ImageAssetValueSet{{Field: fieldText, Values: []string{"none", "block"}}},
			OnRules:  []types.ImageAssetValueSet{{Field: fieldVisual, Values: []string{"true"}}},
		}), "on outranks off for the same image, even across attributes")

		disabled := false
		require.Empty(t, list(types.ImageAssetQuery{IsEnabled: &disabled}))
	})
}

func TestListImageAssets_SortsByAttribute(t *testing.T) {
	imageAssetBackends(t, func(t *testing.T, db *gorm.DB) {
		fx := newImageFixture(t, db)
		fx.add("image_caption", 0, fixtureImage{URL: "local://s.png", Attrs: map[string]any{"contain.text": "sparse"}})
		fx.add("image_caption", 1, fixtureImage{URL: "local://b.png", Attrs: map[string]any{"contain.text": "block"}})
		fx.add("image_caption", 2, fixtureImage{URL: "local://u.png"})

		rows, _ := fx.list(types.ImageAssetQuery{SortField: fieldText})
		require.Equal(t, []string{"local://u.png", "local://b.png", "local://s.png"}, rowURLs(t, rows),
			"an image without the attribute sorts as the empty string")
	})
}
