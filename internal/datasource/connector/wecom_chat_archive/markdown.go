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
	if isRevokeMessage(msg) {
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
	case "conversion_error":
		return normalizedMessage{Envelope: msg, Body: extractText(msg.Raw), Skipped: true, Error: extractText(msg.Raw)}
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
	if msg.Error != "" {
		bucket.ConversionErrors++
	}
	if isRevokeMessage(msg.Envelope) {
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

func isRevokeMessage(msg ArchiveMessageEnvelope) bool {
	return strings.EqualFold(msg.Action, "revoke") ||
		(strings.EqualFold(msg.Action, "recall") && strings.EqualFold(msg.MsgType, "revoke"))
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
