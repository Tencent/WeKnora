package userinput

import (
	"context"
	"time"

	"github.com/Tencent/WeKnora/internal/event"
	"github.com/Tencent/WeKnora/internal/types"
)

// Mode controls how many predefined options the user may select.
type Mode string

const (
	ModeSingle    Mode = "single_choice"
	ModeMultiple  Mode = "multiple_choice"
	ModeShortText Mode = "short_text"
	ModeLongText  Mode = "long_text"
	ModeNumber    Mode = "number"
	ModeDate      Mode = "date"
)

// Option is one selectable answer presented to the user.
type Option struct {
	ID          string `json:"id"`
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
}

// Question is the validated structured prompt shown by the Web client.
type Question struct {
	Text           string                          `json:"question"`
	Mode           Mode                            `json:"mode"`
	FieldKey       string                          `json:"field_key,omitempty"`
	SchemaVersion  int64                           `json:"schema_version,omitempty"`
	GroupID        string                          `json:"question_group_id"`
	Index          int                             `json:"question_index"`
	Total          int                             `json:"question_total"`
	CompletedCount int                             `json:"completed_count,omitempty"`
	RemainingCount int                             `json:"remaining_count,omitempty"`
	Options        []Option                        `json:"options,omitempty"`
	Validation     types.AgentCollectionValidation `json:"validation,omitempty"`
	AllowOther     bool                            `json:"allow_other"`
	AllowSkip      bool                            `json:"allow_skip"`
}

// Answer is submitted by the session owner while the Agent is waiting.
type Answer struct {
	FieldKey          string   `json:"field_key,omitempty"`
	SchemaVersion     int64    `json:"schema_version,omitempty"`
	SelectedOptionIDs []string `json:"selected_option_ids"`
	Value             any      `json:"value,omitempty"`
	OtherText         string   `json:"other_text,omitempty"`
	Skipped           bool     `json:"skipped"`
}

// Status describes the terminal state of a pending question.
type Status string

const (
	StatusAnswered Status = "answered"
	StatusSkipped  Status = "skipped"
	StatusTimedOut Status = "timed_out"
	StatusCanceled Status = "canceled"
)

// Result is returned to the Agent as the ask_user tool result.
type Result struct {
	Status          Status   `json:"status"`
	FieldKey        string   `json:"field_key,omitempty"`
	SchemaVersion   int64    `json:"schema_version,omitempty"`
	QuestionGroupID string   `json:"question_group_id"`
	QuestionIndex   int      `json:"question_index"`
	QuestionTotal   int      `json:"question_total"`
	SelectedOptions []Option `json:"selected_options,omitempty"`
	OtherText       string   `json:"other_text,omitempty"`
	Value           any      `json:"value,omitempty"`
	Reason          string   `json:"reason,omitempty"`
}

type PendingSnapshot struct {
	PendingID          string    `json:"pending_id"`
	TenantID           uint64    `json:"tenant_id"`
	UserID             string    `json:"user_id"`
	SessionID          string    `json:"session_id"`
	AssistantMessageID string    `json:"assistant_message_id"`
	RequestID          string    `json:"request_id,omitempty"`
	ToolCallID         string    `json:"tool_call_id,omitempty"`
	Question           Question  `json:"question"`
	Status             string    `json:"status"`
	ExpiresAt          time.Time `json:"expires_at"`
}

// PendingRequest contains the live execution metadata needed by the gate.
type PendingRequest struct {
	TenantID           uint64
	UserID             string
	SessionID          string
	AssistantMessageID string
	RequestID          string
	ToolCallID         string
	EventBus           *event.EventBus
	Question           Question
}

// Requester is the narrow dependency used by the ask_user tool.
type Requester interface {
	RequestAndWait(context.Context, PendingRequest) (Result, error)
}

// Resolver is the narrow dependency used by the authenticated HTTP handler.
type Resolver interface {
	Resolve(tenantID uint64, userID, pendingID string, answer Answer) error
}

type PendingReader interface {
	GetPending(context.Context, uint64, string, string) (*PendingSnapshot, error)
}
