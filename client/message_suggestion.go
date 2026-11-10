package client

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
)

type MessageSuggestionItem struct {
	ID               string   `json:"id"`
	Text             string   `json:"text"`
	Category         string   `json:"category,omitempty"`
	Source           string   `json:"source"`
	KnowledgeBaseIDs []string `json:"knowledge_base_ids,omitempty"`
}

type MessageSuggestionSet struct {
	ID                 string                  `json:"id"`
	SessionID          string                  `json:"session_id"`
	AssistantMessageID string                  `json:"assistant_message_id"`
	Status             string                  `json:"status"`
	AllowRegenerate    bool                    `json:"allow_regenerate"`
	SuppressionReason  string                  `json:"suppression_reason,omitempty"`
	Questions          []MessageSuggestionItem `json:"questions"`
}

type messageSuggestionResponse struct {
	Success bool                 `json:"success"`
	Data    MessageSuggestionSet `json:"data"`
}

func (c *Client) EnsureMessageSuggestions(
	ctx context.Context,
	sessionID string,
	messageID string,
	regenerate bool,
) (*MessageSuggestionSet, error) {
	path := fmt.Sprintf("/api/v1/sessions/%s/messages/%s/suggestions", url.PathEscape(sessionID), url.PathEscape(messageID))
	resp, err := c.doRequest(ctx, http.MethodPost, path, map[string]bool{"regenerate": regenerate}, nil)
	if err != nil {
		return nil, err
	}
	var response messageSuggestionResponse
	if err := parseResponse(resp, &response); err != nil {
		return nil, err
	}
	return &response.Data, nil
}

func (c *Client) GetMessageSuggestions(
	ctx context.Context,
	sessionID string,
	messageID string,
) (*MessageSuggestionSet, error) {
	path := fmt.Sprintf("/api/v1/sessions/%s/messages/%s/suggestions", url.PathEscape(sessionID), url.PathEscape(messageID))
	resp, err := c.doRequest(ctx, http.MethodGet, path, nil, nil)
	if err != nil {
		return nil, err
	}
	var response messageSuggestionResponse
	if err := parseResponse(resp, &response); err != nil {
		return nil, err
	}
	return &response.Data, nil
}

func (c *Client) RecordMessageSuggestionEvent(
	ctx context.Context,
	sessionID string,
	setID string,
	questionID string,
	eventType string,
) error {
	path := fmt.Sprintf("/api/v1/sessions/%s/suggestion-events", url.PathEscape(sessionID))
	resp, err := c.doRequest(ctx, http.MethodPost, path, map[string]string{
		"suggestion_set_id": setID,
		"question_id":       questionID,
		"event_type":        eventType,
	}, nil)
	if err != nil {
		return err
	}
	return parseResponse(resp, nil)
}
