package wecom_chat_archive

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/datasource"
	"github.com/Tencent/WeKnora/internal/types"
)

const (
	virtualResourceAll       = "all"
	defaultTimezone          = "Asia/Shanghai"
	defaultFullSyncDays      = 90
	attachmentPolicyMetadata = "metadata_only"
	defaultFetchBatchSize    = 1000
	defaultMaxFetchBatches   = 200
	conversationTypeUnknown  = "unknown"
	conversationTypeRoom     = "room"
	conversationTypeSingle   = "single"
	senderTypeInternal       = "internal"
	senderTypeExternal       = "external"
	senderTypeUnknown        = "unknown"
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
	Proxy                    string   `json:"proxy"`
	ProxyPassword            string   `json:"proxy_password"`
	TimeoutSeconds           int      `json:"timeout_seconds"`
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

func (c *weComChatArchiveCursor) UnmarshalJSON(data []byte) error {
	type cursorAlias weComChatArchiveCursor
	var raw struct {
		cursorAlias
		LastSeq json.RawMessage `json:"last_seq"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	*c = weComChatArchiveCursor(raw.cursorAlias)
	if len(raw.LastSeq) == 0 || string(raw.LastSeq) == "null" {
		return nil
	}
	var seqString string
	if err := json.Unmarshal(raw.LastSeq, &seqString); err == nil {
		seq, parseErr := strconv.ParseUint(seqString, 10, 64)
		if parseErr != nil {
			return parseErr
		}
		c.LastSeq = seq
		return nil
	}
	var seqNumber json.Number
	if err := json.Unmarshal(raw.LastSeq, &seqNumber); err != nil {
		return err
	}
	seq, err := strconv.ParseUint(seqNumber.String(), 10, 64)
	if err != nil {
		return err
	}
	c.LastSeq = seq
	return nil
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
	ConversionError  error
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
		TimeoutSeconds:           5,
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
	if s.TimeoutSeconds <= 0 {
		s.TimeoutSeconds = 5
	}
}
