package yunzhijia

// callbackMessage is the JSON structure that Yunzhijia posts to the webhook.
type callbackMessage struct {
	Type           int    `json:"type"`
	RobotID        string `json:"robotId"`
	RobotName      string `json:"robotName"`
	OperatorOpenid string `json:"operatorOpenid"`
	OperatorName   string `json:"operatorName"`
	Time           int64  `json:"time"`
	MsgID          string `json:"msgId"`
	Content        string `json:"content"`
	GroupType      int    `json:"groupType"`
}

// sendMessagePayload is the JSON body POSTed to sendMsgUrl to reply.
type sendMessagePayload struct {
	MsgType      int           `json:"msgtype"`
	Content      string        `json:"content"`
	NotifyParams []notifyParam `json:"notifyParams,omitempty"`
}

// notifyParam specifies recipients in a Yunzhijia send message request.
type notifyParam struct {
	Type   string   `json:"type"`
	Values []string `json:"values"`
}
