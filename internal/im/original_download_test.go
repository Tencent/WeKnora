package im

import (
	"context"
	"errors"
	"github.com/Tencent/WeKnora/internal/event"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

type originalKnowledgeStub struct {
	interfaces.KnowledgeService
	k *types.Knowledge
}

func (d originalKnowledgeStub) GetKnowledgeByID(ctx context.Context, id string) (*types.Knowledge, error) {
	tenant, _ := types.TenantIDFromContext(ctx)
	if d.k.ID != id || d.k.TenantID != tenant {
		return nil, errors.New("missing")
	}
	return d.k, nil
}

type originalKBStub struct {
	interfaces.KnowledgeBaseService
	kb *types.KnowledgeBase
}

func (d originalKBStub) GetKnowledgeBaseByIDOnly(_ context.Context, id string) (*types.KnowledgeBase, error) {
	if id != d.kb.ID {
		return nil, errors.New("missing")
	}
	return d.kb, nil
}

type originalAgentStub struct {
	interfaces.CustomAgentService
	agent *types.CustomAgent
}

func (d originalAgentStub) GetAgentByID(_ context.Context, id string) (*types.CustomAgent, error) {
	if id != d.agent.ID {
		return nil, errors.New("missing")
	}
	return d.agent, nil
}

type originalTenantStub struct{ interfaces.TenantService }

func (originalTenantStub) GetTenantByID(_ context.Context, id uint64) (*types.Tenant, error) {
	return &types.Tenant{ID: id}, nil
}

type originalCatalogStub struct {
	interfaces.ResourceCatalog
	r *types.StoredResource
}

func (d originalCatalogStub) Resolve(_ context.Context, ref string) (*types.StoredResource, error) {
	if ref != types.BuildResourcePath(d.r.Handle) {
		return nil, errors.New("missing")
	}
	return d.r, nil
}

type originalStorageStub struct {
	interfaces.StorageBackendResolver
	fs *originalFSStub
}

func (d originalStorageStub) ResolveFileService(
	_ context.Context, tenant *types.Tenant, backend, provider, _ string,
) (interfaces.FileService, string, error) {
	if tenant.ID != 7 || backend != "source-store" || provider != "local" {
		return nil, "", errors.New("wrong instance")
	}
	return d.fs, "local", nil
}

type originalFSStub struct {
	interfaces.FileService
	reads int
	file  string
}

func (f *originalFSStub) GetFile(_ context.Context, path string) (io.ReadCloser, error) {
	if path != "local://7/file/source.pdf" {
		return nil, errors.New("wrong object")
	}
	f.reads++
	return os.Open(f.file)
}

const citedAnswer = `quoted <kb kb_id="kb" chunk_id="chunk" />`

type originalFixture struct {
	service      *Service
	admin, imCtx context.Context
	channel      *IMChannel
	agent        *types.CustomAgent
	kb           *types.KnowledgeBase
	doc          *types.Knowledge
	resource     *types.StoredResource
	refs         []*types.SearchResult
	redis        *miniredis.Miniredis
	file         *originalFSStub
}

func newOriginalFixture(t *testing.T) *originalFixture {
	t.Helper()
	t.Setenv("APP_EXTERNAL_URL", "https://files.example.test/tools")
	db := newLifecycleTestDB(t)
	require.NoError(t, db.AutoMigrate(&originalSharing{}, &types.TenantMember{}, &types.ResourceBinding{}))
	const userTable = "CREATE TABLE users (id TEXT PRIMARY KEY, is_active BOOLEAN, deleted_at DATETIME)"
	require.NoError(t, db.Exec(userTable).Error)
	require.NoError(t, db.Exec("INSERT INTO users (id,is_active) VALUES ('owner',true)").Error)
	require.NoError(t, db.Create(&types.TenantMember{UserID: "owner", TenantID: 7, Role: types.TenantRoleAdmin,
		Status: types.TenantMemberStatusActive,
	}).Error)
	ch := &IMChannel{ID: "channel", TenantID: 7, AgentID: "agent", Platform: "feishu", Enabled: true,
		Credentials: types.JSON("{}"),
	}
	require.NoError(t, db.Create(ch).Error)
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	resource := &types.StoredResource{
		ID: "stored", Handle: strings.Repeat("a", types.ResourceHandleLength), TenantID: 7,
		PhysicalPath: "local://7/file/source.pdf", Provider: "local", StorageBackendID: "source-store",
		State: types.ResourceStateActive,
	}
	require.NoError(t, db.Create(&types.ResourceBinding{ResourceID: resource.ID, TenantID: 7,
		OwnerType: types.ResourceOwnerKnowledge, OwnerID: "doc", Relation: types.ResourceRelationSourceFile,
	}).Error)
	doc := &types.Knowledge{ID: "doc", KnowledgeBaseID: "kb", TenantID: 7,
		FilePath: types.BuildResourcePath(resource.Handle), FileName: "file.pdf",
	}
	kb := &types.KnowledgeBase{ID: "kb", TenantID: 7}
	agent := &types.CustomAgent{ID: "agent", TenantID: 7}
	agent.Config.KBSelectionMode = "selected"
	agent.Config.KnowledgeBases = []string{"kb"}
	fs := &originalFSStub{file: filepath.Join(t.TempDir(), "file.pdf")}
	require.NoError(t, os.WriteFile(fs.file, []byte("original bytes"), 0600))
	s := Service{
		db: db, redis: client, knowledgeService: originalKnowledgeStub{k: doc},
		kbService: originalKBStub{kb: kb}, agentService: originalAgentStub{agent: agent},
		tenantService: originalTenantStub{}, resourceCatalog: originalCatalogStub{r: resource},
		storageResolver: originalStorageStub{fs: fs},
	}
	tenantCtx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(7))
	admin := types.WithCaller(tenantCtx, types.Caller{TenantID: 7, UserID: "owner", Role: types.TenantRoleAdmin})
	channelIdentity := originalChannel{id: ch.ID, agentID: ch.AgentID}
	imCtx := context.WithValue(withIMIdentity(tenantCtx, ch, nil), originalChannelKey{}, channelIdentity)
	refs := []*types.SearchResult{{ID: "chunk", KnowledgeID: doc.ID, KnowledgeBaseID: kb.ID}}
	return &originalFixture{
		service: &s, admin: admin, imCtx: imCtx, channel: ch, agent: agent, kb: kb, doc: doc, resource: resource,
		refs: refs, redis: mr, file: fs,
	}
}

func TestFeishuOriginalSharing(t *testing.T) {
	for _, scenario := range []string{
		"download", "expired", "modified token", "query token", "off then on", "disabled channel",
		"changed agent", "scope narrowed", "missing admin", "inactive user", "document moved", "resource replaced",
		"other resource tenant", "missing storage binding", "removed source binding", "expired resource",
		"channel off then on",
	} {
		t.Run(scenario, func(t *testing.T) {
			f := newOriginalFixture(t)
			s, ch, admin, imCtx, refs := f.service, f.channel, f.admin, f.imCtx, f.refs
			db := s.db
			require.Empty(t, s.originalDownloadLinks(imCtx, citedAnswer, refs))
			require.Error(t, s.ConfigureOriginalSharing(imCtx, ch, "kb"))
			f.kb.TenantID = 8
			require.Error(t, s.ConfigureOriginalSharing(admin, ch, "kb"))
			f.kb.TenantID = 7
			require.NoError(t, s.ConfigureOriginalSharing(admin, ch, "kb"))
			require.Empty(t, s.originalDownloadLinks(imCtx, "no citation", refs))
			require.Empty(t, s.originalDownloadLinks(imCtx, `<kb kb_id="other" chunk_id="chunk"/>`, refs))
			link := s.originalDownloadLinks(imCtx, citedAnswer+citedAnswer, refs)
			require.Equal(t, 1, strings.Count(link, "https://"))
			token := strings.TrimSuffix(strings.Split(link, "#")[1], ")")
			require.Equal(t, originalTTL, f.redis.TTL(originalGrantKey(token)))
			want := http.StatusNotFound
			switch scenario {
			case "download":
				want = http.StatusOK
			case "expired":
				f.redis.FastForward(time.Hour)
			case "modified token":
				token += "1"
			case "off then on":
				require.NoError(t, s.ConfigureOriginalSharing(admin, ch, ""))
				require.NoError(t, s.ConfigureOriginalSharing(admin, ch, "kb"))
			case "disabled channel":
				require.NoError(t, db.Model(ch).Update("enabled", false).Error)
			case "removed source binding":
				require.NoError(t, db.Where("resource_id = ?", f.resource.ID).Delete(&types.ResourceBinding{}).Error)
			case "expired resource":
				past := time.Now().Add(-time.Hour)
				f.resource.ExpiresAt = &past
			case "channel off then on":
				require.NoError(t, db.Model(ch).Update("enabled", false).Error)
				require.NoError(t, db.Model(ch).Update("enabled", true).Error)
			case "changed agent":
				f.agent.UpdatedAt = time.Now()
			case "scope narrowed":
				f.agent.Config.KnowledgeBases = nil
			case "missing admin":
				require.NoError(t, db.Model(&types.TenantMember{}).Where("user_id = ?", "owner").
					Update("role", types.TenantRoleViewer).Error)
			case "inactive user":
				require.NoError(t, db.Exec("UPDATE users SET is_active=false").Error)
			case "document moved":
				f.doc.KnowledgeBaseID = "other"
			case "resource replaced":
				f.resource.ContentHash = "new hash"
			case "other resource tenant":
				f.resource.TenantID = 8
			case "missing storage binding":
				f.resource.StorageBackendID = ""
			}
			body := url.Values{"token": {token}}.Encode()
			path := "/api/v1/im/citation-download"
			if scenario == "query token" {
				path += "?" + body
				body = ""
			}
			r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			w := httptest.NewRecorder()
			s.ServeOriginalDownload(w, r)
			require.Equal(t, want, w.Code)
			require.Equal(t, "private, no-store", w.Header().Get("Cache-Control"))
			if want == http.StatusOK {
				require.Equal(t, "original bytes", w.Body.String())
				require.Equal(t, 1, f.file.reads)
				require.Contains(t, w.Header().Get("Content-Disposition"), "attachment")
			} else {
				require.Zero(t, f.file.reads)
			}
		})
	}
}

func TestOriginalSharingRejectsReboundRequest(t *testing.T) {
	f := newOriginalFixture(t)
	require.NoError(t, f.service.ConfigureOriginalSharing(f.admin, f.channel, f.kb.ID))
	queued := originalChannel{id: f.channel.ID, agentID: "previous-agent"}
	ctx := context.WithValue(f.imCtx, originalChannelKey{}, queued)
	require.Empty(t, f.service.originalDownloadLinks(ctx, citedAnswer, f.refs))
}

func TestOriginalDownloadLanding(t *testing.T) {
	w := httptest.NewRecorder()
	(&Service{}).ServeOriginalDownload(w, httptest.NewRequest(http.MethodGet, "/api/v1/im/citation-download", nil))
	require.Equal(t, http.StatusOK, w.Code)
	require.Contains(t, w.Body.String(), `method="post"`)
	require.Contains(t, w.Header().Get("Content-Security-Policy"), "form-action 'self'")
}

type referencedAnswerSession struct {
	fullOutputSessionService
	refs []*types.SearchResult
}

func (s *referencedAnswerSession) KnowledgeQA(ctx context.Context, req *types.QARequest, bus *event.EventBus) error {
	if err := bus.Emit(ctx, event.Event{ID: "references", Type: event.EventAgentReferences, SessionID: req.Session.ID,
		Data: event.AgentReferencesData{References: s.refs}}); err != nil {
		return err
	}
	return s.fullOutputSessionService.KnowledgeQA(ctx, req, bus)
}

func TestOriginalDownloadDelivery(t *testing.T) {
	for _, mode := range []string{"full", "stream"} {
		t.Run(mode, func(t *testing.T) {
			f := newOriginalFixture(t)
			require.NoError(t, f.service.ConfigureOriginalSharing(f.admin, f.channel, f.kb.ID))
			f.channel.OutputMode = mode
			_, adapter, session, _ := newFullOutputHarness(citedAnswer)
			messages := &deliveryMessageService{}
			f.service.sessionService = &referencedAnswerSession{fullOutputSessionService: *session, refs: f.refs}
			f.service.messageService = messages
			f.service.streamManager = &fullOutputStreamManager{}
			ctx, cancel := context.WithCancel(withIMIdentity(context.Background(), f.channel, nil))
			f.service.executeQARequest(&qaRequest{
				ctx: ctx, cancel: cancel, channel: f.channel, channelID: f.channel.ID,
				adapter: adapter, userKey: "user",
				tenant: &types.Tenant{ID: 7}, session: &types.Session{ID: "s"},
				msg: &IncomingMessage{Platform: PlatformFeishu, UserID: "u", Content: "question"},
			})
			require.Contains(t, adapter.finalContent, "https://files.example.test/tools/api/v1/im/citation-download#")
			require.NotNil(t, messages.saved)
			require.Equal(t, citedAnswer, messages.saved.Content)
		})
	}
}
