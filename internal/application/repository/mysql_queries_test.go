//go:build mysqlintegration

package repository

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/testutil"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

func TestMySQLMCPMetadataRejectsStaleSnapshotsAndCountsTools(t *testing.T) {
	db := testutil.MySQL(t)
	r := &mcpServiceRepository{db: db}
	ctx := context.Background()
	require.NoError(t, db.Create(&types.MCPService{ID: "svc", TenantID: 1, Name: "test", TransportType: types.MCPTransportSSE}).Error)
	newer := &types.MCPMetadata{TenantID: 1, ServiceID: "svc", Principal: "user:a", ConfigFingerprint: "new", SyncedAt: time.Now().UTC().Truncate(time.Microsecond), Tools: []*types.MCPTool{{Name: "工具"}}}
	require.NoError(t, r.SaveMetadata(ctx, newer))
	older := *newer
	older.SyncedAt = newer.SyncedAt.Add(-time.Minute)
	older.Tools = []*types.MCPTool{}
	older.ConfigFingerprint = "old"
	require.NoError(t, r.SaveMetadata(ctx, &older))
	got, err := r.GetMetadata(ctx, 1, "svc", "user:a")
	require.NoError(t, err)
	require.Equal(t, "new", got.ConfigFingerprint)
	require.Len(t, got.Tools, 1)
	rows, err := r.ListMetadataSummaries(ctx, 1, []string{"user:a"})
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, 1, rows[0].ToolCount)
	newer.SyncedAt = newer.SyncedAt.Add(time.Minute)
	newer.Tools = []*types.MCPTool{}
	require.NoError(t, r.SaveMetadata(ctx, newer))
	got, err = r.GetMetadata(ctx, 1, "svc", "user:a")
	require.NoError(t, err)
	require.Empty(t, got.Tools)
	missing, err := r.GetMetadata(ctx, 2, "svc", "user:a")
	require.NoError(t, err)
	require.Nil(t, missing)
}

func TestMySQLWikiJSONSearchAndSourceReferences(t *testing.T) {
	db := testutil.MySQL(t)
	r := &wikiPageRepository{db: db}
	ctx := context.Background()
	page := &types.WikiPage{ID: "wiki-1", TenantID: 1, KnowledgeBaseID: "kb", Slug: "summary", Title: "Alpha 中文 100%", Content: "reference", PageType: types.WikiPageTypeSummary, Status: "published", SourceRefs: types.StringArray{"doc", "prefix|title"}, CategoryPath: types.StringArray{"目录"}, Depth: 1, InLinks: types.StringArray{}}
	require.NoError(t, r.Create(ctx, page))
	for _, query := range []string{"ALPHA", "中文", "100%"} {
		pages, total, err := r.List(ctx, &types.WikiPageListRequest{KnowledgeBaseID: "kb", Query: query, SortBy: "wiki_path"})
		require.NoError(t, err)
		require.EqualValues(t, 1, total)
		require.Len(t, pages, 1)
	}
	for _, id := range []string{"doc", "prefix"} {
		pages, err := r.ListBySourceRef(ctx, "kb", id)
		require.NoError(t, err)
		require.Len(t, pages, 1)
		slugs, err := r.ListSlugsBySourceRef(ctx, "kb", id)
		require.NoError(t, err)
		require.Equal(t, []string{"summary"}, slugs)
	}
	summaries, err := r.ListSummariesByKnowledgeIDs(ctx, "kb", []string{"doc", "prefix"})
	require.NoError(t, err)
	require.Equal(t, "reference", summaries["doc"])
	pages, total, err := r.List(ctx, &types.WikiPageListRequest{KnowledgeBaseID: "kb", CategoryPath: []string{"目录"}})
	require.NoError(t, err)
	require.EqualValues(t, 1, total)
	require.Len(t, pages, 1)
	var empty int64
	require.NoError(t, db.Model(&types.WikiPage{}).Where(r.wikiEmptyInLinksPredicate()).Count(&empty).Error)
	require.EqualValues(t, 1, empty)
	found, err := r.Search(ctx, "kb", "Alpha", 10)
	require.NoError(t, err)
	require.Len(t, found, 1)
	normalized, err := r.FindPagesByNormalizedTitle(ctx, "kb", types.WikiPageTypeSummary, "alpha中文100%")
	require.NoError(t, err)
	require.Len(t, normalized, 1)
	_, err = r.FindSimilarPages(ctx, "kb", "Alpha", []string{types.WikiPageTypeSummary}, 10)
	require.NoError(t, err)
}

func TestMySQLMemoryAndGeneratedSequenceIDs(t *testing.T) {
	db := testutil.MySQL(t)
	ctx := context.Background()
	r := &memoryRepository{db: db}
	scope := interfaces.MemoryScope{TenantID: 1, SubjectID: "web_user:a"}
	_, err := r.EnsureSubject(ctx, scope)
	require.NoError(t, err)
	item := &types.MemoryItem{ID: "mem-1", TenantID: 1, SubjectID: scope.SubjectID, Kind: "fact", Content: "remember", Importance: 3}
	require.NoError(t, r.CreateItem(ctx, item))
	require.NoError(t, r.UpsertItemEmbedding(ctx, scope, &types.MemoryItemEmbedding{ItemID: item.ID, ModelID: "embed", Dims: 2, Vector: types.EncodeEmbedding([]float32{1, 0})}))
	hits, err := r.SearchItemsByVector(ctx, scope, interfaces.MemoryVectorQuery{ModelID: "embed", Vector: []float32{1, 0}, Limit: 5, MinScore: 0.5})
	require.NoError(t, err)
	require.Len(t, hits, 1)
	missing, err := r.GetItem(ctx, interfaces.MemoryScope{TenantID: 2, SubjectID: scope.SubjectID}, item.ID)
	require.NoError(t, err)
	require.Nil(t, missing)
	chunks := []*types.Chunk{{ID: "chunk-a", TenantID: 1, KnowledgeBaseID: "kb", KnowledgeID: "doc", Content: "text", IsEnabled: true}, {ID: "chunk-b", TenantID: 1, KnowledgeBaseID: "kb", KnowledgeID: "doc", Content: "text", IsEnabled: true}}
	require.NoError(t, (&chunkRepository{db: db}).CreateChunks(ctx, chunks))
	require.Positive(t, chunks[0].SeqID)
	require.Greater(t, chunks[1].SeqID, chunks[0].SeqID)
	for _, chunk := range chunks {
		got, err := (&chunkRepository{db: db}).GetChunkBySeqID(ctx, 1, chunk.SeqID)
		require.NoError(t, err)
		require.Equal(t, chunk.ID, got.ID)
	}
	tag := &types.KnowledgeTag{ID: "tag-a", TenantID: 1, KnowledgeBaseID: "kb", Name: "tag"}
	require.NoError(t, db.Create(tag).Error)
	require.Positive(t, tag.SeqID)
	// Parent resources deleted between API validation and persistence cannot
	// become fresh bindings. The repository is the transaction boundary.
	storeID := "store-removed"
	require.NoError(t, db.Exec("INSERT INTO vector_stores (id, tenant_id, name, engine_type, deleted_at) VALUES (?,1,'removed','qdrant',CURRENT_TIMESTAMP(6))", storeID).Error)
	err = (&knowledgeBaseRepository{db: db}).CreateKnowledgeBase(ctx, &types.KnowledgeBase{ID: "bad-bind", TenantID: 1, Name: "invalid", VectorStoreID: &storeID})
	require.Error(t, err)
}

func TestMySQLForkBootstrapJSONTextAndNull(t *testing.T) {
	db := testutil.MySQL(t)
	require.NoError(t, db.Exec("INSERT INTO sessions (id, tenant_id, title, fork_bootstrap) VALUES (?, 1, 'fork', ?)", "s1", `{"snapshot_id":"snap","consumed_at":null}`).Error)
	r := &sessionRepository{db: db}
	ids, err := r.UnconsumedForkSnapshotHolders(context.Background(), "snap")
	require.NoError(t, err)
	require.Equal(t, []string{"s1"}, ids)
	require.NoError(t, db.Exec("UPDATE sessions SET fork_bootstrap = ? WHERE id = 's1'", `{"snapshot_id":"snap","consumed_at":"2026-10-13T00:00:00Z"}`).Error)
	ids, err = r.UnconsumedForkSnapshotHolders(context.Background(), "snap")
	require.NoError(t, err)
	require.Empty(t, ids)
}

func TestMySQLUserSearchAndGalleryProjection(t *testing.T) {
	db := testutil.MySQL(t)
	ctx := context.Background()
	require.NoError(t, db.Exec("INSERT INTO users (id, username, email, password_hash, is_active) VALUES ('user-1','Alice','alice@example.com','hash',true)").Error)
	users, err := (&userRepository{db: db}).SearchUsers(ctx, "ALICE", 10)
	require.NoError(t, err)
	require.Len(t, users, 1)
	r := &chunkRepository{db: db}
	require.NoError(t, db.Exec("INSERT INTO chunks (id, tenant_id, knowledge_base_id, knowledge_id, content, chunk_index, start_at, end_at, image_info, is_enabled) VALUES ('image-1',1,'kb','doc','caption',0,0,1,?,true)", `[{"url":"image.png","caption":"Alpha 中文","attrs":{"attrs":{"subject":"Cat","observed":true}}}]`).Error)
	var count int64
	require.NoError(t, db.Table("chunk_images").Count(&count).Error)
	require.EqualValues(t, 1, count, "real schema triggers maintain gallery projection")
	rows, total, err := r.ListImageAssets(ctx, 1, "kb", &types.ImageAssetQuery{Keyword: "cat", SearchFields: []types.ImageAssetField{{Attr: "subject"}}})
	require.NoError(t, err)
	require.EqualValues(t, 1, total)
	require.Len(t, rows, 1)
	require.NoError(t, db.Exec("UPDATE chunks SET is_enabled=false WHERE id='image-1'").Error)
	var enabled bool
	require.NoError(t, db.Table("chunk_images").Select("is_enabled").Scan(&enabled).Error)
	require.False(t, enabled)
	require.NoError(t, db.Exec("UPDATE chunks SET deleted_at=CURRENT_TIMESTAMP(6) WHERE id='image-1'").Error)
	require.NoError(t, db.Table("chunk_images").Count(&count).Error)
	require.Zero(t, count)
}

func TestMySQLSkillEnvironmentModelUsageAndMemoryLease(t *testing.T) {
	db := testutil.MySQL(t)
	ctx := context.Background()
	t.Setenv("SYSTEM_AES_KEY", "0123456789abcdef0123456789abcdef")
	skills := &tenantSkillRepository{db: db}
	skill := &types.TenantSkillEntity{ID: "skill", TenantID: 1, SandboxConfigID: "cfg", Name: "parser", Status: types.SkillStatusReady}
	require.NoError(t, skills.CreateSkill(ctx, skill))
	require.NoError(t, skills.UpdateSkillEnvs(ctx, 1, "cfg", "skill", types.SkillEnvVars{{Name: "KEY", Value: "secret"}}))
	got, err := skills.GetSkill(ctx, 1, "cfg", "skill")
	require.NoError(t, err)
	require.Equal(t, "secret", got.Envs[0].Value)
	var raw string
	require.NoError(t, db.Table("tenant_skills").Select("envs").Where("id='skill'").Scan(&raw).Error)
	require.NotContains(t, raw, "secret")
	p := types.Principal{Type: "im_user", ID: strings.Repeat("p", 512)}
	entry := &types.TenantUserEnvVar{TenantID: 1, PrincipalType: p.Type, PrincipalID: p.ID, SandboxConfigID: "cfg", SkillID: "skill", Name: strings.Repeat("n", 255), Value: "old"}
	require.NoError(t, skills.UpsertUserEnvVar(ctx, entry))
	entry.Value = "new"
	require.NoError(t, skills.UpsertUserEnvVar(ctx, entry))
	vars, err := skills.ListUserEnvVars(ctx, 1, p, "cfg", "skill")
	require.NoError(t, err)
	require.Len(t, vars, 1)
	require.Equal(t, "new", vars[0].Value)
	vars, err = skills.ListUserEnvVars(ctx, 2, p, "cfg", "skill")
	require.NoError(t, err)
	require.Empty(t, vars)
	require.NoError(t, db.Exec("INSERT INTO knowledge_bases (id,tenant_id,name,embedding_model_id,summary_model_id,wiki_config) VALUES ('usage',1,'usage','','',?)", `{"synthesis_model_id":"model-a"}`).Error)
	count, err := (&knowledgeBaseRepository{db: db}).CountByModelID(ctx, 1, "model-a")
	require.NoError(t, err)
	require.EqualValues(t, 1, count)
	require.NoError(t, db.Exec("INSERT INTO organizations (id,name,owner_id,searchable) VALUES ('org','Alpha','user',true)").Error)
	orgs, err := (&organizationRepository{db: db}).ListSearchable(ctx, "ALPHA", 10)
	require.NoError(t, err)
	require.Len(t, orgs, 1)
	r := &memoryRepository{db: db}
	scope := interfaces.MemoryScope{TenantID: 1, SubjectID: strings.Repeat("s", 512)}
	_, err = r.EnsureSubject(ctx, scope)
	require.NoError(t, err)
	_, _, err = r.EnqueuePendingSession(ctx, scope, "session", time.Minute)
	require.NoError(t, err)
	start := make(chan struct{})
	batches := make(chan *types.MemoryExtractionBatch, 2)
	errs := make(chan error, 2)
	var group sync.WaitGroup
	for _, lease := range []string{"worker-a", "worker-b"} {
		group.Add(1)
		go func(lease string) {
			defer group.Done()
			<-start
			batch, err := r.ClaimPendingSessions(ctx, scope, "", lease, time.Minute)
			batches <- batch
			errs <- err
		}(lease)
	}
	close(start)
	group.Wait()
	close(batches)
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	winners := 0
	var session types.MemoryExtractionSession
	for batch := range batches {
		require.NotNil(t, batch)
		if len(batch.Sessions) > 0 {
			winners++
			require.Len(t, batch.Sessions, 1)
			session = batch.Sessions[0]
		} else {
			require.False(t, batch.RetryAt.IsZero())
		}
	}
	require.Equal(t, 1, winners)
	subject, err := r.GetSubject(ctx, scope)
	require.NoError(t, err)
	lease := subject.ExtractionState.LeaseID
	_, err = r.RecordExtractionFailure(ctx, scope, lease, interfaces.MemoryExtractionFailure{Session: session, Code: "retry"})
	require.NoError(t, err)
	require.NoError(t, r.CheckpointExtraction(ctx, scope, lease, session, types.MemoryMessageCursor{}, false))
	var unset bool
	require.NoError(t, db.Raw("SELECT cursor_at IS NULL AND failed_from_at IS NULL AND failed_to_at IS NULL FROM memory_extraction_sessions WHERE tenant_id=? AND subject_id=?", scope.TenantID, scope.SubjectID).Scan(&unset).Error)
	require.True(t, unset)
	require.NoError(t, r.FinishExtraction(ctx, scope, lease))
	require.ErrorIs(t, r.CheckpointExtraction(ctx, scope, lease, session, types.MemoryMessageCursor{}, true), types.ErrMemoryExtractionLeaseLost)
	for range 2 {
		stat, err := r.BumpTopic(ctx, scope, "topic", strings.Repeat("t", 255), "")
		require.NoError(t, err)
		require.NotNil(t, stat)
	}
	stat, err := r.TopicByKey(ctx, scope, strings.Repeat("t", 255))
	require.NoError(t, err)
	require.EqualValues(t, 2, stat.Hits)
}

func TestMySQLBusinessRecordsAndMessageArtifacts(t *testing.T) {
	db := testutil.MySQL(t)
	ctx := context.Background()
	t.Setenv("RETRIEVE_DRIVER", "")
	tenant := &types.Tenant{Name: "workspace", Business: "test"}
	tenants := &tenantRepository{db: db}
	require.NoError(t, tenants.CreateTenant(ctx, tenant))
	require.Positive(t, tenant.ID)
	loaded, err := tenants.GetTenantByID(ctx, tenant.ID)
	require.NoError(t, err)
	require.Equal(t, tenant.Name, loaded.Name)
	kbs := &knowledgeBaseRepository{db: db}
	kb := &types.KnowledgeBase{ID: "kb", TenantID: tenant.ID, Name: "knowledge base"}
	require.NoError(t, kbs.CreateKnowledgeBase(ctx, kb))
	kb.Name = "updated"
	require.NoError(t, kbs.UpdateKnowledgeBase(ctx, kb))
	require.NoError(t, kbs.UpdateKnowledgeBase(ctx, kb), "unchanged Save must succeed")
	knowledge := &types.Knowledge{ID: "document", TenantID: tenant.ID, KnowledgeBaseID: kb.ID, Title: "中文 100%", Type: "manual", Source: "text", ParseStatus: types.ParseStatusCompleted}
	knowledges := &knowledgeRepository{db: db}
	require.NoError(t, knowledges.CreateKnowledge(ctx, knowledge))
	got, err := knowledges.GetKnowledgeByID(ctx, tenant.ID, knowledge.ID)
	require.NoError(t, err)
	require.Equal(t, knowledge.Title, got.Title)
	_, err = knowledges.GetKnowledgeByID(ctx, tenant.ID+1, knowledge.ID)
	require.ErrorIs(t, err, ErrKnowledgeNotFound)
	sessions := &sessionRepository{db: db}
	session, err := sessions.Create(ctx, &types.Session{ID: "conversation", TenantID: tenant.ID, Title: "chat"})
	require.NoError(t, err)
	messages := &messageRepository{db: db}
	message, err := messages.CreateMessage(ctx, &types.Message{ID: "answer", SessionID: session.ID, Role: "assistant", Content: "answer", Artifacts: types.MessageArtifacts{{URL: "local://file", FileName: "report.txt", FileType: ".txt", FileSize: 10, CreatedAt: time.Now().UTC()}}})
	require.NoError(t, err)
	gotMessage, err := messages.GetMessage(ctx, session.ID, message.ID)
	require.NoError(t, err)
	require.Equal(t, message.Content, gotMessage.Content)
	require.Len(t, gotMessage.Artifacts, 1)
	require.Equal(t, "report.txt", gotMessage.Artifacts[0].FileName)
	_, err = sessions.Get(ctx, tenant.ID+1, "", session.ID)
	require.Error(t, err)
	require.NoError(t, kbs.DeleteKnowledgeBase(ctx, kb.ID))
	_, err = kbs.GetKnowledgeBaseByIDAndTenant(ctx, kb.ID, tenant.ID)
	require.ErrorIs(t, err, ErrKnowledgeBaseNotFound)
}

func TestMySQLSuggestionGenerationHasOneOwner(t *testing.T) {
	db := testutil.MySQL(t)
	require.NoError(t, db.Exec("INSERT INTO tenants (id,name,business) VALUES (1,'test','test')").Error)
	r := &messageSuggestionRepository{db: db}
	ctx := context.Background()
	start := make(chan struct{})
	type outcome struct {
		set  *types.MessageSuggestionSet
		owns bool
		err  error
	}
	results := make(chan outcome, 2)
	for range 2 {
		go func() {
			<-start
			set, owns, err := r.AcquireGeneration(ctx, &types.MessageSuggestionSet{TenantID: 1, SessionID: "session", AssistantMessageID: "message", Placement: "follow_up", ConfigHash: "hash", Locale: "zh"}, false)
			results <- outcome{set, owns, err}
		}()
	}
	close(start)
	a, b := <-results, <-results
	require.NoError(t, a.err)
	require.NoError(t, b.err)
	require.NotEqual(t, a.owns, b.owns, "only one caller acquires the generation lease")
	require.Equal(t, a.set.ID, b.set.ID, "both return the persisted cache winner")
	a.set.Status = types.SuggestionStatusReady
	require.NoError(t, r.Save(ctx, a.set))
	set, owns, err := r.AcquireGeneration(ctx, &types.MessageSuggestionSet{TenantID: 1, SessionID: "session", AssistantMessageID: "message", Placement: "follow_up", ConfigHash: "hash", Locale: "zh"}, false)
	require.NoError(t, err)
	require.False(t, owns)
	require.Equal(t, a.set.ID, set.ID)
}
