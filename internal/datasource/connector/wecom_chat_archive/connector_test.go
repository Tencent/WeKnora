package wecom_chat_archive

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
)

type fakeArchiveClient struct {
	validateErr error
	messages    []ArchiveMessageEnvelope
}

func (f *fakeArchiveClient) Validate(ctx context.Context) error { return f.validateErr }
func (f *fakeArchiveClient) FetchMessages(ctx context.Context, startSeq uint64, limit int) ([]ArchiveMessageEnvelope, bool, error) {
	return f.messages, false, nil
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
}

func parseConfigExpectError(cfg *types.DataSourceConfig) string {
	_, err := parseConfig(cfg)
	if err == nil {
		return ""
	}
	return err.Error()
}
