package service

import (
	"context"
	"errors"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// messageService implements the MessageService interface for managing messaging operations
// It handles creating, retrieving, updating, and deleting messages within sessions
type messageService struct {
	messageRepo interfaces.MessageRepository // Repository for message storage operations
	sessionRepo interfaces.SessionRepository // Repository for session validation
}

// NewMessageService creates a new message service instance with the required repositories
// Parameters:
//   - messageRepo: Repository for persisting and retrieving messages
//   - sessionRepo: Repository for validating session existence
//
// Returns an implementation of the MessageService interface
func NewMessageService(messageRepo interfaces.MessageRepository,
	sessionRepo interfaces.SessionRepository,
) interfaces.MessageService {
	return &messageService{
		messageRepo: messageRepo,
		sessionRepo: sessionRepo,
	}
}

// sessionTenantIDForLookup returns the tenant ID to use for session lookup.
// When SessionTenantIDContextKey is set (e.g. pipeline with shared agent), use it so session/message belong to session owner.
func sessionTenantIDForLookup(ctx context.Context) (uint64, bool) {
	if v := ctx.Value(types.SessionTenantIDContextKey); v != nil {
		if tid, ok := v.(uint64); ok && tid != 0 {
			return tid, true
		}
	}
	if v := ctx.Value(types.TenantIDContextKey); v != nil {
		if tid, ok := v.(uint64); ok {
			return tid, true
		}
	}
	return 0, false
}

// CreateMessage creates a new message within an existing session
// It validates that the session exists before creating the message
// Parameters:
//   - ctx: Context containing tenant information
//   - message: The message to be created
//
// Returns the created message or an error if creation fails
func (s *messageService) CreateMessage(ctx context.Context, message *types.Message) (*types.Message, error) {
	logger.Info(ctx, "Start creating message")
	logger.Infof(ctx, "Creating message for session ID: %s", message.SessionID)

	// Check if the session exists to validate the message belongs to a valid session
	tenantID := ctx.Value(types.TenantIDContextKey).(uint64)
	logger.Infof(ctx, "Checking if session exists, tenant ID: %d, session ID: %s", tenantID, message.SessionID)
	_, err := s.sessionRepo.Get(ctx, tenantID, message.SessionID)
	if err != nil {
		logger.Errorf(ctx, "Failed to get session: %v", err)
		return nil, err
	}

	// Create the message in the repository
	logger.Info(ctx, "Session exists, creating message")
	createdMessage, err := s.messageRepo.CreateMessage(ctx, message)
	if err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{
			"session_id": message.SessionID,
		})
		return nil, err
	}

	logger.Infof(ctx, "Message created successfully, ID: %s", createdMessage.ID)
	return createdMessage, nil
}

// GetMessage retrieves a specific message by its ID within a session
// Parameters:
//   - ctx: Context containing tenant information
//   - sessionID: The ID of the session containing the message
//   - messageID: The ID of the message to retrieve
//
// Returns the requested message or an error if retrieval fails
func (s *messageService) GetMessage(ctx context.Context, sessionID string, messageID string) (*types.Message, error) {
	logger.Info(ctx, "Start getting message")
	logger.Infof(ctx, "Getting message, session ID: %s, message ID: %s", sessionID, messageID)

	// Verify the session exists before attempting to retrieve the message
	tenantID := ctx.Value(types.TenantIDContextKey).(uint64)
	logger.Infof(ctx, "Checking if session exists, tenant ID: %d", tenantID)
	_, err := s.sessionRepo.Get(ctx, tenantID, sessionID)
	if err != nil {
		logger.Errorf(ctx, "Failed to get session: %v", err)
		return nil, err
	}

	// Retrieve the message from the repository
	logger.Info(ctx, "Session exists, getting message")
	message, err := s.messageRepo.GetMessage(ctx, sessionID, messageID)
	if err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{
			"session_id": sessionID,
			"message_id": messageID,
		})
		return nil, err
	}

	logger.Info(ctx, "Message retrieved successfully")
	return message, nil
}

// GetMessagesBySession retrieves paginated messages for a specific session
// Parameters:
//   - ctx: Context containing tenant information
//   - sessionID: The ID of the session to get messages from
//   - page: The page number for pagination (0-based)
//   - pageSize: The number of messages per page
//
// Returns a slice of messages or an error if retrieval fails
func (s *messageService) GetMessagesBySession(ctx context.Context,
	sessionID string, page int, pageSize int,
) ([]*types.Message, error) {
	logger.Info(ctx, "Start getting messages by session")
	logger.Infof(ctx, "Getting messages for session ID: %s, page: %d, pageSize: %d", sessionID, page, pageSize)

	// Verify the session exists before retrieving messages
	tenantID := ctx.Value(types.TenantIDContextKey).(uint64)
	logger.Infof(ctx, "Checking if session exists, tenant ID: %d", tenantID)
	_, err := s.sessionRepo.Get(ctx, tenantID, sessionID)
	if err != nil {
		logger.Errorf(ctx, "Failed to get session: %v", err)
		return nil, err
	}

	// Retrieve paginated messages
	logger.Info(ctx, "Session exists, getting messages")
	messages, err := s.messageRepo.GetMessagesBySession(ctx, sessionID, page, pageSize)
	if err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{
			"session_id": sessionID,
			"page":       page,
			"page_size":  pageSize,
		})
		return nil, err
	}

	logger.Infof(ctx, "Retrieved %d messages successfully", len(messages))
	return messages, nil
}

// GetRecentMessagesBySession retrieves the most recent messages from a session
// This is typically used for loading the initial conversation history
// Parameters:
//   - ctx: Context containing tenant information
//   - sessionID: The ID of the session to get messages from
//   - limit: Maximum number of messages to retrieve
//
// Returns a slice of recent messages or an error if retrieval fails
func (s *messageService) GetRecentMessagesBySession(ctx context.Context,
	sessionID string, limit int,
) ([]*types.Message, error) {
	logger.Info(ctx, "Start getting recent messages by session")
	logger.Infof(ctx, "Getting recent messages for session ID: %s, limit: %d", sessionID, limit)

	// Verify the session exists before retrieving messages (use SessionTenantID when set, e.g. pipeline with shared agent)
	tenantID, ok := sessionTenantIDForLookup(ctx)
	if !ok {
		logger.Error(ctx, "Tenant ID not found in context for session lookup")
		return nil, errors.New("tenant ID not found in context")
	}
	logger.Infof(ctx, "Checking if session exists, tenant ID: %d", tenantID)
	_, err := s.sessionRepo.Get(ctx, tenantID, sessionID)
	if err != nil {
		logger.Errorf(ctx, "Failed to get session: %v", err)
		return nil, err
	}

	// Retrieve the most recent messages
	logger.Info(ctx, "Session exists, getting recent messages")
	messages, err := s.messageRepo.GetRecentMessagesBySession(ctx, sessionID, limit)
	if err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{
			"session_id": sessionID,
			"limit":      limit,
		})
		return nil, err
	}

	logger.Infof(ctx, "Retrieved %d recent messages successfully", len(messages))
	return messages, nil
}

// GetMessagesBySessionBeforeTime retrieves messages sent before a specific time
// This is typically used for pagination when scrolling through conversation history
// Parameters:
//   - ctx: Context containing tenant information
//   - sessionID: The ID of the session to get messages from
//   - beforeTime: Timestamp to retrieve messages before
//   - limit: Maximum number of messages to retrieve
//
// Returns a slice of messages or an error if retrieval fails
func (s *messageService) GetMessagesBySessionBeforeTime(ctx context.Context,
	sessionID string, beforeTime time.Time, limit int,
) ([]*types.Message, error) {
	logger.Info(ctx, "Start getting messages before time")
	logger.Infof(ctx, "Getting messages before %v for session ID: %s, limit: %d", beforeTime, sessionID, limit)

	// Verify the session exists before retrieving messages (use SessionTenantID when set, e.g. pipeline with shared agent)
	tenantID, ok := sessionTenantIDForLookup(ctx)
	if !ok {
		logger.Error(ctx, "Tenant ID not found in context for session lookup")
		return nil, errors.New("tenant ID not found in context")
	}
	logger.Infof(ctx, "Checking if session exists, tenant ID: %d", tenantID)
	_, err := s.sessionRepo.Get(ctx, tenantID, sessionID)
	if err != nil {
		logger.Errorf(ctx, "Failed to get session: %v", err)
		return nil, err
	}

	// Retrieve messages before the specified time
	logger.Info(ctx, "Session exists, getting messages before time")
	messages, err := s.messageRepo.GetMessagesBySessionBeforeTime(ctx, sessionID, beforeTime, limit)
	if err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{
			"session_id":  sessionID,
			"before_time": beforeTime,
			"limit":       limit,
		})
		return nil, err
	}

	logger.Infof(ctx, "Retrieved %d messages before time successfully", len(messages))
	return messages, nil
}

// UpdateMessage updates an existing message's content or metadata
// Parameters:
//   - ctx: Context containing tenant information
//   - message: The message with updated fields
//
// Returns an error if the update fails
func (s *messageService) UpdateMessage(ctx context.Context, message *types.Message) error {
	logger.Info(ctx, "Start updating message")
	logger.Infof(ctx, "Updating message, ID: %s, session ID: %s", message.ID, message.SessionID)

	// Verify the session exists before updating the message
	tenantID := ctx.Value(types.TenantIDContextKey).(uint64)
	logger.Infof(ctx, "Checking if session exists, tenant ID: %d", tenantID)
	_, err := s.sessionRepo.Get(ctx, tenantID, message.SessionID)
	if err != nil {
		logger.Errorf(ctx, "Failed to get session: %v", err)
		return err
	}

	// Update the message in the repository
	logger.Info(ctx, "Session exists, updating message")
	err = s.messageRepo.UpdateMessage(ctx, message)
	if err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{
			"session_id": message.SessionID,
			"message_id": message.ID,
		})
		return err
	}

	logger.Info(ctx, "Message updated successfully")
	return nil
}

// DeleteMessage removes a message from a session
// Parameters:
//   - ctx: Context containing tenant information
//   - sessionID: The ID of the session containing the message
//   - messageID: The ID of the message to delete
//
// Returns an error if deletion fails
func (s *messageService) DeleteMessage(ctx context.Context, sessionID string, messageID string) error {
	logger.Info(ctx, "Start deleting message")
	logger.Infof(ctx, "Deleting message, session ID: %s, message ID: %s", sessionID, messageID)

	// Verify the session exists before deleting the message
	tenantID := ctx.Value(types.TenantIDContextKey).(uint64)
	logger.Infof(ctx, "Checking if session exists, tenant ID: %d", tenantID)
	_, err := s.sessionRepo.Get(ctx, tenantID, sessionID)
	if err != nil {
		logger.Errorf(ctx, "Failed to get session: %v", err)
		return err
	}

	// Delete the message from the repository
	logger.Info(ctx, "Session exists, deleting message")
	err = s.messageRepo.DeleteMessage(ctx, sessionID, messageID)
	if err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{
			"session_id": sessionID,
			"message_id": messageID,
		})
		return err
	}

	logger.Info(ctx, "Message deleted successfully")
	return nil
}
