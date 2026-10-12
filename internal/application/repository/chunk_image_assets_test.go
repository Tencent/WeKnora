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
//
// Both runs apply the real chunk_images migration, so the triggers and the
// backfill under test are the ones that ship.
func imageAssetBackends(t *testing.T, fn func(t *testing.T, db *gorm.DB)) {
	t.Run("sqlite", func(t *testing.T) {
		db := setupChunkTestDB(t)
		applyChunkImagesMigration(t, db)
		fn(t, db)
	})
	t.Run("postgres", func(t *testing.T) {
		dsn := os.Getenv("WEKNORA_REPOSITORY_TEST_POSTGRES_DSN")
		if dsn == "" {
			t.Skip("set WEKNORA_REPOSITORY_TEST_POSTGRES_DSN to run the PostgreSQL gallery query")
		}
		db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
		require.NoError(t, err)
		require.NoError(t, db.AutoMigrate(&types.Chunk{}))
		applyChunkImagesMigration(t, db)
		fn(t, db)
	})
}

// applyChunkImagesMigration runs the dialect's chunk_images up migration. It
// is idempotent, which is also what lets a test re-run it to exercise the
// backfill.
func applyChunkImagesMigration(t *testing.T, db *gorm.DB) {
	t.Helper()
	path := "../../../migrations/sqlite/000032_chunk_images.up.sql"
	if db.Name() == "postgres" {
		path = "../../../migrations/versioned/000113_chunk_images.up.sql"
	}
	sql, err := os.ReadFile(path)
	require.NoError(t, err)
	require.NoError(t, db.Exec(string(sql)).Error)
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
		urls = append(urls, r.URL)
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
				require.Equal(t, "new", r.Caption)
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
		// A malformed row must neither fail the chunk write nor the listing.
		fx.addRaw("text", 0, `[{"url": broken`)

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
		// Ties follow the sort direction, so newest-first walks the chunk's
		// images from the last one, like images of separate chunks.
		require.Equal(t, []string{
			"local://p4.png", "local://p3.png", "local://p2.png", "local://p1.png", "local://p0.png",
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
		_, total := fx.list(types.ImageAssetQuery{Keyword: "local://"})
		require.Zero(t, total, "and the total agrees")
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

func TestListImageAssets_TriggersFollowChunkWrites(t *testing.T) {
	imageAssetBackends(t, func(t *testing.T, db *gorm.DB) {
		fx := newImageFixture(t, db)
		older := fx.add("image_ocr", 1, fixtureImage{URL: "local://a.png", Caption: "old"})
		newer := fx.add("image_caption", 2, fixtureImage{URL: "local://a.png", Caption: "new"})
		keep := fx.add("image_caption", 3, fixtureImage{URL: "local://b.png", Caption: "b"})
		captions := func() []string {
			rows, _ := fx.list(types.ImageAssetQuery{SortField: types.ImageAssetField{Builtin: "created_at"}})
			out := make([]string, 0, len(rows))
			for _, r := range rows {
				out = append(out, r.Caption)
			}
			return out
		}
		require.Equal(t, []string{"new", "b"}, captions())

		// A status-only write (no image change) still moves the image to the
		// copy that is now the most recently updated.
		require.NoError(t, db.Model(older).Updates(map[string]any{
			"status": 2, "updated_at": fx.base.Add(time.Hour),
		}).Error)
		require.Equal(t, []string{"old", "b"}, captions())
		require.NoError(t, db.Model(newer).Updates(map[string]any{
			"status": 2, "updated_at": fx.base.Add(2 * time.Hour),
		}).Error)
		require.Equal(t, []string{"new", "b"}, captions())

		// Soft-deleting the winning copy hands the image to the other copy.
		require.NoError(t, db.Delete(newer).Error)
		require.Equal(t, []string{"old", "b"}, captions())

		// An edit to image_info shows up at once.
		raw, _ := json.Marshal([]fixtureImage{{URL: "local://a.png", Caption: "edited"}})
		require.NoError(t, db.Model(older).Update("image_info", string(raw)).Error)
		require.Equal(t, []string{"edited", "b"}, captions())

		// So does the enabled flag.
		require.NoError(t, db.Model(keep).Update("is_enabled", false).Error)
		disabled := false
		rows, _ := fx.list(types.ImageAssetQuery{IsEnabled: &disabled})
		require.Equal(t, []string{"local://b.png"}, rowURLs(t, rows))

		// Moving a document to another knowledge base moves its images.
		target := newImageFixture(t, db)
		require.NoError(t, db.Model(keep).Update("knowledge_base_id", target.kbID).Error)
		require.Equal(t, []string{"edited"}, captions())
		rows, _ = target.list(types.ImageAssetQuery{})
		require.Equal(t, []string{"local://b.png"}, rowURLs(t, rows))

		// A hard delete removes the image entirely.
		require.NoError(t, db.Unscoped().Delete(older).Error)
		require.Empty(t, captions())
	})
}

func TestListImageAssets_MigrationBackfillsExistingChunks(t *testing.T) {
	imageAssetBackends(t, func(t *testing.T, db *gorm.DB) {
		fx := newImageFixture(t, db)
		fx.add("image_ocr", 1, fixtureImage{URL: "local://a.png", Caption: "old"})
		fx.add("image_caption", 2, fixtureImage{URL: "local://a.png", Caption: "new"})
		fx.add("text", 0, fixtureImage{URL: "local://b.png"}, fixtureImage{URL: "local://c.png"})
		// Forget the projection, as a database that predates the migration.
		require.NoError(t, db.Exec("DELETE FROM chunk_images WHERE knowledge_base_id = ?", fx.kbID).Error)
		applyChunkImagesMigration(t, db)

		rows, total := fx.list(types.ImageAssetQuery{})
		require.EqualValues(t, 3, total)
		require.ElementsMatch(t, []string{"local://a.png", "local://b.png", "local://c.png"}, rowURLs(t, rows))
		for _, r := range rows {
			if r.URL == "local://a.png" {
				require.Equal(t, "new", r.Caption, "the backfill elects the most recently updated copy")
			}
		}
	})
}
