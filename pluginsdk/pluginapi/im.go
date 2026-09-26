package pluginapi

// IMCallbackPath is where WeKnora relays an IM platform's callback to IM
// channel id; IMSendPath is where it asks the plugin to reply.
func IMCallbackPath(id string) string { return "/v1/im/" + id + "/callback" }

// IMSendPath is the reply endpoint of IM channel id.
func IMSendPath(id string) string { return "/v1/im/" + id + "/send" }

// IMCallbackOutput is what a platform's callback amounts to: the HTTP
// answer the platform expects (a URL verification challenge, an ack) and,
// when it carried one, the user's message for WeKnora to answer.
type IMCallbackOutput struct {
	// Response is written back to the platform; nil answers 200 "{}".
	Response *WebhookResponse `json:"response,omitempty"`
	Message  *IMMessage       `json:"message,omitempty"`
}

// IMMessage is a user's message in a chat.
type IMMessage struct {
	UserID   string `json:"userId"`
	UserName string `json:"userName,omitempty"`
	// ChatID is the conversation (the user for direct chats).
	ChatID string `json:"chatId"`
	// ChatType is "direct" or "group".
	ChatType  string `json:"chatType,omitempty"`
	Content   string `json:"content"`
	MessageID string `json:"messageId,omitempty"`
	ThreadID  string `json:"threadId,omitempty"`
	// Extra carries what the plugin needs to reply (a response URL).
	Extra map[string]string `json:"extra,omitempty"`
}

// IMSendInput is a reply to a message.
type IMSendInput struct {
	// Message is the one being answered, as the callback reported it.
	Message IMMessage `json:"message"`
	// Content is the answer, Markdown.
	Content string `json:"content"`
}
