package service

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	apprepo "github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// 模型替身只生成固定 Wiki 内容；调用计数区分整篇提取和单页归并。
// Map 内部有并行请求，因此记录必须加锁，不能复用无锁的捕获模型。
type wikiPageRetryModel struct {
	mu      sync.Mutex
	calls   map[string]int
	prompts map[string][]string
	respond func(context.Context, string, string, int) (string, error)
}

func newWikiPageRetryModel() *wikiPageRetryModel {
	return &wikiPageRetryModel{calls: make(map[string]int), prompts: make(map[string][]string)}
}

func (m *wikiPageRetryModel) Chat(
	ctx context.Context, messages []chat.Message, _ *chat.ChatOptions,
) (*types.ChatResponse, error) {
	purpose, _ := types.LLMCallMetadataFromContext(ctx)
	var text strings.Builder
	for _, message := range messages {
		text.WriteString(message.Content)
		text.WriteByte('\n')
	}
	prompt := text.String()
	key := purpose
	if purpose == "wiki_page_modify" {
		if strings.Contains(prompt, "entity/beta") {
			key += ":entity/beta"
		} else {
			key += ":entity/alpha"
		}
	}
	m.mu.Lock()
	m.calls[key]++
	call := m.calls[key]
	m.prompts[key] = append(m.prompts[key], prompt)
	m.mu.Unlock()
	if m.respond != nil {
		content, err := m.respond(ctx, purpose, prompt, call)
		if err != nil {
			return nil, err
		}
		return &types.ChatResponse{Content: content}, nil
	}
	var content string
	switch purpose {
	case "wiki_candidate_slug", "wiki_knowledge_extract":
		content = `{"entities":[{"name":"Alpha","slug":"entity/alpha",` +
			`"description":"Alpha facts","details":"Alpha new contribution"},` +
			`{"name":"Beta","slug":"entity/beta",` +
			`"description":"Beta facts","details":"Beta new contribution"}],"concepts":[]}`
	case "wiki_summary":
		content = "SUMMARY: Alpha and Beta\n\nAlpha and Beta are independent subjects described by this document."
	case "wiki_chunk_citation":
		content = `{"citations":{},"new_slugs":[]}`
	case "wiki_taxonomy_plan":
		content = `{"assignments":[]}`
	case "wiki_deduplication":
		content = `{"decisions":[]}`
	case "wiki_page_modify":
		content = wikiPageRetryAnswer(prompt)
	default:
		return nil, fmt.Errorf("测试未预期的模型用途: %s", purpose)
	}
	return &types.ChatResponse{Content: content}, nil
}

func (*wikiPageRetryModel) ChatStream(
	context.Context, []chat.Message, *chat.ChatOptions,
) (<-chan types.StreamResponse, error) {
	return nil, errors.New("测试不使用流式模型")
}

func (*wikiPageRetryModel) GetModelName() string { return "wiki-page-retry-fixture" }
func (*wikiPageRetryModel) GetModelID() string   { return "wiki-page-retry-fixture" }

func (m *wikiPageRetryModel) count(key string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.calls[key]
}

func (m *wikiPageRetryModel) captured(key string) []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string(nil), m.prompts[key]...)
}

func wikiPageRetryAnswer(prompt string) string {
	body := "SUMMARY: merged\n\nALPHA_BASE\nNEW_CONTRIBUTION"
	if strings.Contains(prompt, "entity/beta") {
		body = "SUMMARY: merged\n\nBETA_BASE\nNEW_CONTRIBUTION"
	}
	if strings.Contains(prompt, "CONCURRENT_UPDATE") {
		body += "\nCONCURRENT_UPDATE"
	}
	return body
}

// 写入钩子放在真实服务之前，可模拟外部作者在模型生成期间修改了目标页。
type wikiPageRetryStore struct {
	interfaces.WikiPageService
	mu          sync.Mutex
	writes      map[string]int
	results     map[string][]error
	beforeWrite func(context.Context, *types.WikiPage, int) error
}

func newWikiPageRetryStore(service interfaces.WikiPageService) *wikiPageRetryStore {
	return &wikiPageRetryStore{
		WikiPageService: service, writes: make(map[string]int), results: make(map[string][]error),
	}
}

func (s *wikiPageRetryStore) UpdatePage(ctx context.Context, page *types.WikiPage) (*types.WikiPage, error) {
	s.mu.Lock()
	s.writes[page.Slug]++
	call := s.writes[page.Slug]
	s.mu.Unlock()
	var result *types.WikiPage
	var err error
	if s.beforeWrite != nil {
		err = s.beforeWrite(ctx, page, call)
	}
	if err == nil {
		result, err = s.WikiPageService.UpdatePage(ctx, page)
	}
	s.mu.Lock()
	s.results[page.Slug] = append(s.results[page.Slug], err)
	s.mu.Unlock()
	return result, err
}

func (s *wikiPageRetryStore) count(slug string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.writes[slug]
}

// 只记录页面 span，模型生成子 span 不与页面最终结果混算。
type wikiPageRetryTracker struct {
	noopSpanTracker
	mu       sync.Mutex
	starts   int
	ends     int
	failures int
}

func (s *wikiPageRetryTracker) BeginSubSpan(_ context.Context, parent *Span, name, kind string, _ types.JSONMap) *Span {
	if parent == nil || !strings.HasPrefix(name, "postprocess.wiki.page[") {
		return nil
	}
	s.mu.Lock()
	s.starts++
	s.mu.Unlock()
	return &Span{Name: name, Kind: kind, KnowledgeID: parent.KnowledgeID}
}

func (s *wikiPageRetryTracker) EndSpan(_ context.Context, span *Span, _ types.JSONMap) {
	if span == nil {
		return
	}
	s.mu.Lock()
	s.ends++
	s.mu.Unlock()
}

func (s *wikiPageRetryTracker) FailSpan(_ context.Context, span *Span, _, _ string, _ error) {
	if span == nil {
		return
	}
	s.mu.Lock()
	s.failures++
	s.mu.Unlock()
}

func wikiPageRetryUpdates() []SlugUpdate {
	return []SlugUpdate{{
		Slug: "entity/alpha", Type: types.WikiPageTypeEntity,
		KnowledgeID: "k-row-guard", SourceRef: "k-row-guard", DocTitle: "Source",
		Item: extractedItem{Name: "Alpha", Description: "NEW_CONTRIBUTION", Details: "New facts."},
	}}
}

func newWikiPageRetryReducer(t *testing.T) (*wikiIngestService, *wikiPageRetryStore, *wikiPageRetryModel) {
	t.Helper()
	svc, store, model, _ := newWikiPageRetryReducerWithDB(t)
	return svc, store, model
}

func newWikiPageRetryReducerWithDB(
	t *testing.T,
) (*wikiIngestService, *wikiPageRetryStore, *wikiPageRetryModel, *gorm.DB) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "pages.db")), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	require.NoError(t, db.AutoMigrate(&types.WikiFolder{}, &types.WikiPage{}, &types.WikiPageRevision{}))
	service := NewWikiPageService(apprepo.NewWikiPageRepository(db), nil, nil, nil, nil, nil)
	// 每轮使用独立数据库并关闭连接，重复 race 测试不会复用上轮页面。
	_, err = service.CreatePage(context.Background(), &types.WikiPage{
		TenantID: 1, KnowledgeBaseID: "kb-row-guard", Slug: "entity/alpha",
		Title: "Alpha", PageType: types.WikiPageTypeEntity, Content: "ALPHA_BASE",
	})
	require.NoError(t, err)
	store := newWikiPageRetryStore(service)
	return &wikiIngestService{wikiService: store, knowledgeSvc: liveKnowledgeStub{}}, store, newWikiPageRetryModel(), db
}

func runWikiPageRetryReducer(
	ctx context.Context, svc *wikiIngestService, model chat.Chat, updates []SlugUpdate, spans map[string]*Span,
) (bool, bool, error) {
	batch := svc.newWikiBatchContext("kb-row-guard", nil)
	changed, _, _, deferred, err := svc.reduceSlugUpdates(
		ctx, model, "kb-row-guard", "entity/alpha", updates, 1, batch, spans,
	)
	return changed, deferred, err
}

// 这里不是直接伪造冲突错误：首次模型输出后，另一作者通过真实服务写入，
// 随后的旧快照保存必须由真实服务/仓储拒绝；重试的模型必须看到新内容。
func TestWikiPageRetryRealConflictReloadsPageAndKeepsSingleSpan(t *testing.T) {
	svc, store, model := newWikiPageRetryReducer(t)
	tracker := &wikiPageRetryTracker{}
	svc.spanTracker = tracker
	store.beforeWrite = func(_ context.Context, page *types.WikiPage, call int) error {
		if call != 1 {
			return nil
		}
		fresh, err := store.GetPageBySlug(context.Background(), page.KnowledgeBaseID, page.Slug)
		if err != nil {
			return err
		}
		fresh.Content += "\nCONCURRENT_UPDATE"
		fresh.SourceRefs = append(fresh.SourceRefs, "other-source")
		_, err = store.WikiPageService.UpdatePage(context.Background(), fresh)
		return err
	}
	updates := append(wikiPageRetryUpdates(), SlugUpdate{
		Slug: "entity/alpha", Type: types.WikiPageTypeEntity,
		KnowledgeID: "second-source", SourceRef: "second-source", DocTitle: "Second source",
		Item: extractedItem{Name: "Alpha", Description: "Another contribution"},
	})
	changed, deferred, err := runWikiPageRetryReducer(context.Background(), svc, model, updates,
		map[string]*Span{"k-row-guard": {KnowledgeID: "k-row-guard"}})
	require.NoError(t, err)
	require.True(t, changed)
	require.False(t, deferred)
	require.Equal(t, 2, store.count("entity/alpha"), "冲突页只额外保存一次")
	require.ErrorIs(t, store.results["entity/alpha"][0], apprepo.ErrWikiPageConflict, "必须实际经历快照冲突")
	prompts := model.captured("wiki_page_modify:entity/alpha")
	require.Len(t, prompts, 2)
	require.NotContains(t, prompts[0], "CONCURRENT_UPDATE")
	require.Contains(t, prompts[1], "CONCURRENT_UPDATE", "第二次必须重新读取和归并，不可重放旧输出")
	page, err := store.GetPageBySlug(context.Background(), "kb-row-guard", "entity/alpha")
	require.NoError(t, err)
	require.Contains(t, page.Content, "NEW_CONTRIBUTION")
	require.Contains(t, page.Content, "CONCURRENT_UPDATE")
	require.ElementsMatch(t, []string{"other-source", "k-row-guard", "second-source"}, []string(page.SourceRefs))
	require.Equal(t, 1, tracker.starts, "有限重试仍属于一个页面任务")
	require.Equal(t, 1, tracker.ends)
	require.Zero(t, tracker.failures, "中途冲突不得把最终成功的页面标红")
}

func TestWikiPageRetryOnlyRetriesVersionConflicts(t *testing.T) {
	permanent := errors.New("database unavailable")
	for _, tc := range []struct {
		name     string
		writeErr error
		attempts int
		deferred bool
	}{
		{"冲突耗尽", fmt.Errorf("wrapped: %w", apprepo.ErrWikiPageConflict), 3, false},
		{"永久错误", permanent, 1, false},
		{"行数保护", ErrWikiWriteDroppedTableRows, 1, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, store, model := newWikiPageRetryReducer(t)
			tracker := &wikiPageRetryTracker{}
			svc.spanTracker = tracker
			store.beforeWrite = func(context.Context, *types.WikiPage, int) error { return tc.writeErr }
			_, deferred, err := runWikiPageRetryReducer(context.Background(), svc, model, wikiPageRetryUpdates(),
				map[string]*Span{"k-row-guard": {KnowledgeID: "k-row-guard"}})
			if tc.deferred {
				require.NoError(t, err)
			} else {
				require.ErrorIs(t, err, tc.writeErr)
			}
			require.Equal(t, tc.deferred, deferred)
			require.Equal(t, tc.attempts, store.count("entity/alpha"))
			require.Equal(t, tc.attempts, model.count("wiki_page_modify:entity/alpha"))
			require.Equal(t, 1, tracker.starts)
			if !tc.deferred {
				require.Equal(t, 1, tracker.failures, "耗尽后只记录一次最终失败")
			}
			page, readErr := store.GetPageBySlug(context.Background(), "kb-row-guard", "entity/alpha")
			require.NoError(t, readErr)
			require.Equal(t, "ALPHA_BASE", page.Content)
		})
	}
}

func TestWikiPageRetryCancellationDoesNotWriteOrStartAnotherAttempt(t *testing.T) {
	for _, phase := range []string{"调用前", "模型返回后", "首次冲突后"} {
		t.Run(phase, func(t *testing.T) {
			svc, store, model := newWikiPageRetryReducer(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			wantModels, wantWrites := 0, 0
			switch phase {
			case "调用前":
				cancel()
			case "模型返回后":
				wantModels = 1
				model.respond = func(_ context.Context, _, prompt string, _ int) (string, error) {
					cancel()
					return wikiPageRetryAnswer(prompt), nil
				}
			case "首次冲突后":
				wantModels, wantWrites = 1, 1
				store.beforeWrite = func(context.Context, *types.WikiPage, int) error {
					cancel()
					return apprepo.ErrWikiPageConflict
				}
			}
			_, _, err := runWikiPageRetryReducer(ctx, svc, model, wikiPageRetryUpdates(), nil)
			require.ErrorIs(t, err, context.Canceled)
			require.Equal(t, wantModels, model.count("wiki_page_modify:entity/alpha"))
			require.Equal(t, wantWrites, store.count("entity/alpha"), "取消后不能继续调用写入或下一轮模型")
			page, readErr := store.GetPageBySlug(context.Background(), "kb-row-guard", "entity/alpha")
			require.NoError(t, readErr)
			require.Equal(t, "ALPHA_BASE", page.Content)
		})
	}
}

type wikiPageRetryModelService struct {
	interfaces.ModelService
	model chat.Chat
}

func (s wikiPageRetryModelService) GetChatModel(context.Context, string) (chat.Chat, error) {
	return s.model, nil
}

// 原文切片由固定内存数据提供，调用计数用于证明局部重试没有重新执行 Map。
type wikiPageRetryChunks struct {
	interfaces.ChunkRepository
	reads atomic.Int32
}

func (s *wikiPageRetryChunks) ListChunksByKnowledgeIDAndTypes(
	_ context.Context, tenant uint64, kid string, _ []types.ChunkType,
) ([]*types.Chunk, error) {
	s.reads.Add(1)
	return []*types.Chunk{{
		ID: "chunk-source", TenantID: tenant, KnowledgeBaseID: "kb-1", KnowledgeID: kid,
		ChunkType: types.ChunkTypeText, IsEnabled: true,
		Content: strings.Repeat(
			"Alpha and Beta describe separate projects with enough source detail for Wiki extraction. ", 5,
		),
	}}, nil
}

func (*wikiPageRetryChunks) ListChunksByParentIDs(context.Context, uint64, []string) ([]*types.Chunk, error) {
	return nil, nil
}

func newWikiPageRetryBatch(
	t *testing.T, rdb *redis.Client,
) (*wikiIngestService, *gorm.DB, *wikiPageRetryStore, *wikiPageRetryModel, *wikiPageRetryChunks) {
	t.Helper()
	svc, db := newWikiLookupRecoveryService(t, "kb-1", rdb)
	require.NoError(t, db.AutoMigrate(
		&types.KnowledgeBase{}, &types.WikiFolder{}, &types.WikiPage{}, &types.WikiPageRevision{},
	))
	kb := svc.kbService.(*wikiLookupKBService).kb
	kb.TenantID = 7
	kb.WikiConfig = &types.WikiConfig{IngestMapParallel: 1, IngestReduceParallel: 2}
	require.NoError(t, db.Create(kb).Error)
	pageSvc := NewWikiPageService(apprepo.NewWikiPageRepository(db), nil, nil, nil, nil, nil)
	for _, name := range []string{"alpha", "beta"} {
		_, err := pageSvc.CreatePage(context.Background(), &types.WikiPage{
			TenantID: 7, KnowledgeBaseID: "kb-1", Slug: "entity/" + name,
			Title: strings.ToUpper(name[:1]) + name[1:], PageType: types.WikiPageTypeEntity,
			Content: strings.ToUpper(name) + "_BASE", Status: types.WikiPageStatusPublished,
		})
		require.NoError(t, err)
	}
	store, model, chunks := newWikiPageRetryStore(pageSvc), newWikiPageRetryModel(), &wikiPageRetryChunks{}
	svc.wikiService, svc.modelService, svc.chunkRepo = store, wikiPageRetryModelService{model: model}, chunks
	return svc, db, store, model, chunks
}

// 使用真实批次入口、pending 仓储和完成计数；不通过直接调用重试 helper 假装覆盖结算。
func TestWikiPageRetryBatchKeepsSuccessfulPagesAndSettlesDocument(t *testing.T) {
	for _, mode := range []string{"lite", "redis"} {
		for _, exhausted := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/exhausted=%t", mode, exhausted), func(t *testing.T) {
				var rdb *redis.Client
				if mode == "redis" {
					server := miniredis.RunT(t)
					rdb = redis.NewClient(&redis.Options{Addr: server.Addr()})
					t.Cleanup(func() { _ = rdb.Close() })
				}
				svc, db, store, model, chunks := newWikiPageRetryBatch(t, rdb)
				task := seedWikiLookupTask(t, svc, db, "kb-1", "k-1")
				store.beforeWrite = func(_ context.Context, page *types.WikiPage, call int) error {
					if page.Slug == "entity/alpha" && (exhausted || call == 1) {
						return fmt.Errorf("测试并发写入: %w", apprepo.ErrWikiPageConflict)
					}
					return nil
				}
				require.NoError(t, svc.ProcessWikiIngest(context.Background(), task))
				require.EqualValues(t, 1, chunks.reads.Load(), "单页冲突不能重新读取整篇切片并执行 Map")
				for _, purpose := range []string{"wiki_candidate_slug", "wiki_summary", "wiki_chunk_citation"} {
					require.Equal(t, 1, model.count(purpose), purpose+"只能运行一次")
				}
				require.Equal(t, 1, model.count("wiki_page_modify:entity/beta"), "已成功的页面不重算")
				require.Equal(t, 1, store.count("entity/beta"))
				wantAttempts := 2
				if exhausted {
					wantAttempts = 3
				}
				require.Equal(t, wantAttempts, model.count("wiki_page_modify:entity/alpha"))
				require.Equal(t, wantAttempts, store.count("entity/alpha"))
				var pending []types.TaskPendingOp
				require.NoError(t, db.Where("task_type = ?", wikiTaskType).Find(&pending).Error)
				knowledge, err := svc.knowledgeRepo.GetKnowledgeByIDOnly(context.Background(), "k-1")
				require.NoError(t, err)
				if exhausted {
					require.Len(t, pending, 1, "耗尽后保留原文档操作，交给既有持久重试")
					require.Equal(t, 1, pending[0].FailCount, "三次局部尝试只消耗一次文档失败预算")
					require.Nil(t, pending[0].ClaimedAt)
					require.Equal(t, 1, knowledge.PendingSubtasksCount, "还有失败页不能提前完成")
					require.Equal(t, types.ParseStatusFinalizing, knowledge.ParseStatus)
				} else {
					require.Empty(t, pending)
					require.Zero(t, knowledge.PendingSubtasksCount)
					require.Equal(t, types.ParseStatusCompleted, knowledge.ParseStatus)
				}
			})
		}
	}
}

// 模型已用旧来源生成整页时，不能只删掉 source_ref 就保存旧贡献。
func TestWikiPageRetrySourceDeletedDuringModelDoesNotWrite(t *testing.T) {
	svc, store, model := newWikiPageRetryReducer(t)
	var deleted atomic.Bool
	svc.knowledgeSvc = &wikiKnowledgeLookupStub{lookup: func(context.Context, string) (*types.Knowledge, error) {
		if deleted.Load() {
			return nil, apprepo.ErrKnowledgeNotFound
		}
		return &types.Knowledge{ID: "k-row-guard", ParseStatus: types.ParseStatusCompleted}, nil
	}}
	model.respond = func(_ context.Context, _, prompt string, _ int) (string, error) {
		deleted.Store(true)
		return wikiPageRetryAnswer(prompt), nil
	}
	_, _, err := runWikiPageRetryReducer(context.Background(), svc, model, wikiPageRetryUpdates(), nil)
	require.Error(t, err, "来源在生成期间消失，必须保留未应用信号")
	require.Zero(t, store.count("entity/alpha"))
	require.Equal(t, 1, model.count("wiki_page_modify:entity/alpha"))
	page, readErr := store.GetPageBySlug(context.Background(), "kb-row-guard", "entity/alpha")
	require.NoError(t, readErr)
	require.Equal(t, "ALPHA_BASE", page.Content)
}

// 历史行可能仍带内部引用；展示清理不能改变归并所用的原始快照，
// 否则没有其他写入也会误报冲突，并耗尽局部重试预算。
func TestWikiPageRetryLegacyCitationDoesNotCreateFalseConflict(t *testing.T) {
	svc, store, model, db := newWikiPageRetryReducerWithDB(t)
	require.NoError(t, db.Model(&types.WikiPage{}).
		Where("knowledge_base_id = ? AND slug = ?", "kb-row-guard", "entity/alpha").
		Updates(map[string]interface{}{"content": "ALPHA_BASE [c003]", "summary": "legacy summary [c004]"}).Error)
	var raw types.WikiPage
	require.NoError(t, db.Where("knowledge_base_id = ? AND slug = ?", "kb-row-guard", "entity/alpha").First(&raw).Error)
	require.Contains(t, raw.Content, "[c003]", "必须从真实旧行验证，不能由写入服务提前清理夹具")
	require.Contains(t, raw.Summary, "[c004]")
	shown, err := store.GetPageBySlug(context.Background(), "kb-row-guard", "entity/alpha")
	require.NoError(t, err)
	require.NotContains(t, shown.Content, "[c003]", "公开页面读取仍清理内部引用")
	require.NotContains(t, shown.Summary, "[c004]")
	shownByID, err := store.GetPageByID(context.Background(), raw.ID)
	require.NoError(t, err)
	require.NotContains(t, shownByID.Content, "[c003]")
	require.NotContains(t, shownByID.Summary, "[c004]")

	changed, deferred, err := runWikiPageRetryReducer(context.Background(), svc, model, wikiPageRetryUpdates(), nil)
	require.NoError(t, err)
	require.True(t, changed)
	require.False(t, deferred)
	require.Equal(t, 1, model.count("wiki_page_modify:entity/alpha"), "不存在并发写入，不应重新调用模型")
	require.Equal(t, 1, store.count("entity/alpha"))
	require.Len(t, store.results["entity/alpha"], 1)
	require.NoError(t, store.results["entity/alpha"][0])
	var saved types.WikiPage
	require.NoError(t, db.First(&saved, "id = ?", raw.ID).Error)
	require.Contains(t, saved.Content, "NEW_CONTRIBUTION")
	require.NotContains(t, saved.Content, "[c003]")
	require.NotContains(t, saved.Summary, "[c004]")
}

// 使用真实 span 仓储确认取消后的终态已落库；忽略 ctx 的内存替身
// 无法发现用已取消 context 收尾而导致页面永久 running 的问题。
func TestWikiPageRetryCancellationClosesPersistedPageSpan(t *testing.T) {
	for _, phase := range []string{"模型返回后", "首次冲突后"} {
		t.Run(phase, func(t *testing.T) {
			svc, store, model := newWikiPageRetryReducer(t)
			tracker, spanDB := setupSpanTrackerTest(t)
			sqlDB, err := spanDB.DB()
			require.NoError(t, err)
			sqlDB.SetMaxOpenConns(1)
			t.Cleanup(func() { _ = sqlDB.Close() })
			svc.spanTracker = tracker
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			_, attempt, err := tracker.OpenAttempt(ctx, "k-row-guard", "")
			require.NoError(t, err)
			stage := tracker.BeginStage(ctx, "k-row-guard", attempt, types.StagePostProcess, nil)
			require.NotNil(t, stage)
			parent := tracker.BeginSubSpan(ctx, stage, "postprocess.wiki", types.SpanKindSubSpan, nil)
			require.NotNil(t, parent)
			wantWrites := 0
			if phase == "模型返回后" {
				model.respond = func(_ context.Context, _, prompt string, _ int) (string, error) {
					cancel()
					return wikiPageRetryAnswer(prompt), nil
				}
			} else {
				wantWrites = 1
				store.beforeWrite = func(context.Context, *types.WikiPage, int) error {
					cancel()
					return apprepo.ErrWikiPageConflict
				}
			}
			_, _, err = runWikiPageRetryReducer(
				ctx, svc, model, wikiPageRetryUpdates(), map[string]*Span{"k-row-guard": parent},
			)
			require.ErrorIs(t, err, context.Canceled)
			require.Equal(t, 1, model.count("wiki_page_modify:entity/alpha"))
			require.Equal(t, wantWrites, store.count("entity/alpha"))
			var pageSpans []types.KnowledgeProcessingSpan
			require.NoError(t, spanDB.Where("knowledge_id = ? AND attempt = ? AND name = ?",
				"k-row-guard", attempt, "postprocess.wiki.page[entity/alpha]").Find(&pageSpans).Error)
			require.Len(t, pageSpans, 1, "取消后的局部尝试不能留下多个页面条目")
			require.Equal(t, types.SpanStatusFailed, pageSpans[0].Status, "终态必须用独立的收尾 context 写入")
			require.Equal(t, "REDUCE_FAILED", pageSpans[0].ErrorCode)
			require.NotNil(t, pageSpans[0].FinishedAt)
			require.Contains(t, pageSpans[0].ErrorDetail, context.Canceled.Error())
		})
	}
}
