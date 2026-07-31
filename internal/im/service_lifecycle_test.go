package im

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

type lifecycleTestAdapter struct{}

func (*lifecycleTestAdapter) Platform() Platform                { return Platform("test") }
func (*lifecycleTestAdapter) VerifyCallback(*gin.Context) error { return nil }
func (*lifecycleTestAdapter) ParseCallback(*gin.Context) (*IncomingMessage, error) {
	return nil, nil
}
func (*lifecycleTestAdapter) SendReply(context.Context, *IncomingMessage, *ReplyMessage) error {
	return nil
}
func (*lifecycleTestAdapter) HandleURLVerification(*gin.Context) bool { return false }

type attachmentTestAdapter struct {
	lifecycleTestAdapter
	name string
	data string
}

type messageStorageKBServiceStub struct {
	interfaces.KnowledgeBaseService
	kb  *types.KnowledgeBase
	err error
}

func (s *messageStorageKBServiceStub) GetKnowledgeBaseByID(context.Context, string) (*types.KnowledgeBase, error) {
	return s.kb, s.err
}

func (a *attachmentTestAdapter) DownloadFile(context.Context, *IncomingMessage) (io.ReadCloser, string, error) {
	return io.NopCloser(strings.NewReader(a.data)), a.name, nil
}

type lifecycleFactoryCounters struct {
	starts atomic.Int32
	stops  atomic.Int32
}

func (c *lifecycleFactoryCounters) factory() AdapterFactory {
	return func(context.Context, *IMChannel, func(context.Context, *IncomingMessage) error) (Adapter, context.CancelFunc, error) {
		c.starts.Add(1)
		var once sync.Once
		return &lifecycleTestAdapter{}, func() {
			once.Do(func() { c.stops.Add(1) })
		}, nil
	}
}

func newLifecycleTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:im-lifecycle-%d?mode=memory&cache=shared", time.Now().UnixNano())), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	// IMChannel's production schema uses PostgreSQL's uuid_generate_v4()
	// default, which SQLite cannot parse. Keep an equivalent minimal table for
	// lifecycle tests; IDs are assigned explicitly below.
	if err := db.Exec(`CREATE TABLE im_channels (
		id TEXT PRIMARY KEY,
		tenant_id INTEGER NOT NULL,
		agent_id TEXT NOT NULL,
		platform TEXT NOT NULL,
		name TEXT NOT NULL DEFAULT '',
		enabled NUMERIC NOT NULL DEFAULT 1,
		mode TEXT NOT NULL DEFAULT 'websocket',
		output_mode TEXT NOT NULL DEFAULT 'stream',
		knowledge_base_id TEXT DEFAULT '',
		message_storage_enabled NUMERIC NOT NULL DEFAULT 0,
		bot_identity TEXT NOT NULL DEFAULT '',
		session_mode TEXT NOT NULL DEFAULT 'user',
		credentials TEXT NOT NULL DEFAULT '{}',
		created_at DATETIME,
		updated_at DATETIME,
		deleted_at DATETIME
	)`).Error; err != nil {
		t.Fatalf("create im_channels: %v", err)
	}
	return db
}

func newLifecycleTestService(db *gorm.DB, redisClient *redis.Client, instanceID string) *Service {
	return &Service{
		db:               db,
		channels:         make(map[string]*channelState),
		leaderRetries:    make(map[string]*leaderRetryState),
		adapterFactories: make(map[string]AdapterFactory),
		redis:            redisClient,
		instanceID:       instanceID,
		stopCh:           make(chan struct{}),
	}
}

func TestPrepareIMAttachmentsReusesDownloadedImageForVision(t *testing.T) {
	svc := &Service{}
	adapter := &attachmentTestAdapter{name: "diagram.png", data: "image-bytes"}
	attachments, files, imageURLs, err := svc.prepareIMAttachments(context.Background(), &IncomingMessage{
		MessageType: MessageTypeImage,
		FileName:    "diagram.png",
	}, adapter)
	if err != nil {
		t.Fatalf("prepareIMAttachments() error = %v", err)
	}
	if len(attachments) != 1 || attachments[0].FileName != "diagram.png" {
		t.Fatalf("attachments = %#v", attachments)
	}
	if len(files) != 1 || string(files[0].content) != "image-bytes" {
		t.Fatalf("attachment files = %#v", files)
	}
	if len(imageURLs) != 1 || !strings.HasPrefix(imageURLs[0], "data:image/png;base64,") {
		t.Fatalf("image URLs = %#v", imageURLs)
	}
}

func TestPrepareIMAttachmentsRejectsOversizedFileBeforeDownload(t *testing.T) {
	svc := &Service{}
	_, _, _, err := svc.prepareIMAttachments(context.Background(), &IncomingMessage{
		MessageType: MessageTypeFile,
		FileSize:    maxIMAttachmentBytes + 1,
	}, &attachmentTestAdapter{})
	if err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("prepareIMAttachments() error = %v, want size-limit error", err)
	}
}

func TestValidateMessageStorageRequiresSameTenantKnowledgeBase(t *testing.T) {
	channel := &IMChannel{TenantID: 7, MessageStorageEnabled: true, KnowledgeBaseID: "kb-1"}
	svc := &Service{kbService: &messageStorageKBServiceStub{kb: &types.KnowledgeBase{ID: "kb-1", TenantID: 8}}}
	if err := svc.validateMessageStorage(channel); !errors.Is(err, ErrInvalidMessageStorage) {
		t.Fatalf("validateMessageStorage() error = %v, want invalid storage error", err)
	}

	svc.kbService = &messageStorageKBServiceStub{kb: &types.KnowledgeBase{ID: "kb-1", TenantID: 7}}
	if err := svc.validateMessageStorage(channel); err != nil {
		t.Fatalf("validateMessageStorage() error = %v, want nil", err)
	}
}

func createLifecycleChannel(t *testing.T, db *gorm.DB, id, agentID string) *IMChannel {
	t.Helper()
	channel := &IMChannel{
		ID:          id,
		TenantID:    1,
		AgentID:     agentID,
		Platform:    "test",
		Enabled:     true,
		Mode:        "webhook",
		OutputMode:  "full",
		SessionMode: string(SessionModeUser),
		Credentials: types.JSON(`{"token":"v1"}`),
	}
	if err := db.Create(channel).Error; err != nil {
		t.Fatalf("create channel: %v", err)
	}
	return channel
}

func TestEnsureChannelAdapterRefreshesStaleConfig(t *testing.T) {
	db := newLifecycleTestDB(t)
	channel := createLifecycleChannel(t, db, "channel-refresh", "agent-old")
	counters := &lifecycleFactoryCounters{}
	svc := newLifecycleTestService(db, nil, "instance-one")
	svc.RegisterAdapterFactory("test", counters.factory())
	t.Cleanup(svc.Stop)

	if err := svc.StartChannel(channel); err != nil {
		t.Fatalf("start initial channel: %v", err)
	}
	if err := db.Model(&IMChannel{}).Where("id = ?", channel.ID).
		Updates(map[string]any{"agent_id": "agent-new", "credentials": types.JSON(`{"token":"v2"}`)}).Error; err != nil {
		t.Fatalf("update durable channel: %v", err)
	}

	_, fresh, err := svc.EnsureChannelAdapter(channel.ID)
	if err != nil {
		t.Fatalf("ensure channel adapter: %v", err)
	}
	if fresh.AgentID != "agent-new" || string(fresh.Credentials) != `{"token":"v2"}` {
		t.Fatalf("stale runtime config returned: agent=%q credentials=%s", fresh.AgentID, fresh.Credentials)
	}
	if counters.starts.Load() != 2 || counters.stops.Load() != 1 {
		t.Fatalf("runtime was not rebuilt exactly once: starts=%d stops=%d", counters.starts.Load(), counters.stops.Load())
	}
}

func TestEnsureChannelAdapterStopsDisabledCachedChannel(t *testing.T) {
	db := newLifecycleTestDB(t)
	channel := createLifecycleChannel(t, db, "channel-disabled", "agent")
	counters := &lifecycleFactoryCounters{}
	svc := newLifecycleTestService(db, nil, "instance-one")
	svc.RegisterAdapterFactory("test", counters.factory())
	t.Cleanup(svc.Stop)

	if err := svc.StartChannel(channel); err != nil {
		t.Fatalf("start channel: %v", err)
	}
	if err := db.Model(&IMChannel{}).Where("id = ?", channel.ID).Update("enabled", false).Error; err != nil {
		t.Fatalf("disable channel: %v", err)
	}
	if _, _, err := svc.EnsureChannelAdapter(channel.ID); err == nil {
		t.Fatal("EnsureChannelAdapter() expected disabled error")
	}
	if _, _, ok := svc.GetChannelAdapter(channel.ID); ok {
		t.Fatal("disabled channel remained in runtime map")
	}
	if counters.stops.Load() != 1 {
		t.Fatalf("cleanup calls = %d, want 1", counters.stops.Load())
	}
}

func TestEnsureChannelAdapterKeepsRuntimeOnDatabaseFailure(t *testing.T) {
	db := newLifecycleTestDB(t)
	channel := createLifecycleChannel(t, db, "channel-db-failure", "agent")
	counters := &lifecycleFactoryCounters{}
	svc := newLifecycleTestService(db, nil, "instance-one")
	svc.RegisterAdapterFactory("test", counters.factory())
	t.Cleanup(svc.Stop)

	if err := svc.StartChannel(channel); err != nil {
		t.Fatalf("start channel: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("resolve sql DB: %v", err)
	}
	if err := sqlDB.Close(); err != nil {
		t.Fatalf("close test DB: %v", err)
	}
	if _, _, err := svc.EnsureChannelAdapter(channel.ID); err == nil {
		t.Fatal("EnsureChannelAdapter() expected database error")
	}
	if _, _, ok := svc.GetChannelAdapter(channel.ID); !ok {
		t.Fatal("transient database failure tore down the cached runtime")
	}
	if counters.stops.Load() != 0 {
		t.Fatalf("cleanup calls = %d, want 0 before service shutdown", counters.stops.Load())
	}
}

func TestChannelConfigEventReloadsOtherReplica(t *testing.T) {
	db := newLifecycleTestDB(t)
	channel := createLifecycleChannel(t, db, "channel-pubsub", "agent-old")
	redisServer := miniredis.RunT(t)
	redisOne := redis.NewClient(&redis.Options{Addr: redisServer.Addr()})
	redisTwo := redis.NewClient(&redis.Options{Addr: redisServer.Addr()})
	t.Cleanup(func() { _ = redisOne.Close(); _ = redisTwo.Close() })

	countersOne := &lifecycleFactoryCounters{}
	countersTwo := &lifecycleFactoryCounters{}
	svcOne := newLifecycleTestService(db, redisOne, "instance-one")
	svcTwo := newLifecycleTestService(db, redisTwo, "instance-two")
	svcOne.RegisterAdapterFactory("test", countersOne.factory())
	svcTwo.RegisterAdapterFactory("test", countersTwo.factory())
	svcOne.startChannelConfigSubscriber()
	svcTwo.startChannelConfigSubscriber()
	t.Cleanup(svcOne.Stop)
	t.Cleanup(svcTwo.Stop)

	if err := svcOne.StartChannel(channel); err != nil {
		t.Fatalf("start channel on instance one: %v", err)
	}
	copyForTwo := *channel
	if err := svcTwo.StartChannel(&copyForTwo); err != nil {
		t.Fatalf("start channel on instance two: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		counts, err := redisOne.PubSubNumSub(context.Background(), RedisChannelConfig).Result()
		if err == nil && counts[RedisChannelConfig] == 2 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	updated := *channel
	updated.AgentID = "agent-new"
	updated.Credentials = types.JSON(`{"token":"v2"}`)
	if err := svcOne.UpdateChannel(&updated); err != nil {
		t.Fatalf("update channel: %v", err)
	}

	deadline = time.Now().Add(2 * time.Second)
	reloaded := false
	for time.Now().Before(deadline) {
		_, runtimeChannel, ok := svcTwo.GetChannelAdapter(channel.ID)
		if ok && runtimeChannel.AgentID == "agent-new" && string(runtimeChannel.Credentials) == `{"token":"v2"}` {
			if countersTwo.starts.Load() < 2 || countersTwo.stops.Load() < 1 {
				t.Fatalf("replica config changed without rebuilding runtime: starts=%d stops=%d", countersTwo.starts.Load(), countersTwo.stops.Load())
			}
			reloaded = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !reloaded {
		t.Fatal("second replica did not reload the published channel change")
	}

	if _, err := svcOne.ToggleChannel(channel.ID, channel.TenantID); err != nil {
		t.Fatalf("disable channel: %v", err)
	}
	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, _, ok := svcTwo.GetChannelAdapter(channel.ID); !ok {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("second replica did not stop the disabled channel")
}

func TestServiceStopIsIdempotent(t *testing.T) {
	db := newLifecycleTestDB(t)
	channel := createLifecycleChannel(t, db, "channel-stop", "agent")
	counters := &lifecycleFactoryCounters{}
	svc := newLifecycleTestService(db, nil, "instance-one")
	svc.RegisterAdapterFactory("test", counters.factory())
	if err := svc.StartChannel(channel); err != nil {
		t.Fatalf("start channel: %v", err)
	}

	svc.Stop()
	svc.Stop()

	if counters.stops.Load() != 1 {
		t.Fatalf("cleanup calls = %d, want exactly 1", counters.stops.Load())
	}
	if err := svc.StartChannel(channel); err == nil {
		t.Fatal("StartChannel() succeeded after service shutdown")
	}
}

// A factory can block for seconds while dialing, so Stop() may drain the
// channel map after StartChannel's pre-flight check but before registration.
// The adapter created in that window must be torn down instead of outliving
// shutdown with an open connection.
func TestStartChannelDoesNotLeakAdapterWhenStoppedDuringFactory(t *testing.T) {
	db := newLifecycleTestDB(t)
	channel := createLifecycleChannel(t, db, "channel-stop-race", "agent")
	counters := &lifecycleFactoryCounters{}
	svc := newLifecycleTestService(db, nil, "instance-one")

	factoryEntered := make(chan struct{})
	releaseFactory := make(chan struct{})
	inner := counters.factory()
	svc.RegisterAdapterFactory("test", func(
		ctx context.Context,
		ch *IMChannel,
		handler func(context.Context, *IncomingMessage) error,
	) (Adapter, context.CancelFunc, error) {
		close(factoryEntered)
		<-releaseFactory
		return inner(ctx, ch, handler)
	})

	startErr := make(chan error, 1)
	go func() { startErr <- svc.StartChannel(channel) }()

	<-factoryEntered
	svc.Stop()
	close(releaseFactory)

	if err := <-startErr; err == nil {
		t.Fatal("StartChannel() succeeded even though the service was stopped")
	}
	if _, _, ok := svc.GetChannelAdapter(channel.ID); ok {
		t.Fatal("adapter was registered after shutdown")
	}
	if counters.starts.Load() != 1 {
		t.Fatalf("factory starts = %d, want 1", counters.starts.Load())
	}
	if counters.stops.Load() != 1 {
		t.Fatalf("cleanup calls = %d, want 1 so the connection is not leaked", counters.stops.Load())
	}
}

func TestSameChannelRuntimeConfigUsesSemanticCredentials(t *testing.T) {
	now := time.Now()
	cached := &IMChannel{
		ID:          "channel",
		TenantID:    1,
		AgentID:     "agent",
		Platform:    "test",
		Enabled:     true,
		Mode:        "webhook",
		OutputMode:  "full",
		SessionMode: string(SessionModeUser),
		Credentials: types.JSON(`{"token":"secret","timeout":10}`),
		UpdatedAt:   now,
	}
	fresh := *cached
	fresh.Credentials = types.JSON(`{"timeout":10,"token":"secret"}`)
	fresh.UpdatedAt = now.Truncate(time.Microsecond)

	if !sameChannelRuntimeConfig(cached, &fresh) {
		t.Fatal("semantic-equivalent credentials or timestamp precision triggered a rebuild")
	}
	fresh.Credentials = types.JSON(`{"timeout":10,"token":"changed"}`)
	if sameChannelRuntimeConfig(cached, &fresh) {
		t.Fatal("changed credentials did not trigger a rebuild")
	}
}

func TestLeaderElectionFailsClosedWhenRedisUnavailable(t *testing.T) {
	db := newLifecycleTestDB(t)
	channel := createLifecycleChannel(t, db, "channel-redis-down", "agent")
	channel.Mode = "websocket"
	counters := &lifecycleFactoryCounters{}
	redisClient := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1"})
	_ = redisClient.Close()
	svc := newLifecycleTestService(db, redisClient, "instance-one")
	svc.RegisterAdapterFactory("test", counters.factory())
	t.Cleanup(svc.Stop)

	if err := svc.StartChannel(channel); err != nil {
		t.Fatalf("StartChannel() should schedule a retry, got %v", err)
	}
	if err := svc.StartChannel(channel); err != nil {
		t.Fatalf("second StartChannel() should replace the retry, got %v", err)
	}
	if counters.starts.Load() != 0 {
		t.Fatalf("factory starts = %d, want 0 while leader election is unavailable", counters.starts.Load())
	}
	svc.mu.RLock()
	retryCount := len(svc.leaderRetries)
	svc.mu.RUnlock()
	if retryCount != 1 {
		t.Fatalf("leader retry goroutines = %d, want one per channel", retryCount)
	}
}

func TestLeadershipLossStopsAdapterAndSchedulesRecovery(t *testing.T) {
	db := newLifecycleTestDB(t)
	channel := createLifecycleChannel(t, db, "channel-leader-loss", "agent")
	channel.Mode = "websocket"
	if err := db.Model(&IMChannel{}).Where("id = ?", channel.ID).
		Update("mode", channel.Mode).Error; err != nil {
		t.Fatalf("persist websocket mode: %v", err)
	}

	redisServer := miniredis.RunT(t)
	redisClient := redis.NewClient(&redis.Options{Addr: redisServer.Addr()})
	t.Cleanup(func() { _ = redisClient.Close() })

	counters := &lifecycleFactoryCounters{}
	svc := newLifecycleTestService(db, redisClient, "instance-one")
	svc.RegisterAdapterFactory("test", counters.factory())
	t.Cleanup(svc.Stop)

	if err := svc.StartChannel(channel); err != nil {
		t.Fatalf("start websocket channel: %v", err)
	}
	if counters.starts.Load() != 1 {
		t.Fatalf("factory starts = %d, want 1", counters.starts.Load())
	}

	// Simulate another owner replacing the lease before the next renewal.
	key := RedisKeyLeader + channel.ID
	redisServer.Set(key, "instance-two")
	svc.handleWSLeadershipLoss(channel.ID)

	if _, _, ok := svc.GetChannelAdapter(channel.ID); ok {
		t.Fatal("adapter remained active after leadership loss")
	}
	if counters.stops.Load() != 1 {
		t.Fatalf("adapter stops = %d, want 1", counters.stops.Load())
	}
	svc.mu.RLock()
	retryCount := len(svc.leaderRetries)
	svc.mu.RUnlock()
	if retryCount != 1 {
		t.Fatalf("leader retry goroutines = %d, want one recovery path", retryCount)
	}

	// Repeated loss handling after teardown must not create duplicate retries.
	svc.handleWSLeadershipLoss(channel.ID)
	svc.mu.RLock()
	retryCount = len(svc.leaderRetries)
	svc.mu.RUnlock()
	if retryCount != 1 {
		t.Fatalf("leader retry goroutines after duplicate loss = %d, want one", retryCount)
	}
}
