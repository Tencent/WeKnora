package qqbot

import "encoding/json"

const (
	defaultAPIBaseURL = "https://api.sgroup.qq.com"
	appTokenURL       = "https://bots.qq.com/app/getAppAccessToken"
	defaultGatewayURL = "https://api.sgroup.qq.com/gateway"

	opDispatch        = 0
	opHeartbeat       = 1
	opIdentify        = 2
	opReconnect       = 7
	opInvalidSession  = 9
	opHello           = 10
	opHeartbeatACK    = 11
	intentGroupAndC2C = 1 << 25

	eventC2CMessageCreate     = "C2C_MESSAGE_CREATE"
	eventGroupAtMessageCreate = "GROUP_AT_MESSAGE_CREATE"

	extraKeyMessageID = "message_id"
	extraKeyChatKind  = "chat_kind"
)

type gatewayPayload struct {
	ID string          `json:"id,omitempty"`
	Op int             `json:"op"`
	D  json.RawMessage `json:"d,omitempty"`
	S  *int64          `json:"s,omitempty"`
	T  string          `json:"t,omitempty"`
}

type helloData struct {
	HeartbeatInterval int `json:"heartbeat_interval"`
}

type identifyData struct {
	Token   string `json:"token"`
	Intents int    `json:"intents"`
	Shard   []int  `json:"shard,omitempty"`
}

type tokenResponse struct {
	AccessToken string          `json:"access_token"`
	ExpiresIn   json.RawMessage `json:"expires_in"`
	Code        int             `json:"code"`
	Message     string          `json:"message"`
}

type gatewayResponse struct {
	URL string `json:"url"`
}

type messageEvent struct {
	ID          string         `json:"id"`
	Content     string         `json:"content"`
	GroupOpenID string         `json:"group_openid"`
	Author      qqbotAuthor    `json:"author"`
	Attachments []qqAttachment `json:"attachments"`
}

type qqbotAuthor struct {
	UserOpenID   string `json:"user_openid"`
	MemberOpenID string `json:"member_openid"`
	ID           string `json:"id"`
	Username     string `json:"username"`
	Bot          bool   `json:"bot"`
}

type qqAttachment struct {
	ContentType string `json:"content_type"`
	Filename    string `json:"filename"`
	Size        int64  `json:"size"`
	URL         string `json:"url"`
}

type sendMessageRequest struct {
	Content string `json:"content"`
	MsgType int    `json:"msg_type"`
	MsgID   string `json:"msg_id,omitempty"`
	MsgSeq  int    `json:"msg_seq,omitempty"`
}
