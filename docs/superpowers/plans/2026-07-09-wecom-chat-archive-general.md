# WeCom Chat Archive General Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a compile-safe first version of the `wecom_chat_archive` datasource connector that can be registered, configured, listed in the UI, and unit-tested without the official WeCom SDK.

**Architecture:** Implement the WeKnora connector surface and internal message aggregation pipeline behind an `ArchiveClient` interface. The first version uses a default client that returns a clear "SDK not configured" error, while tests inject fake clients to verify config parsing, resource listing, Markdown rendering, aggregation, and cursor behavior.

**Tech Stack:** Go 1.26, WeKnora datasource Connector interface, `types.DataSourceConfig`, `types.FetchedItem`, Vue 3 + TypeScript, TDesign, existing i18n files.

## Global Constraints

- Connector type is exactly `wecom_chat_archive`.
- Current `datasource.Connector` interface includes `ListResources(ctx, config, parentID)` and `ResolveResourceAncestors(ctx, config, resourceIDs)`.
- `types.Resource.Metadata` is `map[string]interface{}`.
- `types.FetchedItem.UpdatedAt` is `time.Time`.
- MVP records participant metadata for future ACL filtering but does not implement document-level ACL enforcement.
- MVP does not parse or download attachments.
- MVP does not use `FetchedItem.IsDeleted` for revoke messages.
- Do not log `secret`, `private_key`, message decryption keys, or full chat body.
- Keep official SDK/CGO out of this first implementation.
- Use TDD: write failing tests, verify red, implement, verify green.

---

## File Structure

Create:

- `internal/datasource/connector/wecom_chat_archive/types.go`: connector config, settings defaults, cursor, sender/message/bucket structs, config parsing.
- `internal/datasource/connector/wecom_chat_archive/client.go`: `ArchiveClient` interface and default unavailable client.
- `internal/datasource/connector/wecom_chat_archive/markdown.go`: message conversion, sender display, bucket-to-Markdown rendering, metadata rendering helpers.
- `internal/datasource/connector/wecom_chat_archive/connector.go`: WeKnora connector implementation, fake-client injection option, resource listing, fetch all/incremental.
- `internal/datasource/connector/wecom_chat_archive/connector_test.go`: unit tests for first implementation.

Modify:

- `internal/types/datasource.go`: add `ConnectorTypeWeComChatArchive`.
- `internal/datasource/connector.go`: add metadata registry entry.
- `internal/container/container.go`: import and register the connector.
- `frontend/src/views/knowledge/settings/DataSourceEditorDialog.vue`: add connector definition and per-connector defaults.
- `frontend/src/views/knowledge/settings/DataSourceTypeIcon.vue`: add fallback text for the connector.
- `frontend/src/i18n/locales/zh-CN.ts`: add Chinese labels.
- `frontend/src/i18n/locales/en-US.ts`: add English labels.
- `frontend/src/i18n/locales/ko-KR.ts`: add Korean labels.
- `frontend/src/i18n/locales/ru-RU.ts`: add Russian labels.

---

### Task 1: Backend Type Constant And Metadata

**Files:**
- Modify: `internal/types/datasource.go:17-29`
- Modify: `internal/datasource/connector.go:109-206`
- Test: `internal/datasource/connector_test.go`

**Interfaces:**
- Consumes: existing `datasource.ListAvailableConnectors() []ConnectorMetadata`
- Produces: `types.ConnectorTypeWeComChatArchive` constant and metadata entry with capabilities `[]string{"incremental"}`

- [ ] **Step 1: Write the failing test**

Add to `internal/datasource/connector_test.go`:

```go
func TestWeComChatArchiveMetadataRegistered(t *testing.T) {
	meta, ok := ConnectorMetadataRegistry[types.ConnectorTypeWeComChatArchive]
	if !ok {
		t.Fatalf("metadata for %q is not registered", types.ConnectorTypeWeComChatArchive)
	}
	if meta.Type != types.ConnectorTypeWeComChatArchive {
		t.Fatalf("Type = %q, want %q", meta.Type, types.ConnectorTypeWeComChatArchive)
	}
	if meta.Name != "企业微信会话存档" {
		t.Fatalf("Name = %q", meta.Name)
	}
	if meta.AuthType != "api_key" {
		t.Fatalf("AuthType = %q, want api_key", meta.AuthType)
	}
	if strings.Join(meta.Capabilities, ",") != "incremental" {
		t.Fatalf("Capabilities = %v, want [incremental]", meta.Capabilities)
	}
}
```

Ensure imports include `strings` if not already present:

```go
import (
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/datasource -run TestWeComChatArchiveMetadataRegistered -count=1`

Expected: FAIL with `undefined: types.ConnectorTypeWeComChatArchive` or missing metadata.

- [ ] **Step 3: Add connector type constant**

Modify `internal/types/datasource.go` const block:

```go
	ConnectorTypeRSS              = "rss"
	ConnectorTypeWeComChatArchive = "wecom_chat_archive"
```

- [ ] **Step 4: Add metadata registry entry**

Add to `ConnectorMetadataRegistry` in `internal/datasource/connector.go`:

```go
	types.ConnectorTypeWeComChatArchive: {
		Type:         types.ConnectorTypeWeComChatArchive,
		Name:         "企业微信会话存档",
		Description:  "从企业微信会话内容存档同步聊天记录到知识库",
		Icon:         "wecom",
		Priority:     2,
		AuthType:     "api_key",
		Capabilities: []string{"incremental"},
	},
```

Keep no `deletion_sync` capability.

- [ ] **Step 5: Run test to verify it passes**

Run: `go test ./internal/datasource -run TestWeComChatArchiveMetadataRegistered -count=1`

Expected: PASS.

- [ ] **Step 6: Commit**

Run:

```bash
git add internal/types/datasource.go internal/datasource/connector.go internal/datasource/connector_test.go
git commit -m "feat(datasource): register wecom chat archive metadata"
```

---

### Task 2: Config Parsing And Defaults

**Files:**
- Create: `internal/datasource/connector/wecom_chat_archive/types.go`
- Test: `internal/datasource/connector/wecom_chat_archive/connector_test.go`

**Interfaces:**
- Consumes: `types.DataSourceConfig`
- Produces: `Config`, `Settings`, `parseConfig(config *types.DataSourceConfig) (*Config, error)`, `weComChatArchiveCursor`

- [ ] **Step 1: Write the failing tests**

Create `internal/datasource/connector/wecom_chat_archive/connector_test.go`:

```go
package wecom_chat_archive

import (
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

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

func parseConfigExpectError(cfg *types.DataSourceConfig) string {
	_, err := parseConfig(cfg)
	if err == nil {
		return ""
	}
	return err.Error()
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/datasource/connector/wecom_chat_archive -run 'TestParseConfig' -count=1`

Expected: FAIL because package or `parseConfig` does not exist.

- [ ] **Step 3: Implement config parsing**

Create `internal/datasource/connector/wecom_chat_archive/types.go`:

```go
package wecom_chat_archive

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/datasource"
	"github.com/Tencent/WeKnora/internal/types"
)

const (
	virtualResourceAll        = "all"
	defaultTimezone           = "Asia/Shanghai"
	defaultFullSyncDays       = 90
	attachmentPolicyMetadata  = "metadata_only"
	defaultFetchBatchSize     = 1000
	defaultMaxFetchBatches    = 200
	conversationTypeUnknown   = "unknown"
	conversationTypeRoom      = "room"
	conversationTypeSingle    = "single"
	senderTypeInternal        = "internal"
	senderTypeExternal        = "external"
	senderTypeUnknown         = "unknown"
)

type Config struct {
	CorpID            string
	Secret            string
	PrivateKey        string
	PrivateKeyVersion string
	ResourceIDs       []string
	Settings          Settings
}

type Settings struct {
	SyncScope                string   `json:"sync_scope"`
	Aggregation              string   `json:"aggregation"`
	Timezone                 string   `json:"timezone"`
	FullSyncDays             int      `json:"full_sync_days"`
	IncludeMessageTypes      []string `json:"include_message_types"`
	AttachmentPolicy         string   `json:"attachment_policy"`
	IncludeSenderName        bool     `json:"include_sender_name"`
	IncludeSenderID          bool     `json:"include_sender_id"`
	IncludeRoomID            bool     `json:"include_room_id"`
	IncludeExternalUserID    bool     `json:"include_external_user_id"`
	SyncRevokeAsDelete       bool     `json:"sync_revoke_as_delete"`
	RecordParticipantsForACL bool     `json:"record_participants_for_acl"`
}

type credentials struct {
	CorpID            string `json:"corp_id"`
	Secret            string `json:"secret"`
	PrivateKey        string `json:"private_key"`
	PrivateKeyVersion string `json:"private_key_version"`
}

type weComChatArchiveCursor struct {
	LastSeq      uint64            `json:"last_seq"`
	LastMsgTime  int64             `json:"last_msg_time"`
	LastSyncTime string            `json:"last_sync_time"`
	DayBuckets   map[string]string `json:"day_buckets,omitempty"`
}

type Sender struct {
	UserID         string
	ExternalUserID string
	Name           string
	Type           string
}

type ArchiveMessageEnvelope struct {
	Seq              uint64
	MsgID            string
	Action           string
	MsgType          string
	ConversationID   string
	ConversationName string
	ConversationType string
	RoomID           string
	From             Sender
	ToList           []Sender
	MsgTime          time.Time
	Raw              json.RawMessage
}

type normalizedMessage struct {
	Envelope ArchiveMessageEnvelope
	Body     string
	Skipped  bool
	Error    string
}

type dayBucket struct {
	ConversationID   string
	ConversationName string
	ConversationType string
	Date             string
	Messages         []normalizedMessage
	FirstMsgTime     time.Time
	LastMsgTime      time.Time
	LastSeq          uint64
	ParticipantUsers map[string]struct{}
	ParticipantExts  map[string]struct{}
	ParticipantRooms map[string]struct{}
	SenderUsers      map[string]struct{}
	SenderExts       map[string]struct{}
	ConversionErrors int
	AttachmentCount  int
	RevokeCount      int
}

func parseConfig(config *types.DataSourceConfig) (*Config, error) {
	if config == nil {
		return nil, fmt.Errorf("%w: config is nil", datasource.ErrInvalidConfig)
	}
	credBytes, err := json.Marshal(config.Credentials)
	if err != nil {
		return nil, fmt.Errorf("marshal credentials: %w", err)
	}
	var creds credentials
	if err := json.Unmarshal(credBytes, &creds); err != nil {
		return nil, fmt.Errorf("parse wecom chat archive credentials: %w", err)
	}
	if strings.TrimSpace(creds.CorpID) == "" {
		return nil, fmt.Errorf("%w: corp_id is required", datasource.ErrInvalidCredentials)
	}
	if strings.TrimSpace(creds.Secret) == "" {
		return nil, fmt.Errorf("%w: secret is required", datasource.ErrInvalidCredentials)
	}
	if strings.TrimSpace(creds.PrivateKey) == "" {
		return nil, fmt.Errorf("%w: private_key is required", datasource.ErrInvalidCredentials)
	}
	if strings.TrimSpace(creds.PrivateKeyVersion) == "" {
		return nil, fmt.Errorf("%w: private_key_version is required", datasource.ErrInvalidCredentials)
	}

	settings := defaultSettings()
	if len(config.Settings) > 0 {
		settingsBytes, err := json.Marshal(config.Settings)
		if err != nil {
			return nil, fmt.Errorf("marshal settings: %w", err)
		}
		if err := json.Unmarshal(settingsBytes, &settings); err != nil {
			return nil, fmt.Errorf("parse wecom chat archive settings: %w", err)
		}
	}
	settings.applyDefaults()

	resourceIDs := config.ResourceIDs
	if len(resourceIDs) == 0 {
		resourceIDs = []string{virtualResourceAll}
	}

	return &Config{
		CorpID:            strings.TrimSpace(creds.CorpID),
		Secret:            strings.TrimSpace(creds.Secret),
		PrivateKey:        strings.TrimSpace(creds.PrivateKey),
		PrivateKeyVersion: strings.TrimSpace(creds.PrivateKeyVersion),
		ResourceIDs:       resourceIDs,
		Settings:          settings,
	}, nil
}

func defaultSettings() Settings {
	return Settings{
		SyncScope:                "all_archived_conversations",
		Aggregation:              "conversation_day",
		Timezone:                 defaultTimezone,
		FullSyncDays:             defaultFullSyncDays,
		IncludeMessageTypes:      []string{"text", "markdown", "link", "news", "mixed"},
		AttachmentPolicy:         attachmentPolicyMetadata,
		IncludeSenderName:        true,
		IncludeSenderID:          true,
		IncludeRoomID:            true,
		IncludeExternalUserID:    true,
		SyncRevokeAsDelete:       false,
		RecordParticipantsForACL: true,
	}
}

func (s *Settings) applyDefaults() {
	if strings.TrimSpace(s.Timezone) == "" {
		s.Timezone = defaultTimezone
	}
	if s.FullSyncDays <= 0 {
		s.FullSyncDays = defaultFullSyncDays
	}
	if strings.TrimSpace(s.AttachmentPolicy) == "" {
		s.AttachmentPolicy = attachmentPolicyMetadata
	}
	if len(s.IncludeMessageTypes) == 0 {
		s.IncludeMessageTypes = []string{"text", "markdown", "link", "news", "mixed"}
	}
	if strings.TrimSpace(s.SyncScope) == "" {
		s.SyncScope = "all_archived_conversations"
	}
	if strings.TrimSpace(s.Aggregation) == "" {
		s.Aggregation = "conversation_day"
	}
	if !s.IncludeSenderName && !s.IncludeSenderID && !s.IncludeExternalUserID && !s.IncludeRoomID {
		s.IncludeSenderName = true
		s.IncludeSenderID = true
		s.IncludeExternalUserID = true
		s.IncludeRoomID = true
	}
	if !s.RecordParticipantsForACL {
		s.RecordParticipantsForACL = true
	}
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/datasource/connector/wecom_chat_archive -run 'TestParseConfig' -count=1`

Expected: PASS.

- [ ] **Step 5: Commit**

Run:

```bash
git add internal/datasource/connector/wecom_chat_archive/types.go internal/datasource/connector/wecom_chat_archive/connector_test.go
git commit -m "feat(datasource): parse wecom chat archive config"
```

---

### Task 3: Client Boundary And Connector Skeleton

**Files:**
- Create: `internal/datasource/connector/wecom_chat_archive/client.go`
- Create: `internal/datasource/connector/wecom_chat_archive/connector.go`
- Modify: `internal/datasource/connector/wecom_chat_archive/connector_test.go`

**Interfaces:**
- Consumes: `Config`, `ArchiveMessageEnvelope`
- Produces: `ArchiveClient`, `NewConnector(options ...Option) *Connector`, `WithClientFactory`, connector methods `Type`, `Validate`, `ListResources`, `ResolveResourceAncestors`

- [ ] **Step 1: Write failing tests**

Append to `connector_test.go`:

```go
type fakeArchiveClient struct {
	validateErr error
	messages    []ArchiveMessageEnvelope
}

func (f *fakeArchiveClient) Validate(ctx context.Context) error { return f.validateErr }
func (f *fakeArchiveClient) FetchMessages(ctx context.Context, startSeq uint64, limit int) ([]ArchiveMessageEnvelope, bool, error) {
	return f.messages, false, nil
}
func (f *fakeArchiveClient) Close() error { return nil }

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
```

Add imports to `connector_test.go`:

```go
import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
)
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/datasource/connector/wecom_chat_archive -run 'TestConnector|TestValidate|TestListResources|TestResolve' -count=1`

Expected: FAIL because `NewConnector`, `ArchiveClient`, or methods are undefined.

- [ ] **Step 3: Implement client boundary**

Create `internal/datasource/connector/wecom_chat_archive/client.go`:

```go
package wecom_chat_archive

import (
	"context"
	"fmt"
)

type ArchiveClient interface {
	Validate(ctx context.Context) error
	FetchMessages(ctx context.Context, startSeq uint64, limit int) ([]ArchiveMessageEnvelope, bool, error)
	Close() error
}

type clientFactory func(cfg *Config) ArchiveClient

func newUnavailableClient(cfg *Config) ArchiveClient {
	return unavailableClient{}
}

type unavailableClient struct{}

func (unavailableClient) Validate(ctx context.Context) error {
	return fmt.Errorf("wecom chat archive SDK client is not configured in this build")
}

func (unavailableClient) FetchMessages(ctx context.Context, startSeq uint64, limit int) ([]ArchiveMessageEnvelope, bool, error) {
	return nil, false, fmt.Errorf("wecom chat archive SDK client is not configured in this build")
}

func (unavailableClient) Close() error { return nil }
```

- [ ] **Step 4: Implement connector skeleton**

Create `internal/datasource/connector/wecom_chat_archive/connector.go`:

```go
package wecom_chat_archive

import (
	"context"
	"fmt"

	"github.com/Tencent/WeKnora/internal/datasource"
	"github.com/Tencent/WeKnora/internal/types"
)

var _ datasource.Connector = (*Connector)(nil)

type Connector struct {
	newClient clientFactory
}

type Option func(*Connector)

func NewConnector(options ...Option) *Connector {
	c := &Connector{newClient: newUnavailableClient}
	for _, option := range options {
		option(c)
	}
	return c
}

func WithClientFactory(factory clientFactory) Option {
	return func(c *Connector) {
		if factory != nil {
			c.newClient = factory
		}
	}
}

func (c *Connector) Type() string { return types.ConnectorTypeWeComChatArchive }

func (c *Connector) Validate(ctx context.Context, config *types.DataSourceConfig) error {
	cfg, err := parseConfig(config)
	if err != nil {
		return err
	}
	client := c.newClient(cfg)
	defer client.Close()
	if err := client.Validate(ctx); err != nil {
		return fmt.Errorf("wecom chat archive connection failed: %w", err)
	}
	return nil
}

func (c *Connector) ListResources(ctx context.Context, config *types.DataSourceConfig, parentID string) ([]types.Resource, error) {
	if _, err := parseConfig(config); err != nil {
		return nil, err
	}
	if parentID != "" {
		return []types.Resource{}, nil
	}
	return []types.Resource{{
		ExternalID:  virtualResourceAll,
		Name:        "全部已授权会话",
		Type:        "wecom_chat_archive_scope",
		Description: "同步企业微信会话内容存档授权范围内的所有会话",
		Metadata: map[string]interface{}{
			"scope": "all_archived_conversations",
		},
	}}, nil
}

func (c *Connector) ResolveResourceAncestors(ctx context.Context, config *types.DataSourceConfig, resourceIDs []string) ([]string, error) {
	return []string{}, nil
}

func (c *Connector) FetchAll(ctx context.Context, config *types.DataSourceConfig, resourceIDs []string) ([]types.FetchedItem, error) {
	return nil, fmt.Errorf("wecom chat archive fetch is not implemented yet")
}

func (c *Connector) FetchIncremental(ctx context.Context, config *types.DataSourceConfig, cursor *types.SyncCursor) ([]types.FetchedItem, *types.SyncCursor, error) {
	return nil, nil, fmt.Errorf("wecom chat archive incremental fetch is not implemented yet")
}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/datasource/connector/wecom_chat_archive -run 'TestConnector|TestValidate|TestListResources|TestResolve' -count=1`

Expected: PASS.

- [ ] **Step 6: Commit**

Run:

```bash
git add internal/datasource/connector/wecom_chat_archive/client.go internal/datasource/connector/wecom_chat_archive/connector.go internal/datasource/connector/wecom_chat_archive/connector_test.go
git commit -m "feat(datasource): add wecom chat archive connector skeleton"
```

---

### Task 4: Markdown Rendering And Participant Metadata

**Files:**
- Create: `internal/datasource/connector/wecom_chat_archive/markdown.go`
- Modify: `internal/datasource/connector/wecom_chat_archive/connector_test.go`

**Interfaces:**
- Consumes: `ArchiveMessageEnvelope`, `dayBucket`, `Sender`
- Produces: `normalizeMessage`, `bucketItem`, `addToBucket`, stable metadata fields `participant_userids`, `sender_userids`, `participant_external_userids`

- [ ] **Step 1: Write failing tests**

Append to `connector_test.go`:

```go
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
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/datasource/connector/wecom_chat_archive -run 'TestBucketItem|TestNormalizeMessage' -count=1`

Expected: FAIL because rendering helpers are undefined.

- [ ] **Step 3: Implement rendering helpers**

Create `internal/datasource/connector/wecom_chat_archive/markdown.go`:

```go
package wecom_chat_archive

import (
	"bytes"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
)

func normalizeMessage(msg ArchiveMessageEnvelope) normalizedMessage {
	if strings.EqualFold(msg.Action, "revoke") {
		return normalizedMessage{Envelope: msg, Body: fmt.Sprintf("[消息已撤回, msgid=%s]", msg.MsgID)}
	}
	switch msg.MsgType {
	case "text", "markdown":
		return normalizedMessage{Envelope: msg, Body: extractText(msg.Raw)}
	case "link", "news":
		return normalizedMessage{Envelope: msg, Body: extractLinkSummary(msg.Raw)}
	case "mixed":
		body := extractText(msg.Raw)
		if strings.TrimSpace(body) == "" {
			body = fmt.Sprintf("[未支持 mixed 消息内容, msgid=%s]", msg.MsgID)
		}
		return normalizedMessage{Envelope: msg, Body: body}
	case "image", "voice", "video":
		label := "未解析"
		if msg.MsgType == "voice" {
			label = "未转写"
		}
		return normalizedMessage{Envelope: msg, Body: fmt.Sprintf("[附件: %s, msgid=%s, %s]", msg.MsgType, msg.MsgID, label), Skipped: true}
	case "file":
		return normalizedMessage{Envelope: msg, Body: fmt.Sprintf("[附件: file, msgid=%s, 未解析]", msg.MsgID), Skipped: true}
	default:
		return normalizedMessage{Envelope: msg, Body: fmt.Sprintf("[未支持消息类型: %s, msgid=%s]", msg.MsgType, msg.MsgID), Skipped: true}
	}
}

func extractText(raw []byte) string {
	if len(raw) == 0 {
		return ""
	}
	return strings.TrimSpace(string(raw))
}

func extractLinkSummary(raw []byte) string {
	text := strings.TrimSpace(string(raw))
	if text == "" {
		return "[链接]"
	}
	return text
}

func newDayBucket(msg ArchiveMessageEnvelope) *dayBucket {
	date := msg.MsgTime.Format("2006-01-02")
	if msg.ConversationType == "" {
		msg.ConversationType = conversationTypeUnknown
	}
	return &dayBucket{
		ConversationID:   msg.ConversationID,
		ConversationName: msg.ConversationName,
		ConversationType: msg.ConversationType,
		Date:             date,
		FirstMsgTime:     msg.MsgTime,
		LastMsgTime:      msg.MsgTime,
		ParticipantUsers: make(map[string]struct{}),
		ParticipantExts:  make(map[string]struct{}),
		ParticipantRooms: make(map[string]struct{}),
		SenderUsers:      make(map[string]struct{}),
		SenderExts:       make(map[string]struct{}),
	}
}

func addToBucket(bucket *dayBucket, msg normalizedMessage) {
	if bucket == nil {
		return
	}
	if bucket.FirstMsgTime.IsZero() || msg.Envelope.MsgTime.Before(bucket.FirstMsgTime) {
		bucket.FirstMsgTime = msg.Envelope.MsgTime
	}
	if msg.Envelope.MsgTime.After(bucket.LastMsgTime) {
		bucket.LastMsgTime = msg.Envelope.MsgTime
	}
	if msg.Envelope.Seq > bucket.LastSeq {
		bucket.LastSeq = msg.Envelope.Seq
	}
	if msg.Skipped {
		bucket.AttachmentCount++
	}
	if strings.EqualFold(msg.Envelope.Action, "revoke") {
		bucket.RevokeCount++
	}
	addSender(bucket, msg.Envelope.From, true)
	for _, sender := range msg.Envelope.ToList {
		addSender(bucket, sender, false)
	}
	if msg.Envelope.RoomID != "" {
		bucket.ParticipantRooms[msg.Envelope.RoomID] = struct{}{}
	}
	bucket.Messages = append(bucket.Messages, msg)
}

func addSender(bucket *dayBucket, sender Sender, isFrom bool) {
	if sender.UserID != "" {
		bucket.ParticipantUsers[sender.UserID] = struct{}{}
		if isFrom {
			bucket.SenderUsers[sender.UserID] = struct{}{}
		}
	}
	if sender.ExternalUserID != "" {
		bucket.ParticipantExts[sender.ExternalUserID] = struct{}{}
		if isFrom {
			bucket.SenderExts[sender.ExternalUserID] = struct{}{}
		}
	}
}

func bucketItem(bucket *dayBucket) types.FetchedItem {
	titleName := bucket.ConversationName
	if strings.TrimSpace(titleName) == "" {
		titleName = bucket.ConversationID
	}
	content := renderBucketMarkdown(bucket, titleName)
	return types.FetchedItem{
		ExternalID:       fmt.Sprintf("wecom-chat:%s:%s", bucket.ConversationID, bucket.Date),
		Title:            fmt.Sprintf("企业微信会话 %s %s", titleName, bucket.Date),
		Content:          []byte(content),
		ContentType:      "text/markdown",
		FileName:         fmt.Sprintf("wecom-chat-%s-%s.md", bucket.ConversationID, bucket.Date),
		URL:              fmt.Sprintf("wecom://chat/%s?date=%s", bucket.ConversationID, bucket.Date),
		UpdatedAt:        bucket.LastMsgTime,
		SourceResourceID: virtualResourceAll,
		Metadata:         bucketMetadata(bucket),
	}
}

func renderBucketMarkdown(bucket *dayBucket, titleName string) string {
	var b bytes.Buffer
	fmt.Fprintf(&b, "# 企业微信会话：%s / %s\n\n", titleName, bucket.Date)
	fmt.Fprintf(&b, "> conversation_id: %s\n", bucket.ConversationID)
	fmt.Fprintf(&b, "> conversation_type: %s\n", bucket.ConversationType)
	fmt.Fprintf(&b, "> message_count: %d\n", len(bucket.Messages))
	fmt.Fprintf(&b, "> time_range: %s - %s\n\n", bucket.FirstMsgTime.Format("15:04:05"), bucket.LastMsgTime.Format("15:04:05"))
	b.WriteString("## 消息记录\n\n")
	for _, msg := range bucket.Messages {
		fmt.Fprintf(&b, "[%s] %s:\n%s\n\n", msg.Envelope.MsgTime.Format("15:04:05"), displaySender(msg.Envelope.From), msg.Body)
	}
	return strings.TrimRight(b.String(), "\n") + "\n"
}

func displaySender(sender Sender) string {
	name := strings.TrimSpace(sender.Name)
	if sender.UserID != "" {
		if name == "" {
			name = sender.UserID
		}
		return fmt.Sprintf("%s（%s）", name, sender.UserID)
	}
	if sender.ExternalUserID != "" {
		if name == "" {
			name = "外部联系人"
		}
		return fmt.Sprintf("%s（external_userid: %s）", name, sender.ExternalUserID)
	}
	if name != "" {
		return name + "（unknown_sender）"
	}
	return "unknown_sender"
}

func bucketMetadata(bucket *dayBucket) map[string]string {
	return map[string]string{
		"source":                       "wecom_chat_archive",
		"conversation_id":              bucket.ConversationID,
		"conversation_type":            bucket.ConversationType,
		"date":                         bucket.Date,
		"message_count":                strconv.Itoa(len(bucket.Messages)),
		"first_msg_time":               bucket.FirstMsgTime.UTC().Format(time.RFC3339),
		"last_msg_time":                bucket.LastMsgTime.UTC().Format(time.RFC3339),
		"last_seq":                     strconv.FormatUint(bucket.LastSeq, 10),
		"participant_userids":          sortedKeys(bucket.ParticipantUsers),
		"participant_external_userids": sortedKeys(bucket.ParticipantExts),
		"participant_room_ids":         sortedKeys(bucket.ParticipantRooms),
		"participant_count":            strconv.Itoa(len(bucket.ParticipantUsers) + len(bucket.ParticipantExts)),
		"sender_userids":               sortedKeys(bucket.SenderUsers),
		"sender_external_userids":      sortedKeys(bucket.SenderExts),
		"sender_policy":                "real_name_and_userid",
	}
}

func sortedKeys(values map[string]struct{}) string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return strings.Join(keys, ",")
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/datasource/connector/wecom_chat_archive -run 'TestBucketItem|TestNormalizeMessage' -count=1`

Expected: PASS.

- [ ] **Step 5: Commit**

Run:

```bash
git add internal/datasource/connector/wecom_chat_archive/markdown.go internal/datasource/connector/wecom_chat_archive/connector_test.go
git commit -m "feat(datasource): render wecom chat archive markdown"
```

---

### Task 5: FetchAll And FetchIncremental With Fake Client

**Files:**
- Modify: `internal/datasource/connector/wecom_chat_archive/connector.go`
- Modify: `internal/datasource/connector/wecom_chat_archive/connector_test.go`

**Interfaces:**
- Consumes: `ArchiveClient.FetchMessages(ctx, startSeq, limit)` and `bucketItem`
- Produces: `FetchAll` returns recent conversation-day items; `FetchIncremental` returns items and a `types.SyncCursor`

- [ ] **Step 1: Write failing tests**

Replace `fakeArchiveClient.FetchMessages` in `connector_test.go` with this cursor-aware version:

```go
func (f *fakeArchiveClient) FetchMessages(ctx context.Context, startSeq uint64, limit int) ([]ArchiveMessageEnvelope, bool, error) {
	var out []ArchiveMessageEnvelope
	for _, msg := range f.messages {
		if msg.Seq >= startSeq {
			out = append(out, msg)
		}
	}
	return out, false, nil
}
```

Append tests:

```go
func TestFetchAllAggregatesRecentMessages(t *testing.T) {
	now := time.Now().UTC()
	c := NewConnector(WithClientFactory(func(cfg *Config) ArchiveClient {
		return &fakeArchiveClient{messages: []ArchiveMessageEnvelope{
			{Seq: 1, MsgID: "old", MsgType: "text", ConversationID: "wr_xxx", ConversationName: "客户项目群", ConversationType: conversationTypeRoom, From: Sender{UserID: "old"}, MsgTime: now.AddDate(0, 0, -100), Raw: []byte("old")},
			{Seq: 2, MsgID: "new", MsgType: "text", ConversationID: "wr_xxx", ConversationName: "客户项目群", ConversationType: conversationTypeRoom, From: Sender{UserID: "zhangsan", Name: "张三"}, MsgTime: now, Raw: []byte("new body")},
		}},
	})
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
		}},
	})
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
	if got := uint64(next.ConnectorCursor["last_seq"].(float64)); got != 11 {
		t.Fatalf("last_seq = %d, want 11", got)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/datasource/connector/wecom_chat_archive -run 'TestFetchAll|TestFetchIncremental' -count=1`

Expected: FAIL because fetch methods return not implemented.

- [ ] **Step 3: Implement fetch methods**

Replace `FetchAll` and `FetchIncremental` in `connector.go`, and add helper functions:

```go
func (c *Connector) FetchAll(ctx context.Context, config *types.DataSourceConfig, resourceIDs []string) ([]types.FetchedItem, error) {
	cfg, err := parseConfig(config)
	if err != nil {
		return nil, err
	}
	client := c.newClient(cfg)
	defer client.Close()
	start := time.Now().UTC().AddDate(0, 0, -cfg.Settings.FullSyncDays)
	items, _, err := c.fetchAndAggregate(ctx, client, cfg, 0, start)
	return items, err
}

func (c *Connector) FetchIncremental(ctx context.Context, config *types.DataSourceConfig, cursor *types.SyncCursor) ([]types.FetchedItem, *types.SyncCursor, error) {
	cfg, err := parseConfig(config)
	if err != nil {
		return nil, nil, err
	}
	prev := parseCursor(cursor)
	client := c.newClient(cfg)
	defer client.Close()
	items, next, err := c.fetchAndAggregate(ctx, client, cfg, prev.LastSeq+1, time.Time{})
	if err != nil {
		return nil, nil, err
	}
	if next.LastSeq < prev.LastSeq {
		next.LastSeq = prev.LastSeq
	}
	cursorMap := map[string]interface{}{
		"last_seq":       float64(next.LastSeq),
		"last_msg_time":  float64(next.LastMsgTime),
		"last_sync_time": next.LastSyncTime,
	}
	if len(next.DayBuckets) > 0 {
		cursorMap["day_buckets"] = next.DayBuckets
	}
	return items, &types.SyncCursor{LastSyncTime: time.Now().UTC(), ConnectorCursor: cursorMap}, nil
}

func (c *Connector) fetchAndAggregate(ctx context.Context, client ArchiveClient, cfg *Config, startSeq uint64, minTime time.Time) ([]types.FetchedItem, weComChatArchiveCursor, error) {
	loc, err := time.LoadLocation(cfg.Settings.Timezone)
	if err != nil {
		loc = time.FixedZone("CST", 8*3600)
	}
	buckets := make(map[string]*dayBucket)
	next := weComChatArchiveCursor{LastSyncTime: time.Now().UTC().Format(time.RFC3339), DayBuckets: make(map[string]string)}
	seq := startSeq
	for batch := 0; batch < defaultMaxFetchBatches; batch++ {
		messages, hasMore, err := client.FetchMessages(ctx, seq, defaultFetchBatchSize)
		if err != nil {
			return nil, weComChatArchiveCursor{}, err
		}
		for _, msg := range messages {
			if !minTime.IsZero() && msg.MsgTime.Before(minTime) {
				if msg.Seq >= seq {
					seq = msg.Seq + 1
				}
				continue
			}
			if msg.ConversationID == "" {
				continue
			}
			localTime := msg.MsgTime.In(loc)
			msg.MsgTime = localTime
			key := msg.ConversationID + ":" + localTime.Format("2006-01-02")
			bucket := buckets[key]
			if bucket == nil {
				bucket = newDayBucket(msg)
				buckets[key] = bucket
			}
			normalized := normalizeMessage(msg)
			addToBucket(bucket, normalized)
			if msg.Seq > next.LastSeq {
				next.LastSeq = msg.Seq
			}
			if msg.MsgTime.Unix() > next.LastMsgTime {
				next.LastMsgTime = msg.MsgTime.Unix()
			}
			if msg.Seq >= seq {
				seq = msg.Seq + 1
			}
		}
		if !hasMore {
			break
		}
	}
	keys := make([]string, 0, len(buckets))
	for key := range buckets {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	items := make([]types.FetchedItem, 0, len(keys))
	for _, key := range keys {
		bucket := buckets[key]
		item := bucketItem(bucket)
		items = append(items, item)
		next.DayBuckets[key] = item.ExternalID
	}
	return items, next, nil
}

func parseCursor(cursor *types.SyncCursor) weComChatArchiveCursor {
	if cursor == nil || cursor.ConnectorCursor == nil {
		return weComChatArchiveCursor{}
	}
	var out weComChatArchiveCursor
	bytes, err := json.Marshal(cursor.ConnectorCursor)
	if err != nil {
		return weComChatArchiveCursor{}
	}
	if err := json.Unmarshal(bytes, &out); err != nil {
		return weComChatArchiveCursor{}
	}
	return out
}
```

Add imports to `connector.go`:

```go
import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/Tencent/WeKnora/internal/datasource"
	"github.com/Tencent/WeKnora/internal/types"
)
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/datasource/connector/wecom_chat_archive -run 'TestFetchAll|TestFetchIncremental' -count=1`

Expected: PASS.

- [ ] **Step 5: Commit**

Run:

```bash
git add internal/datasource/connector/wecom_chat_archive/connector.go internal/datasource/connector/wecom_chat_archive/connector_test.go
git commit -m "feat(datasource): aggregate wecom chat archive fetches"
```

---

### Task 6: Register Connector In Container

**Files:**
- Modify: `internal/container/container.go:58-62`
- Modify: `internal/container/container.go:1363-1375`
- Test: `internal/datasource/connector/wecom_chat_archive/connector_test.go`

**Interfaces:**
- Consumes: `wecom_chat_archive.NewConnector()`
- Produces: runtime connector registry includes `wecom_chat_archive`

- [ ] **Step 1: Write failing registry test**

Append to `connector_test.go`:

```go
func TestConnectorSatisfiesDatasourceInterface(t *testing.T) {
	var _ datasource.Connector = NewConnector()
}
```

Add import:

```go
"github.com/Tencent/WeKnora/internal/datasource"
```

This test proves the connector can be registered.

- [ ] **Step 2: Run tests**

Run: `go test ./internal/datasource/connector/wecom_chat_archive -run TestConnectorSatisfiesDatasourceInterface -count=1`

Expected: PASS if previous skeleton is correct. If it fails, fix interface implementation before registering.

- [ ] **Step 3: Import connector in container**

Modify imports in `internal/container/container.go`:

```go
	wecomChatArchiveConnector "github.com/Tencent/WeKnora/internal/datasource/connector/wecom_chat_archive"
```

- [ ] **Step 4: Register connector**

Add to `initConnectorRegistry` after RSS or before future connectors:

```go
	if err := registry.Register(wecomChatArchiveConnector.NewConnector()); err != nil {
		errs = errors.Join(errs, fmt.Errorf("register wecom chat archive connector: %w", err))
	}
```

- [ ] **Step 5: Run focused backend tests**

Run: `go test ./internal/datasource ./internal/datasource/connector/wecom_chat_archive -count=1`

Expected: PASS.

- [ ] **Step 6: Commit**

Run:

```bash
git add internal/container/container.go internal/datasource/connector/wecom_chat_archive/connector_test.go
git commit -m "feat(datasource): register wecom chat archive connector"
```

---

### Task 7: Frontend Connector Option And Defaults

**Files:**
- Modify: `frontend/src/views/knowledge/settings/DataSourceEditorDialog.vue:333-408`
- Modify: `frontend/src/views/knowledge/settings/DataSourceTypeIcon.vue:16-26`
- Modify: locale files under `frontend/src/i18n/locales/`

**Interfaces:**
- Consumes: existing connector wizard field definitions
- Produces: visible `wecom_chat_archive` option with credential fields and safe defaults

- [ ] **Step 1: Add i18n labels first**

In `frontend/src/i18n/locales/zh-CN.ts`, add keys under `datasource.connector`, `datasource.connectorDesc`, and `datasource.field`:

```ts
wecom_chat_archive: "企业微信会话存档",
```

```ts
wecom_chat_archive: "从企业微信会话内容存档同步聊天记录",
```

```ts
corpId: "企业 ID",
wecomSecret: "Secret",
privateKeyVersion: "私钥版本",
privateKey: "私钥",
privateKeyHint: "粘贴企业微信会话内容存档私钥，凭证会加密保存。",
```

Add equivalent keys to English:

```ts
wecom_chat_archive: 'WeCom Chat Archive',
```

```ts
wecom_chat_archive: 'Sync chat records from WeCom chat archive',
```

```ts
corpId: 'Corp ID',
wecomSecret: 'Secret',
privateKeyVersion: 'Private Key Version',
privateKey: 'Private Key',
privateKeyHint: 'Paste the WeCom chat archive private key. Credentials are encrypted at rest.',
```

For Korean and Russian, add clear fallback English labels if no translation is available.

- [ ] **Step 2: Add connector definition**

In `DataSourceEditorDialog.vue`, add this object to `connectorDefs`:

```ts
  {
    type: 'wecom_chat_archive',
    available: true,
    docUrl: 'https://developer.work.weixin.qq.com/document/path/91360',
    permissionDocUrl: 'https://developer.work.weixin.qq.com/document/path/91360',
    permissionPageUrl: 'https://work.weixin.qq.com/wework_admin/frame#apps',
    requiredPermissions: [
      '会话内容存档授权范围',
      '会话内容存档 Secret',
      '会话内容存档私钥',
    ],
    fields: [
      { key: 'corp_id', labelKey: 'datasource.field.corpId', placeholder: 'wwxxxx' },
      { key: 'secret', labelKey: 'datasource.field.wecomSecret', placeholder: '', secret: true },
      { key: 'private_key_version', labelKey: 'datasource.field.privateKeyVersion', placeholder: '1' },
      { key: 'private_key', labelKey: 'datasource.field.privateKey', placeholder: '-----BEGIN PRIVATE KEY-----', secret: true, multiline: true, hintKey: 'datasource.field.privateKeyHint' },
    ],
  },
```

- [ ] **Step 3: Apply connector defaults on select**

Modify `selectType(def: ConnectorDef)` after setting credentials:

```ts
  if (def.type === 'wecom_chat_archive') {
    form.value.config.resource_ids = ['all']
    selectedResourceIds.value = ['all']
    form.value.config.settings = {
      sync_scope: 'all_archived_conversations',
      aggregation: 'conversation_day',
      timezone: 'Asia/Shanghai',
      full_sync_days: 90,
      include_message_types: ['text', 'markdown', 'link', 'news', 'mixed'],
      attachment_policy: 'metadata_only',
      include_sender_name: true,
      include_sender_id: true,
      include_room_id: true,
      include_external_user_id: true,
      sync_revoke_as_delete: false,
      record_participants_for_acl: true,
    }
    form.value.sync_schedule = '0 */30 * * * *'
    form.value.sync_mode = 'incremental'
    form.value.conflict_strategy = 'overwrite'
    form.value.sync_deletions = false
  }
```

- [ ] **Step 4: Add fallback icon text**

Modify `DataSourceTypeIcon.vue` fallback switch:

```ts
    case 'wecom_chat_archive':
      return '企'
```

- [ ] **Step 5: Run frontend checks**

Run: `npm --prefix frontend run type-check`

Expected: PASS.

If the project has no `type-check` script, run: `npm --prefix frontend run build`

Expected: PASS.

- [ ] **Step 6: Commit**

Run:

```bash
git add frontend/src/views/knowledge/settings/DataSourceEditorDialog.vue frontend/src/views/knowledge/settings/DataSourceTypeIcon.vue frontend/src/i18n/locales/zh-CN.ts frontend/src/i18n/locales/en-US.ts frontend/src/i18n/locales/ko-KR.ts frontend/src/i18n/locales/ru-RU.ts
git commit -m "feat(frontend): add wecom chat archive datasource option"
```

---

### Task 8: Final Verification

**Files:**
- Verify only; no planned edits.

**Interfaces:**
- Consumes: all previous tasks.
- Produces: passing focused backend and frontend checks.

- [ ] **Step 1: Run backend tests**

Run: `go test ./internal/datasource ./internal/datasource/connector/wecom_chat_archive ./internal/types -count=1`

Expected: PASS.

- [ ] **Step 2: Run frontend check**

Run: `npm --prefix frontend run type-check`

Expected: PASS.

If unavailable, run: `npm --prefix frontend run build`.

- [ ] **Step 3: Run git status**

Run: `git status --short`

Expected: only intentional files are modified; no generated files unless explicitly produced by build and intentionally kept.

- [ ] **Step 4: Manual smoke check**

Start/restart app locally and visit the datasource wizard. Confirm:

- Connector card shows 企业微信会话存档 / WeCom Chat Archive.
- Credential form has `corp_id`, `secret`, `private_key_version`, `private_key`.
- Selecting the connector defaults sync deletion to off and schedule to every 30 minutes.
- Testing connection with fake/empty credentials fails with a sanitized missing-field or SDK unavailable error.

- [ ] **Step 5: Commit any verification-only doc updates**

If no code changes were needed, do not create an empty commit.

---

## Self-Review Notes

- Spec coverage: this plan implements the general first version: type/metadata, config parsing, client boundary, connector skeleton, resource listing, aggregation, Markdown rendering, participant metadata, container registration, and frontend configuration. It intentionally excludes official SDK/CGO and document-level ACL enforcement.
- Placeholder scan: no `TBD`, `TODO`, or unspecified implementation steps remain.
- Type consistency: `ListResources` includes `parentID`; `ResolveResourceAncestors` is implemented; `Resource.Metadata` uses `map[string]interface{}`; `FetchedItem.UpdatedAt` uses `time.Time`.
