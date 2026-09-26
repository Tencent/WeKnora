package pluginapi

import (
	"encoding/json"
	"time"
)

// EventsPath receives the events a plugin subscribed to in
// permissions.events. Delivery is at least once: a delivery that fails with
// a retryable error (or times out) is sent again later with the same ID, so
// handlers must be idempotent on ID. A non-retryable error drops it.
const EventsPath = "/v1/events"

// Event types a plugin can subscribe to. The set only grows.
const (
	// EventKnowledgeIngested: a document finished processing and is
	// searchable. Data is KnowledgeEventData.
	EventKnowledgeIngested = "knowledge.ingested"
	// EventKnowledgeFailed: a document failed processing for good.
	// Data is KnowledgeEventData with Error.
	EventKnowledgeFailed = "knowledge.failed"
	// EventKnowledgeDeleted: a document was deleted. Data is
	// KnowledgeEventData.
	EventKnowledgeDeleted = "knowledge.deleted"
	// EventChatAnswered: an assistant answer was completed. Data is
	// ChatEventData, question and answer included.
	EventChatAnswered = "chat.answered"
)

// EventTypes lists every event type, for manifest validation.
var EventTypes = []string{EventKnowledgeIngested, EventKnowledgeFailed, EventKnowledgeDeleted, EventChatAnswered}

// EventDelivery is one event sent to a plugin. The tenant is the envelope's.
type EventDelivery struct {
	// ID identifies the event; retries keep it (idempotency key).
	ID         string    `json:"id"`
	Type       string    `json:"type"`
	OccurredAt time.Time `json:"occurredAt"`
	// Attempt counts deliveries of this event, from 1.
	Attempt int             `json:"attempt"`
	Data    json.RawMessage `json:"data,omitempty"`
}

// KnowledgeEventData describes a document of a knowledge base.
type KnowledgeEventData struct {
	KnowledgeBaseID string `json:"knowledgeBaseId"`
	KnowledgeID     string `json:"knowledgeId"`
	Title           string `json:"title,omitempty"`
	FileName        string `json:"fileName,omitempty"`
	FileType        string `json:"fileType,omitempty"`
	// Source is the URL a URL or data-source document came from.
	Source string `json:"source,omitempty"`
	// Error is why processing failed (knowledge.failed).
	Error string `json:"error,omitempty"`
}

// ChatEventData describes an answered question.
type ChatEventData struct {
	SessionID string `json:"sessionId"`
	// MessageID is the assistant message.
	MessageID string `json:"messageId"`
	AgentID   string `json:"agentId,omitempty"`
	UserID    string `json:"userId,omitempty"`
	Question  string `json:"question,omitempty"`
	Answer    string `json:"answer"`
}

// WebhookPath answers calls to webhook id (contributes.webhooks). Third
// parties call WeKnora at a secret per-workspace URL; WeKnora checks the
// URL, finds the workspace and relays the call here with the workspace's
// envelope. Verifying the sender (a signature header) is the plugin's job.
func WebhookPath(id string) string { return "/v1/webhooks/" + id }

// WebhookRequest is one inbound call.
type WebhookRequest struct {
	Method string `json:"method"`
	// Path is what followed the webhook URL, starting with "/" ("/" if
	// nothing did).
	Path  string `json:"path"`
	Query string `json:"query,omitempty"`
	// Headers are the request headers, one value each, cookies removed.
	Headers map[string]string `json:"headers,omitempty"`
	// Body is the raw body; JSON carries it as base64.
	Body []byte `json:"body,omitempty"`
}

// WebhookResponse is what the caller receives.
type WebhookResponse struct {
	// Status defaults to 200.
	Status int `json:"status,omitempty"`
	// ContentType of Body, if any.
	ContentType string `json:"contentType,omitempty"`
	Body        []byte `json:"body,omitempty"`
}
