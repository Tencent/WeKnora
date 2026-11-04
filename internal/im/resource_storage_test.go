package im

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/application/repository"
	appsvc "github.com/Tencent/WeKnora/internal/application/service"
	filesvc "github.com/Tencent/WeKnora/internal/application/service/file"
	"github.com/Tencent/WeKnora/internal/event"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// Use the real catalog, backend repository, resolver and OSS signer. Only the
// storage HTTP server and IM delivery are local stand-ins; no cloud account or
// platform credentials are needed to verify which instance signs each image.
type imResourceStorageFixture struct {
	db      *gorm.DB
	catalog interfaces.ResourceCatalog
	storage interfaces.StorageBackendResolver
	tenants interfaces.TenantService
	shares  interfaces.KBShareService
	global  interfaces.FileService
	tenant  *types.Tenant
	answer  string
	refs    []string
	baseURL string
}

func newIMResourceStorageFixture(t *testing.T) *imResourceStorageFixture {
	t.Helper()
	t.Setenv("STORAGE_TYPE", "local")
	t.Setenv("APP_EXTERNAL_URL", "")
	t.Setenv("SSRF_WHITELIST", "127.0.0.1")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodHead && r.Method != http.MethodPut {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(server.Close)
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
	require.NoError(t, db.AutoMigrate(
		&types.Tenant{}, &types.StorageBackend{}, &types.StoredResource{},
		&types.ResourceBinding{}, &types.ResourceAccessGrant{},
		&types.KnowledgeBase{}, &types.Knowledge{}, &types.Organization{},
		&types.OrganizationTenantMember{}, &types.KnowledgeBaseShare{},
	))
	for _, id := range []uint64{7, 9} {
		require.NoError(t, db.Create(&types.Tenant{ID: id, Name: fmt.Sprintf("Workspace %d", id)}).Error)
	}
	storageRepo := repository.NewStorageBackendRepository(db)
	catalog := appsvc.NewResourceCatalog(repository.NewResourceRepository(db))
	f := &imResourceStorageFixture{
		db: db, catalog: catalog, storage: appsvc.NewStorageBackendService(storageRepo, db, catalog),
		tenants: appsvc.NewTenantService(repository.NewTenantRepository(db), storageRepo, nil),
		global:  filesvc.NewResourceCatalogFileService(filesvc.NewLocalFileService(t.TempDir(), ""), catalog),
		tenant:  &types.Tenant{ID: 7}, baseURL: server.URL,
	}
	f.shares = appsvc.NewKBShareService(repository.NewKBShareRepository(db),
		repository.NewOrganizationRepository(db), repository.NewKnowledgeBaseRepository(db), nil, nil, nil)
	require.NoError(t, db.Create(&types.Organization{ID: "org", Name: "Shared workspace", OwnerTenantID: 9}).Error)
	for _, id := range []uint64{7, 9} {
		require.NoError(t, db.Create(&types.OrganizationTenantMember{
			ID: fmt.Sprintf("member-%d", id), OrganizationID: "org", TenantID: id, Role: types.OrgRoleViewer,
		}).Error)
	}
	require.NoError(t, db.Create(&types.KnowledgeBase{ID: "shared-kb", TenantID: 9, Name: "Shared KB"}).Error)
	require.NoError(t, db.Create(&types.Knowledge{ID: "shared-doc", TenantID: 9, KnowledgeBaseID: "shared-kb"}).Error)
	require.NoError(t, db.Create(&types.KnowledgeBaseShare{
		ID: "share", KnowledgeBaseID: "shared-kb", OrganizationID: "org",
		SourceTenantID: 9, Permission: types.OrgRoleViewer,
	}).Error)
	for i, owner := range []uint64{7, 7, 9} {
		backend := &types.StorageBackend{
			ID: fmt.Sprintf("oss-%d", i), TenantID: owner, Name: fmt.Sprintf("OSS %d", i), Provider: "oss",
			Config: types.StorageBackendConfig{
				Endpoint: server.URL, Region: "cn-hangzhou", BucketName: fmt.Sprintf("bucket-%d", i),
				AccessKeyID: fmt.Sprintf("instance-key-%d", i), SecretAccessKey: "test-only-secret",
			},
		}
		require.NoError(t, storageRepo.Create(context.Background(), backend))
		physical := fmt.Sprintf("storage://%s/oss://bucket-%d/%d/exports/image.png", backend.ID, i, owner)
		ref, err := catalog.Register(context.Background(), owner, physical,
			interfaces.ResourceRegistration{Kind: "image", OriginalName: "image.png"})
		require.NoError(t, err)
		if owner == 9 {
			require.NoError(t, catalog.Bind(context.Background(), ref,
				types.ResourceOwnerKnowledge, "shared-doc", types.ResourceRelationExtractedImage))
		}
		f.refs = append(f.refs, ref)
		f.answer += fmt.Sprintf("![image %d](%s)\n", i, ref)
	}
	return f
}

func (f *imResourceStorageFixture) wire(s *Service) {
	s.defaultFileSvc = f.global
	s.resourceCatalog = f.catalog
	s.storageResolver = f.storage
	s.tenantService = f.tenants
	s.kbService = &resourceKBLookup{db: f.db}
	s.kbShareService = f.shares
}

type resourceKBLookup struct {
	interfaces.KnowledgeBaseService
	db *gorm.DB
}

func (s *resourceKBLookup) GetKnowledgeBaseByIDOnly(ctx context.Context, id string) (*types.KnowledgeBase, error) {
	var kb types.KnowledgeBase
	if err := s.db.WithContext(ctx).Where("id = ?", id).First(&kb).Error; err != nil {
		return nil, err
	}
	return &kb, nil
}

func (f *imResourceStorageFixture) context() context.Context {
	return withIMIdentity(context.Background(), &IMChannel{TenantID: f.tenant.ID},
		&IncomingMessage{Platform: PlatformWeCom, UserID: "test-user"})
}

func (f *imResourceStorageFixture) checkSignedImages(t *testing.T, content string) {
	t.Helper()
	require.NotContains(t, content, "resource://")
	images := regexp.MustCompile(`!\[[^\]]*\]\(([^)]+)\)`).FindAllStringSubmatch(content, -1)
	require.Len(t, images, 3)
	for i, match := range images {
		u, err := url.Parse(match[1])
		require.NoError(t, err)
		require.Equal(t, strings.TrimPrefix(f.baseURL, "http://"), u.Host)
		require.Contains(t, u.Path, fmt.Sprintf("bucket-%d/", i))
		require.Contains(t, u.Query().Get("x-oss-credential"), fmt.Sprintf("instance-key-%d/", i))
		require.NotEmpty(t, u.Query().Get("x-oss-signature"))
	}
}

func TestIMDeliveryUsesResourceStorageAcrossPlatformsAndModes(t *testing.T) {
	f := newIMResourceStorageFixture(t)
	checkIMDeliveryModes(t, f, f.checkSignedImages)
}

func TestIMDeliveryDoesNotSignUnsharedResourcesAcrossPlatformsAndModes(t *testing.T) {
	f := newIMResourceStorageFixture(t)
	require.NoError(t, f.db.Where("id = ?", "share").Delete(&types.KnowledgeBaseShare{}).Error)
	f.answer = "![private image](" + f.refs[2] + ")"
	checkIMDeliveryModes(t, f, func(t *testing.T, content string) {
		require.Equal(t, f.answer, content)
	})
}

func checkIMDeliveryModes(t *testing.T, f *imResourceStorageFixture, check func(*testing.T, string)) {
	t.Helper()
	platforms := []Platform{
		PlatformWeCom, PlatformFeishu, PlatformLark, PlatformDingtalk,
		PlatformSlack, PlatformTelegram, PlatformMattermost, PlatformWeChat, PlatformQQBot, PlatformYunzhijia,
	}
	modes := []string{"stream", "full", "full without progress", "stream start failure", "stream finalize failure"}
	for _, platform := range platforms {
		for _, mode := range modes {
			t.Run(string(platform)+"/"+mode, func(t *testing.T) {
				s, adapter, _, _ := newFullOutputHarness(f.answer)
				f.wire(s)
				messages := &deliveryMessageService{}
				s.messageService = messages
				channel := &IMChannel{OutputMode: "stream"}
				var sender Adapter = adapter
				switch mode {
				case "full":
					channel.OutputMode = "full"
				case "full without progress":
					channel.OutputMode = "full"
					sender = &fullOutputNoProgressAdapter{fullOutputAdapter: adapter}
				case "stream start failure":
					adapter.startErr = errors.New("stream unavailable")
				case "stream finalize failure":
					adapter.finalizeErr = errors.New("update failed")
				}
				ctx, cancel := context.WithCancel(f.context())
				defer cancel()
				s.executeQARequest(&qaRequest{
					ctx: ctx, cancel: cancel, tenant: f.tenant, channel: channel, adapter: sender,
					msg:     &IncomingMessage{Platform: platform, UserID: "test-user", Content: "Show the images"},
					session: &types.Session{ID: "test-session"}, userKey: "test-user",
				})
				content := adapter.finalContent
				if adapter.plainReplies > 0 {
					content = adapter.plainContent
				}
				check(t, content)
				require.NotNil(t, messages.saved)
				require.Equal(t, strings.TrimSpace(f.answer), strings.TrimSpace(messages.saved.Content),
					"persist stable references, not signed URLs")
			})
		}
	}
}

func TestIMResourceStoragePreservesShortLinksAndRejectsUnavailableInstances(t *testing.T) {
	f := newIMResourceStorageFixture(t)
	s, _, _, _ := newFullOutputHarness(f.answer)
	f.wire(s)
	ctx := f.context()
	t.Setenv("APP_EXTERNAL_URL", "https://weknora.example")
	out := cleanIMContent(ctx, f.answer, s.newOutboundFileServiceResolver(f.tenant, nil, nil))
	require.Equal(t, 3, strings.Count(out, "https://weknora.example/r/"))
	require.NotContains(t, out, "resource://")

	t.Setenv("APP_EXTERNAL_URL", "")
	require.NoError(t, f.db.Model(&types.StorageBackend{}).Where("id = ?", "oss-0").
		Update("status", types.StorageBackendStatusDisabled).Error)
	require.NoError(t, f.db.Where("id = ?", "oss-1").Delete(&types.StorageBackend{}).Error)
	out = cleanIMContent(ctx, f.answer, s.newOutboundFileServiceResolver(f.tenant, nil, nil))
	require.Contains(t, out, f.refs[0])
	require.Contains(t, out, f.refs[1])
	require.NotContains(t, out, f.refs[2], "the available shared instance still resolves")
	require.Contains(t, out, "x-oss-signature=")
}

type resourceStreamingSession struct {
	interfaces.SessionService
	answer  string
	updated <-chan struct{}
}

func (s *resourceStreamingSession) KnowledgeQA(ctx context.Context, req *types.QARequest, bus *event.EventBus) error {
	if err := bus.Emit(ctx, event.Event{
		Type: event.EventAgentFinalAnswer, SessionID: req.Session.ID,
		Data: event.AgentFinalAnswerData{Content: s.answer},
	}); err != nil {
		return err
	}
	select {
	case <-s.updated:
	case <-ctx.Done():
		return ctx.Err()
	}
	return bus.Emit(ctx, event.Event{
		Type: event.EventAgentFinalAnswer, SessionID: req.Session.ID,
		Data: event.AgentFinalAnswerData{Done: true},
	})
}

type resourceStreamingAdapter struct {
	*fullOutputAdapter
	once    sync.Once
	updated chan struct{}
	content string
}

func (a *resourceStreamingAdapter) UpdateStreamContent(
	ctx context.Context,
	msg *IncomingMessage,
	streamID, content string,
) error {
	a.content = content
	a.once.Do(func() { close(a.updated) })
	return a.fullOutputAdapter.UpdateStreamContent(ctx, msg, streamID, content)
}

func TestIMStreamUpdatesUseResourceStorageBeforeCompletion(t *testing.T) {
	f := newIMResourceStorageFixture(t)
	s, base, _, _ := newFullOutputHarness(f.answer)
	f.wire(s)
	adapter := &resourceStreamingAdapter{fullOutputAdapter: base, updated: make(chan struct{})}
	s.sessionService = &resourceStreamingSession{answer: f.answer, updated: adapter.updated}
	ctx, cancel := context.WithTimeout(f.context(), 5*time.Second)
	defer cancel()
	err := s.handleMessageStream(ctx,
		&IncomingMessage{Platform: PlatformWeCom, UserID: "test-user", Content: "Show the images"},
		&types.Session{ID: "test-session"}, nil, nil, nil, nil, adapter, adapter, "test-user", f.tenant)
	require.NoError(t, err)
	require.Positive(t, adapter.updates)
	f.checkSignedImages(t, adapter.content)
	f.checkSignedImages(t, adapter.finalContent)
}
