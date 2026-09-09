package service

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/google/uuid"
)

// Use a separate live-run namespace: ordinary chat steering must never reach
// an installer's maintenance shell through the session API.
func installSteerSession(sessionID string) string { return "skill-install:" + sessionID }

func (s *TenantSkillService) withInstallSteerLock(
	ctx context.Context,
	sessionID string,
	fn func(context.Context) error,
) error {
	return s.withSkillLock(ctx, "weknora-skill-steer-lock:"+sessionID, fn)
}

// SkillInstallGuidance describes one instruction and its delivery state.
type SkillInstallGuidance struct {
	ID      string `json:"id"`
	Content string `json:"content"`
	Status  string `json:"status"`
}

// SkillInstallGuidanceState exposes input availability and the current run's instructions.
type SkillInstallGuidanceState struct {
	Accepting bool                   `json:"accepting"`
	Messages  []SkillInstallGuidance `json:"messages"`
}

// InstallGuidance reads the instructions for the scoped skill's current install.
func (s *TenantSkillService) InstallGuidance(
	ctx context.Context,
	tenantID uint64,
	configID, skillID string,
) (*SkillInstallGuidanceState, error) {
	skill, err := s.GetSkill(ctx, tenantID, configID, skillID)
	if err != nil {
		return nil, err
	}
	if skill == nil {
		return nil, apperrors.NewNotFoundError("skill not found")
	}
	result := &SkillInstallGuidanceState{Messages: []SkillInstallGuidance{}}
	if s.streams == nil || skill.InstallSessionID == "" || skill.InstallMessageID == "" {
		return result, nil
	}
	err = s.withInstallSteerLock(ctx, skill.InstallSessionID, func(ctx context.Context) error {
		live, _, err := s.streams.GetLiveRun(ctx, installSteerSession(skill.InstallSessionID))
		if err != nil {
			return err
		}
		result.Accepting = skill.Status == types.SkillStatusInstalling && live == skill.InstallMessageID
		events, _, err := s.streams.GetSteerEvents(ctx, skill.InstallSessionID, skill.InstallMessageID, 0)
		if err != nil {
			return err
		}
		for _, evt := range events {
			status := "pending"
			if consumed, _ := evt.Data["consumed"].(bool); consumed {
				status = "injected"
			} else if !result.Accepting {
				status = "unprocessed"
			}
			result.Messages = append(
				result.Messages,
				SkillInstallGuidance{ID: evt.ID, Content: evt.Content, Status: status},
			)
		}
		return nil
	})
	return result, err
}

// SteerInstall queues an administrator instruction for exactly the expected install run.
func (s *TenantSkillService) SteerInstall(
	ctx context.Context,
	tenantID uint64,
	configID, skillID, expectedMessageID, steerID, content string,
) error {
	content = strings.TrimSpace(content)
	if content == "" || utf8.RuneCountInString(content) > 10000 {
		return apperrors.NewBadRequestError("guidance must contain 1 to 10000 characters")
	}
	if _, err := uuid.Parse(steerID); err != nil || expectedMessageID == "" {
		return apperrors.NewBadRequestError("steer_id and expected_message_id are required")
	}
	skill, err := s.GetSkill(ctx, tenantID, configID, skillID)
	if err != nil {
		return err
	}
	if skill == nil {
		return apperrors.NewNotFoundError("skill not found")
	}
	if s.streams == nil {
		return apperrors.NewServiceUnavailableError("install guidance is unavailable")
	}
	if skill.InstallSessionID == "" || skill.InstallMessageID != expectedMessageID {
		return apperrors.NewConflictError("the install run changed; refresh before sending guidance")
	}
	return s.withInstallSteerLock(ctx, skill.InstallSessionID, func(ctx context.Context) error {
		// Re-read inside the send/close lock, including the durable terminal state.
		current, err := s.GetSkill(ctx, tenantID, configID, skillID)
		if err != nil {
			return err
		}
		if current == nil || current.InstallMessageID != expectedMessageID {
			return apperrors.NewConflictError("the install run changed")
		}
		events, _, err := s.streams.GetSteerEvents(ctx, skill.InstallSessionID, expectedMessageID, 0)
		if err != nil {
			return err
		}
		pending := 0
		for _, evt := range events {
			if evt.ID == steerID {
				if evt.Content != content {
					return apperrors.NewConflictError("steer_id already belongs to another message")
				}
				return nil // an HTTP retry must not append twice
			}
			if consumed, _ := evt.Data["consumed"].(bool); !consumed {
				pending++
			}
		}
		live, _, err := s.streams.GetLiveRun(ctx, installSteerSession(skill.InstallSessionID))
		if err != nil {
			return err
		}
		if current.Status != types.SkillStatusInstalling || live != expectedMessageID {
			return apperrors.NewConflictError(
				"installer is not accepting input; wait for completion and reinstall with instructions",
			)
		}
		if pending >= 10 || len(events) >= 100 {
			return apperrors.NewBadRequestError("too many install guidance messages")
		}
		return s.streams.AppendSteerEvents(ctx, skill.InstallSessionID, expectedMessageID, []interfaces.StreamEvent{{
			ID: steerID, Content: content, Timestamp: time.Now(), Data: map[string]interface{}{"delivery": "inject"},
		}})
	})
}

type installSteerSink struct {
	service    *TenantSkillService
	transcript *installTranscript
	// Only the engine goroutine accesses these fields.
	guidance []string
	err      error
}

func (sink *installSteerSink) PollSteer(
	ctx context.Context,
	sessionID, messageID string,
	offset int,
) ([]map[string]interface{}, int, error) {
	// Refresh the dedicated marker while the engine is active, even when no
	// console is polling. There is only one engine per maintenance session.
	if err := sink.service.streams.SetLiveRun(ctx, installSteerSession(sessionID), messageID, ""); err != nil {
		sink.err = err
		return nil, offset, err
	}
	events, total, err := sink.service.streams.GetSteerEvents(ctx, sessionID, messageID, 0)
	if err != nil {
		sink.err = err
		return nil, offset, err
	}
	result := make([]map[string]interface{}, 0)
	for _, evt := range events {
		if consumed, _ := evt.Data["consumed"].(bool); !consumed {
			result = append(result, map[string]interface{}{"id": evt.ID, "content": evt.Content})
		}
	}
	return result, total, nil
}

func (sink *installSteerSink) PersistSteerMessage(
	ctx context.Context,
	sessionID, messageID, steerID, content string,
	_ types.MentionedItems,
	_ string,
) string {
	// A deterministic ID makes retries after a failed consumed write safe.
	id := uuid.NewSHA1(uuid.NameSpaceOID, []byte(sessionID+":"+messageID+":"+steerID)).String()
	_, err := sink.service.messages.CreateMessage(
		ctx,
		&types.Message{
			ID:          id,
			SessionID:   sessionID,
			Role:        "user",
			Content:     content,
			IsCompleted: true,
			CreatedAt:   time.Now(),
			UpdatedAt:   time.Now(),
		},
	)
	if err != nil {
		existing, getErr := sink.service.messages.GetMessage(ctx, sessionID, id)
		if getErr != nil || existing == nil || existing.SessionID != sessionID || existing.Content != content {
			sink.err = err
			return ""
		}
	}
	updated, err := sink.service.streams.UpdateSteerEventData(
		ctx,
		sessionID,
		messageID,
		steerID,
		map[string]interface{}{"consumed": true, "user_message_id": id},
	)
	if err != nil || !updated {
		sink.err = fmt.Errorf("record install guidance consumption: updated=%t: %v", updated, err)
		return ""
	}
	sink.guidance = append(sink.guidance, content)
	return id
}

// closeIfDrained and incoming sends use the same distributed lock. A send
// either belongs to another engine turn or is refused before being accepted.
func (sink *installSteerSink) closeIfDrained(ctx context.Context) (bool, error) {
	closed := false
	tr := sink.transcript
	err := sink.service.withInstallSteerLock(ctx, tr.sessionID, func(ctx context.Context) error {
		events, _, err := sink.service.streams.GetSteerEvents(ctx, tr.sessionID, tr.assistantMessageID, 0)
		if err != nil {
			return err
		}
		for _, evt := range events {
			if consumed, _ := evt.Data["consumed"].(bool); !consumed {
				return nil
			}
		}
		err = sink.service.streams.ClearLiveRun(ctx, installSteerSession(tr.sessionID), tr.assistantMessageID)
		closed = err == nil
		return err
	})
	return closed, err
}
