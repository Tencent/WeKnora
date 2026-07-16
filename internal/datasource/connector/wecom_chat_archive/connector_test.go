package wecom_chat_archive

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/datasource"
	"github.com/Tencent/WeKnora/internal/types"
)

type fakeArchiveClient struct {
	validateErr error
	fetchErr    error
	messages    []ArchiveMessageEnvelope
}

func (f *fakeArchiveClient) Validate(ctx context.Context) error { return f.validateErr }
func (f *fakeArchiveClient) FetchMessages(ctx context.Context, startSeq uint64, limit int) ([]ArchiveMessageEnvelope, bool, error) {
	if f.fetchErr != nil {
		return nil, false, f.fetchErr
	}
	var out []ArchiveMessageEnvelope
	for _, msg := range f.messages {
		if msg.Seq >= startSeq {
			out = append(out, msg)
		}
	}
	return out, false, nil
}
func (f *fakeArchiveClient) Close() error { return nil }

func validConfig() *types.DataSourceConfig {
	return &types.DataSourceConfig{
		Type: types.ConnectorTypeWeComChatArchive,
		Credentials: map[string]interface{}{
			"corp_id":             "wwxxxx",
			"secret":              "top-secret",
			"private_key":         "-----BEGIN PRIVATE KEY-----\nabc\n-----END PRIVATE KEY-----",
			"private_key_version": "1",
		},
		ResourceIDs: []string{"all"},
		Settings: map[string]interface{}{
			"timezone":       "Asia/Shanghai",
			"full_sync_days": float64(30),
		},
	}
}

func TestParseConfigAppliesDefaults(t *testing.T) {
	cfg := validConfig()
	cfg.ResourceIDs = nil
	cfg.Settings = nil

	parsed, err := parseConfig(cfg)
	if err != nil {
		t.Fatalf("parseConfig error: %v", err)
	}
	if parsed.CorpID != "wwxxxx" {
		t.Fatalf("CorpID = %q", parsed.CorpID)
	}
	if parsed.ResourceIDs[0] != "all" {
		t.Fatalf("ResourceIDs = %v", parsed.ResourceIDs)
	}
	if parsed.Settings.Timezone != "Asia/Shanghai" {
		t.Fatalf("Timezone = %q", parsed.Settings.Timezone)
	}
	if parsed.Settings.FullSyncDays != 90 {
		t.Fatalf("FullSyncDays = %d", parsed.Settings.FullSyncDays)
	}
	if parsed.Settings.AttachmentPolicy != "metadata_only" {
		t.Fatalf("AttachmentPolicy = %q", parsed.Settings.AttachmentPolicy)
	}
	if parsed.Settings.SyncRevokeAsDelete {
		t.Fatal("SyncRevokeAsDelete should default false")
	}
	if !parsed.Settings.RecordParticipantsForACL {
		t.Fatal("RecordParticipantsForACL should default true")
	}
}

func TestParseConfigReadsSettings(t *testing.T) {
	parsed, err := parseConfig(validConfig())
	if err != nil {
		t.Fatalf("parseConfig error: %v", err)
	}
	if parsed.Settings.FullSyncDays != 30 {
		t.Fatalf("FullSyncDays = %d, want 30", parsed.Settings.FullSyncDays)
	}
}

func TestParseConfigReadsSDKNetworkSettings(t *testing.T) {
	cfg := validConfig()
	cfg.Settings["proxy"] = "socks5://127.0.0.1:8081"
	cfg.Settings["proxy_password"] = "user:pass"
	cfg.Settings["timeout_seconds"] = float64(7)
	parsed, err := parseConfig(cfg)
	if err != nil {
		t.Fatalf("parseConfig error: %v", err)
	}
	if parsed.Settings.Proxy != "socks5://127.0.0.1:8081" || parsed.Settings.ProxyPassword != "user:pass" || parsed.Settings.TimeoutSeconds != 7 {
		t.Fatalf("settings = %#v", parsed.Settings)
	}
}

func TestParseConfigRequiresCredentialsWithoutLeakingSecrets(t *testing.T) {
	cfg := validConfig()
	cfg.Credentials["private_key"] = ""
	err := parseConfigExpectError(cfg)
	if !strings.Contains(err, "private_key is required") {
		t.Fatalf("error = %q", err)
	}
	if strings.Contains(err, "top-secret") || strings.Contains(err, "BEGIN PRIVATE KEY") {
		t.Fatalf("error leaked secret material: %q", err)
	}
}

func TestConnectorType(t *testing.T) {
	if NewConnector().Type() != types.ConnectorTypeWeComChatArchive {
		t.Fatalf("Type() = %q", NewConnector().Type())
	}
}

func TestConnectorSatisfiesDatasourceInterface(t *testing.T) {
	var _ datasource.Connector = NewConnector()
}

func TestValidateUsesArchiveClient(t *testing.T) {
	called := false
	c := NewConnector(WithClientFactory(func(cfg *Config) ArchiveClient {
		called = true
		return &fakeArchiveClient{}
	}))
	if err := c.Validate(context.Background(), validConfig()); err != nil {
		t.Fatalf("Validate error: %v", err)
	}
	if !called {
		t.Fatal("client factory was not called")
	}
}

func TestListResourcesReturnsVirtualAllOnlyAtRoot(t *testing.T) {
	c := NewConnector()
	resources, err := c.ListResources(context.Background(), validConfig(), "")
	if err != nil {
		t.Fatalf("ListResources error: %v", err)
	}
	if len(resources) != 1 || resources[0].ExternalID != "all" {
		t.Fatalf("resources = %#v", resources)
	}
	if resources[0].Type != "wecom_chat_archive_scope" {
		t.Fatalf("resource type = %q", resources[0].Type)
	}
	children, err := c.ListResources(context.Background(), validConfig(), "all")
	if err != nil {
		t.Fatalf("ListResources child error: %v", err)
	}
	if len(children) != 0 {
		t.Fatalf("child resources = %#v, want empty", children)
	}
}

func TestResolveResourceAncestorsReturnsEmpty(t *testing.T) {
	ancestors, err := NewConnector().ResolveResourceAncestors(context.Background(), validConfig(), []string{"all"})
	if err != nil {
		t.Fatalf("ResolveResourceAncestors error: %v", err)
	}
	if len(ancestors) != 0 {
		t.Fatalf("ancestors = %#v, want empty", ancestors)
	}
}

func TestFetchAllAggregatesRecentMessages(t *testing.T) {
	now := time.Now().UTC()
	c := NewConnector(WithClientFactory(func(cfg *Config) ArchiveClient {
		return &fakeArchiveClient{messages: []ArchiveMessageEnvelope{
			{Seq: 1, MsgID: "old", MsgType: "text", ConversationID: "wr_xxx", ConversationName: "客户项目群", ConversationType: conversationTypeRoom, From: Sender{UserID: "old"}, MsgTime: now.AddDate(0, 0, -100), Raw: []byte("old")},
			{Seq: 2, MsgID: "new", MsgType: "text", ConversationID: "wr_xxx", ConversationName: "客户项目群", ConversationType: conversationTypeRoom, From: Sender{UserID: "zhangsan", Name: "张三"}, MsgTime: now, Raw: []byte("new body")},
		}}
	}))
	cfg := validConfig()
	cfg.Settings = map[string]interface{}{"full_sync_days": float64(90)}
	items, err := c.FetchAll(context.Background(), cfg, []string{"all"})
	if err != nil {
		t.Fatalf("FetchAll error: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("items len = %d, want 1", len(items))
	}
	if strings.Contains(string(items[0].Content), "old") {
		t.Fatalf("old message was not filtered: %s", string(items[0].Content))
	}
	if !strings.Contains(string(items[0].Content), "new body") {
		t.Fatalf("new message missing: %s", string(items[0].Content))
	}
}

func TestFetchIncrementalStartsAfterCursorAndAdvancesSeq(t *testing.T) {
	now := time.Date(2026, 7, 7, 9, 0, 0, 0, time.UTC)
	c := NewConnector(WithClientFactory(func(cfg *Config) ArchiveClient {
		return &fakeArchiveClient{messages: []ArchiveMessageEnvelope{
			{Seq: 10, MsgID: "skip", MsgType: "text", ConversationID: "wr_xxx", ConversationType: conversationTypeRoom, From: Sender{UserID: "a"}, MsgTime: now, Raw: []byte("skip")},
			{Seq: 11, MsgID: "keep", MsgType: "text", ConversationID: "wr_xxx", ConversationType: conversationTypeRoom, From: Sender{UserID: "b"}, MsgTime: now.Add(time.Minute), Raw: []byte("keep")},
		}}
	}))
	cursor := &types.SyncCursor{ConnectorCursor: map[string]interface{}{"last_seq": float64(10)}}
	items, next, err := c.FetchIncremental(context.Background(), validConfig(), cursor)
	if err != nil {
		t.Fatalf("FetchIncremental error: %v", err)
	}
	if len(items) != 1 || strings.Contains(string(items[0].Content), "skip") {
		t.Fatalf("items = %#v", items)
	}
	if next == nil || next.ConnectorCursor["last_seq"] == nil {
		t.Fatalf("next cursor missing last_seq: %#v", next)
	}
	if got := next.ConnectorCursor["last_seq"]; got != "11" {
		t.Fatalf("last_seq = %d, want 11", got)
	}
}

func TestFetchIncrementalAggregatesGroupMessagesByRoomID(t *testing.T) {
	msgTime := time.Date(2026, 7, 13, 9, 30, 0, 0, time.FixedZone("CST", 8*3600))
	c := NewConnector(WithClientFactory(func(cfg *Config) ArchiveClient {
		return &fakeArchiveClient{messages: []ArchiveMessageEnvelope{
			{
				Seq:              42,
				MsgID:            "group-msg-1",
				Action:           "send",
				MsgType:          "text",
				ConversationName: "客户项目群",
				RoomID:           "wr_group_123",
				From:             Sender{UserID: "zhangsan", Name: "张三", Type: senderTypeInternal},
				ToList:           []Sender{{UserID: "lisi", Name: "李四", Type: senderTypeInternal}},
				MsgTime:          msgTime,
				Raw:              []byte(`{"text":{"content":"群聊问题"}}`),
			},
		}}
	}))

	items, _, err := c.FetchIncremental(context.Background(), validConfig(), nil)
	if err != nil {
		t.Fatalf("FetchIncremental error: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("items len = %d, want 1", len(items))
	}
	item := items[0]
	if item.ExternalID != "wecom-chat:wr_group_123:2026-07-13" {
		t.Fatalf("ExternalID = %q", item.ExternalID)
	}
	if item.Metadata["conversation_type"] != conversationTypeRoom {
		t.Fatalf("conversation_type = %q", item.Metadata["conversation_type"])
	}
	if item.Metadata["conversation_id"] != "wr_group_123" {
		t.Fatalf("conversation_id = %q", item.Metadata["conversation_id"])
	}
	if item.Metadata["participant_room_ids"] != "wr_group_123" {
		t.Fatalf("participant_room_ids = %q", item.Metadata["participant_room_ids"])
	}
	content := string(item.Content)
	if !strings.Contains(content, "# 企业微信会话：客户项目群 / 2026-07-13") {
		t.Fatalf("content missing group title:\n%s", content)
	}
	if !strings.Contains(content, "群聊问题") {
		t.Fatalf("content missing message body:\n%s", content)
	}
}

func TestFetchIncrementalPreservesUint64CursorPrecision(t *testing.T) {
	now := time.Date(2026, 7, 7, 9, 0, 0, 0, time.UTC)
	largeSeq := uint64(1<<53 + 17)
	c := NewConnector(WithClientFactory(func(cfg *Config) ArchiveClient {
		return &fakeArchiveClient{messages: []ArchiveMessageEnvelope{
			{Seq: largeSeq, MsgID: "large", MsgType: "text", ConversationID: "wr_xxx", ConversationType: conversationTypeRoom, From: Sender{UserID: "a"}, MsgTime: now, Raw: []byte("large")},
		}}
	}))
	cursor := &types.SyncCursor{ConnectorCursor: map[string]interface{}{"last_seq": strconv.FormatUint(largeSeq-1, 10)}}
	_, next, err := c.FetchIncremental(context.Background(), validConfig(), cursor)
	if err != nil {
		t.Fatalf("FetchIncremental error: %v", err)
	}
	if got := next.ConnectorCursor["last_seq"]; got != strconv.FormatUint(largeSeq, 10) {
		t.Fatalf("last_seq = %v, want %d", got, largeSeq)
	}
}

func TestFetchIncrementalAdvancesCursorPastEmptyConversation(t *testing.T) {
	now := time.Date(2026, 7, 7, 9, 0, 0, 0, time.UTC)
	c := NewConnector(WithClientFactory(func(cfg *Config) ArchiveClient {
		return &fakeArchiveClient{messages: []ArchiveMessageEnvelope{
			{Seq: 11, MsgID: "empty", MsgType: "text", ConversationID: "", From: Sender{UserID: "a"}, MsgTime: now, Raw: []byte("empty")},
			{Seq: 12, MsgID: "keep", MsgType: "text", ConversationID: "wr_xxx", ConversationType: conversationTypeRoom, From: Sender{UserID: "b"}, MsgTime: now.Add(time.Minute), Raw: []byte("keep")},
		}}
	}))
	cursor := &types.SyncCursor{ConnectorCursor: map[string]interface{}{"last_seq": float64(10)}}
	items, next, err := c.FetchIncremental(context.Background(), validConfig(), cursor)
	if err != nil {
		t.Fatalf("FetchIncremental error: %v", err)
	}
	if len(items) != 1 || !strings.Contains(string(items[0].Content), "keep") {
		t.Fatalf("items = %#v", items)
	}
	if got := next.ConnectorCursor["last_seq"]; got != "12" {
		t.Fatalf("last_seq = %d, want 12", got)
	}
}

func TestFetchIncrementalAdvancesCursorWhenOnlyEmptyConversations(t *testing.T) {
	now := time.Date(2026, 7, 7, 9, 0, 0, 0, time.UTC)
	c := NewConnector(WithClientFactory(func(cfg *Config) ArchiveClient {
		return &fakeArchiveClient{messages: []ArchiveMessageEnvelope{
			{Seq: 11, MsgID: "empty", MsgType: "text", ConversationID: "", From: Sender{UserID: "a"}, MsgTime: now, Raw: []byte("empty")},
		}}
	}))
	cursor := &types.SyncCursor{ConnectorCursor: map[string]interface{}{"last_seq": float64(10)}}
	items, next, err := c.FetchIncremental(context.Background(), validConfig(), cursor)
	if err != nil {
		t.Fatalf("FetchIncremental error: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("items = %#v, want empty", items)
	}
	if got := next.ConnectorCursor["last_seq"]; got != "11" {
		t.Fatalf("last_seq = %d, want 11", got)
	}
}

func TestValidateRedactsConfiguredSecretsFromClientErrors(t *testing.T) {
	c := NewConnector(WithClientFactory(func(cfg *Config) ArchiveClient {
		return &fakeArchiveClient{validateErr: errors.New("auth failed with top-secret and -----BEGIN PRIVATE KEY-----")}
	}))
	err := c.Validate(context.Background(), validConfig())
	assertErrorRedacted(t, err)
}

func TestFetchIncrementalRedactsConfiguredSecretsFromClientErrors(t *testing.T) {
	c := NewConnector(WithClientFactory(func(cfg *Config) ArchiveClient {
		return &fakeArchiveClient{fetchErr: errors.New("fetch failed with top-secret and -----BEGIN PRIVATE KEY-----")}
	}))
	_, _, err := c.FetchIncremental(context.Background(), validConfig(), nil)
	assertErrorRedacted(t, err)
}

func TestBucketItemRendersTextAndParticipantMetadata(t *testing.T) {
	msgTime := time.Date(2026, 7, 7, 9, 1, 22, 0, time.FixedZone("CST", 8*3600))
	b := newDayBucket(ArchiveMessageEnvelope{
		Seq:              10,
		MsgID:            "msg-1",
		MsgType:          "text",
		ConversationID:   "wr_xxx",
		ConversationName: "客户项目群",
		ConversationType: conversationTypeRoom,
		RoomID:           "wr_xxx",
		From:             Sender{UserID: "zhangsan", Name: "张三", Type: senderTypeInternal},
		ToList:           []Sender{{UserID: "lisi", Name: "李四", Type: senderTypeInternal}},
		MsgTime:          msgTime,
	})
	addToBucket(b, normalizedMessage{
		Envelope: ArchiveMessageEnvelope{
			Seq:              10,
			MsgID:            "msg-1",
			MsgType:          "text",
			ConversationID:   "wr_xxx",
			ConversationName: "客户项目群",
			ConversationType: conversationTypeRoom,
			RoomID:           "wr_xxx",
			From:             Sender{UserID: "zhangsan", Name: "张三", Type: senderTypeInternal},
			ToList:           []Sender{{UserID: "lisi", Name: "李四", Type: senderTypeInternal}},
			MsgTime:          msgTime,
		},
		Body: "今天客户反馈了一个问题，需要确认日志。",
	})

	item := bucketItem(b)
	if item.ExternalID != "wecom-chat:wr_xxx:2026-07-07" {
		t.Fatalf("ExternalID = %q", item.ExternalID)
	}
	content := string(item.Content)
	if !strings.Contains(content, "# 企业微信会话：客户项目群 / 2026-07-07") {
		t.Fatalf("content missing title: %s", content)
	}
	if !strings.Contains(content, "[09:01:22] 张三（zhangsan）:") {
		t.Fatalf("content missing sender: %s", content)
	}
	if item.Metadata["participant_userids"] != "lisi,zhangsan" {
		t.Fatalf("participant_userids = %q", item.Metadata["participant_userids"])
	}
	if item.Metadata["sender_userids"] != "zhangsan" {
		t.Fatalf("sender_userids = %q", item.Metadata["sender_userids"])
	}
}

func TestNormalizeMessageRendersAttachmentsAndRevoke(t *testing.T) {
	image := normalizeMessage(ArchiveMessageEnvelope{MsgID: "img-1", MsgType: "image"})
	if image.Body != "[附件: image, msgid=img-1, 未解析]" {
		t.Fatalf("image body = %q", image.Body)
	}
	revoke := normalizeMessage(ArchiveMessageEnvelope{MsgID: "msg-2", Action: "revoke", MsgType: "text"})
	if revoke.Body != "[消息已撤回, msgid=msg-2]" {
		t.Fatalf("revoke body = %q", revoke.Body)
	}
	recallRevoke := normalizeMessage(ArchiveMessageEnvelope{MsgID: "msg-3", Action: "recall", MsgType: "revoke"})
	if recallRevoke.Body != "[消息已撤回, msgid=msg-3]" {
		t.Fatalf("recall revoke body = %q", recallRevoke.Body)
	}
}

func TestNormalizeMessageRecordsConversionError(t *testing.T) {
	msg := conversionErrorMessage(42, "bad-msg", errors.New("parse decrypted wecom message"))
	normalized := normalizeMessage(msg)
	if !normalized.Skipped || normalized.Error == "" || !strings.Contains(normalized.Body, "消息解析失败") {
		t.Fatalf("normalized = %#v", normalized)
	}
	bucket := newDayBucket(msg)
	addToBucket(bucket, normalized)
	if bucket.ConversionErrors != 1 || bucket.LastSeq != 42 {
		t.Fatalf("bucket errors=%d last_seq=%d", bucket.ConversionErrors, bucket.LastSeq)
	}
}

func parseConfigExpectError(cfg *types.DataSourceConfig) string {
	_, err := parseConfig(cfg)
	if err == nil {
		return ""
	}
	return err.Error()
}

func assertErrorRedacted(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("expected error")
	}
	msg := err.Error()
	if strings.Contains(msg, "top-secret") || strings.Contains(msg, "BEGIN PRIVATE KEY") {
		t.Fatalf("error leaked secret material: %q", msg)
	}
}
