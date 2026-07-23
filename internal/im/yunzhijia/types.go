package yunzhijia

import "encoding/json"

// callbackMessage is the JSON structure that Yunzhijia posts to the webhook.
type callbackMessage struct {
	Type           int    `json:"type"`
	MsgType        int    `json:"msgType"`
	EID            string `json:"eid"`
	ClientID       string `json:"clientId"`
	RobotID        string `json:"robotId"`
	RobotName      string `json:"robotName"`
	OpenID         string `json:"openId"`
	GroupID        string `json:"groupId"`
	OperatorOpenid string `json:"operatorOpenid"`
	OperatorOID    string `json:"operatorOid"`
	OperatorID     string `json:"operatorId"`
	OperatorUserID string `json:"operatorUserId"`
	OperatorName   string `json:"operatorName"`
	SenderID       string `json:"senderId"`
	SenderName     string `json:"senderName"`
	Time           int64  `json:"time"`
	MsgID          string `json:"msgId"`
	Content        string `json:"content"`
	GroupType      int    `json:"groupType"`
	MsgParam       string `json:"msgParam"`
}

type messageParam struct {
	Desc       []messageParamDesc `json:"desc"`
	NotifyTo   []string           `json:"notifyTo"`
	NotifyType int                `json:"notifyType"`
}

type messageParamDesc struct {
	Type   string `json:"type"`
	Data   string `json:"data"`
	Start  int    `json:"start"`
	Length int    `json:"length"`
	Width  int    `json:"w"`
	Height int    `json:"h"`
}

// sendMessagePayload is the JSON body POSTed to sendMsgUrl to reply.
type sendMessagePayload struct {
	MsgType      int               `json:"msgtype"`
	Content      string            `json:"content"`
	NotifyParams []notifyParam     `json:"notifyParams,omitempty"`
	Param        *sendMessageParam `json:"param,omitempty"`
}

// notifyParam specifies recipients in a Yunzhijia send message request.
type notifyParam struct {
	Type   string   `json:"type"`
	Values []string `json:"values"`
}

// sendMessageParam carries extra rendering options for a Yunzhijia reply,
// such as requesting Markdown rendering of Content via formatType.
type sendMessageParam struct {
	FormatType string `json:"formatType,omitempty"`
}

type appAccessTokenResponse struct {
	Data      appAccessTokenData `json:"data"`
	Error     any                `json:"error"`
	ErrorCode int                `json:"errorCode"`
	Success   bool               `json:"success"`
}

type appAccessTokenData struct {
	AccessToken  string `json:"accessToken"`
	ExpireIn     int64  `json:"expireIn"`
	RefreshToken string `json:"refreshToken"`
}

func (m *messageParam) firstImage() (messageParamDesc, bool) {
	if m == nil {
		return messageParamDesc{}, false
	}
	for _, desc := range m.Desc {
		if desc.Type == "image" && desc.Data != "" {
			return desc, true
		}
	}
	return messageParamDesc{}, false
}

func parseMessageParam(raw string) (*messageParam, error) {
	if raw == "" {
		return nil, nil
	}
	var param messageParam
	if err := json.Unmarshal([]byte(raw), &param); err != nil {
		return nil, err
	}
	return &param, nil
}
