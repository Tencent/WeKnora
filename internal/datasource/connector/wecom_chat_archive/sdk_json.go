package wecom_chat_archive

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

type sdkChatData struct {
	Seq              uint64 `json:"seq"`
	MsgID            string `json:"msgid"`
	PublicKeyVersion int    `json:"publickey_ver"`
	EncryptRandomKey string `json:"encrypt_random_key"`
	EncryptChatMsg   string `json:"encrypt_chat_msg"`
}

type sdkChatDataResponse struct {
	ErrCode  int           `json:"errcode"`
	ErrMsg   string        `json:"errmsg"`
	ChatData []sdkChatData `json:"chatdata"`
}

type decryptedSDKMessage struct {
	MsgID    string          `json:"msgid"`
	Action   string          `json:"action"`
	From     string          `json:"from"`
	ToList   []string        `json:"tolist"`
	RoomID   string          `json:"roomid"`
	MsgTime  int64           `json:"msgtime"`
	MsgType  string          `json:"msgtype"`
	Text     *sdkTextMessage `json:"text"`
	Markdown *struct {
		Content string `json:"content"`
	} `json:"markdown"`
	Link *struct {
		Title       string `json:"title"`
		Desc        string `json:"description"`
		LinkURL     string `json:"link_url"`
		ImageURL    string `json:"image_url"`
		ImageData   string `json:"image_data"`
		MessageText string `json:"message_text"`
	} `json:"link"`
	News *struct {
		Items []struct {
			Title string `json:"title"`
			Desc  string `json:"description"`
			URL   string `json:"url"`
		} `json:"item"`
	} `json:"news"`
	Mixed *struct {
		Items []sdkMixedItem `json:"item"`
	} `json:"mixed"`
}

type sdkTextMessage struct {
	Content string `json:"content"`
}

type sdkMixedItem struct {
	Type string          `json:"type"`
	Text *sdkTextMessage `json:"text"`
}

type sdkConversionError struct {
	Seq   uint64
	MsgID string
	Err   error
}

type sdkDecryptKeyError struct {
	Err error
}

func (e sdkDecryptKeyError) Error() string { return fmt.Sprintf("decrypt key: %v", e.Err) }

func (e sdkDecryptKeyError) Unwrap() error { return e.Err }

func (e sdkConversionError) Error() string {
	return fmt.Sprintf("seq=%d msgid=%s: %v", e.Seq, e.MsgID, e.Err)
}

func (e sdkConversionError) Unwrap() error { return e.Err }

func parseChatDataResponse(data []byte) ([]sdkChatData, bool, error) {
	var response sdkChatDataResponse
	if err := json.Unmarshal(data, &response); err != nil {
		return nil, false, fmt.Errorf("parse wecom sdk chatdata response: %w", err)
	}
	if response.ErrCode != 0 {
		return nil, false, fmt.Errorf("wecom sdk chatdata error %d: %s", response.ErrCode, response.ErrMsg)
	}
	return response.ChatData, false, nil
}

func decodeDecryptedMessage(seq uint64, msgID string, payload []byte) (ArchiveMessageEnvelope, error) {
	var decrypted decryptedSDKMessage
	if err := json.Unmarshal(payload, &decrypted); err != nil {
		return ArchiveMessageEnvelope{}, fmt.Errorf("parse decrypted wecom message %s: %w", msgID, err)
	}
	if decrypted.MsgID == "" {
		decrypted.MsgID = msgID
	}

	msg := ArchiveMessageEnvelope{
		Seq:     seq,
		MsgID:   decrypted.MsgID,
		Action:  decrypted.Action,
		MsgType: decrypted.MsgType,
		RoomID:  decrypted.RoomID,
		From:    classifySender(decrypted.From),
		ToList:  classifySenderList(decrypted.ToList),
		MsgTime: time.UnixMilli(decrypted.MsgTime),
		Raw:     []byte(normalizedSDKMessageRaw(decrypted)),
	}
	msg.ConversationID, msg.ConversationType = sdkConversation(decrypted.From, decrypted.ToList, decrypted.RoomID)
	return msg, nil
}

func conversionErrorMessage(seq uint64, msgID string, err error) ArchiveMessageEnvelope {
	return ArchiveMessageEnvelope{
		Seq:              seq,
		MsgID:            msgID,
		Action:           "conversion_error",
		MsgType:          "conversion_error",
		ConversationID:   fmt.Sprintf("conversion-error:%d", seq),
		ConversationType: conversationTypeUnknown,
		MsgTime:          time.Now().UTC(),
		Raw:              []byte(fmt.Sprintf("[消息解析失败, seq=%d, msgid=%s, error=%s]", seq, msgID, err.Error())),
		ConversionError:  err,
	}
}

func classifySender(id string) Sender {
	id = strings.TrimSpace(id)
	if id == "" {
		return Sender{Type: senderTypeUnknown}
	}
	if strings.HasPrefix(id, "wm") || strings.HasPrefix(id, "wo") {
		return Sender{ExternalUserID: id, Type: senderTypeExternal}
	}
	return Sender{UserID: id, Type: senderTypeInternal}
}

func classifySenderList(ids []string) []Sender {
	senders := make([]Sender, 0, len(ids))
	for _, id := range ids {
		senders = append(senders, classifySender(id))
	}
	return senders
}

func sdkConversation(from string, toList []string, roomID string) (string, string) {
	roomID = strings.TrimSpace(roomID)
	if roomID != "" {
		return roomID, conversationTypeRoom
	}
	if strings.TrimSpace(from) == "" || len(toList) == 0 || strings.TrimSpace(toList[0]) == "" {
		return "", conversationTypeUnknown
	}
	participants := []string{strings.TrimSpace(from), strings.TrimSpace(toList[0])}
	sort.Strings(participants)
	return "single:" + strings.Join(participants, ":"), conversationTypeSingle
}

func normalizedSDKMessageRaw(msg decryptedSDKMessage) string {
	switch msg.MsgType {
	case "text":
		if msg.Text != nil {
			return strings.TrimSpace(msg.Text.Content)
		}
	case "markdown":
		if msg.Markdown != nil {
			return strings.TrimSpace(msg.Markdown.Content)
		}
	case "link":
		if msg.Link != nil {
			return joinNonEmpty(msg.Link.Title, msg.Link.Desc, msg.Link.LinkURL, msg.Link.MessageText)
		}
	case "news":
		if msg.News != nil {
			parts := make([]string, 0, len(msg.News.Items)*3)
			for _, item := range msg.News.Items {
				parts = append(parts, item.Title, item.Desc, item.URL)
			}
			return joinNonEmpty(parts...)
		}
	case "mixed":
		if msg.Mixed != nil {
			parts := make([]string, 0, len(msg.Mixed.Items))
			for _, item := range msg.Mixed.Items {
				if item.Type == "text" && item.Text != nil {
					parts = append(parts, item.Text.Content)
				}
			}
			return joinNonEmpty(parts...)
		}
	}
	return ""
}

func joinNonEmpty(values ...string) string {
	parts := make([]string, 0, len(values))
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			parts = append(parts, value)
		}
	}
	return strings.Join(parts, "\n")
}
