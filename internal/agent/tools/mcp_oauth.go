package tools

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/agent/approval"
	"github.com/Tencent/WeKnora/internal/event"
	"github.com/Tencent/WeKnora/internal/mcp"
	"github.com/Tencent/WeKnora/internal/types"
	mcpclient "github.com/mark3labs/mcp-go/client"
)

const defaultMCPToolExecTimeout = 60 * time.Second

// MCPOAuthSession carries chat/session metadata so MCP connect and tool
// registration can pause for in-conversation OAuth. Nil disables the prompt.
type MCPOAuthSession struct {
	EventBus           *event.EventBus
	SessionID          string
	AssistantMessageID string
	UserID             string
	RequestID          string
	// ApprovalCtx is the parent ctx without per-operation timeouts; when nil,
	// the caller ctx is used for the OAuth wait.
	ApprovalCtx context.Context
	// ExecTimeout, when >0, caps the retry ctx after a successful authorization.
	ExecTimeout time.Duration
}

// oauthSessionFromToolExec builds an OAuth session from per-tool execution metadata.
func oauthSessionFromToolExec(ctx context.Context, meta *ToolExecContext) *MCPOAuthSession {
	if meta == nil || meta.EventBus == nil {
		return nil
	}
	approvalCtx := ctx
	if meta.ApprovalCtx != nil {
		approvalCtx = meta.ApprovalCtx
	}
	execTimeout := meta.ExecTimeout
	if execTimeout <= 0 {
		execTimeout = defaultMCPToolExecTimeout
	}
	return &MCPOAuthSession{
		EventBus:           meta.EventBus,
		SessionID:          meta.SessionID,
		AssistantMessageID: meta.AssistantMessageID,
		UserID:             meta.UserID,
		RequestID:          meta.RequestID,
		ApprovalCtx:        approvalCtx,
		ExecTimeout:        execTimeout,
	}
}

// oauthSessionForRegistration builds an OAuth session for tool discovery at agent startup.
func oauthSessionForRegistration(ctx context.Context, sess *MCPOAuthSession, retryTimeout time.Duration) *MCPOAuthSession {
	if sess == nil || sess.EventBus == nil {
		return nil
	}
	approvalCtx := sess.ApprovalCtx
	if approvalCtx == nil {
		approvalCtx = ctx
	}
	userID := sess.UserID
	if userID == "" {
		userID, _ = types.UserIDFromContext(ctx)
	}
	requestID := sess.RequestID
	if requestID == "" {
		requestID, _ = types.RequestIDFromContext(ctx)
	}
	return &MCPOAuthSession{
		EventBus:           sess.EventBus,
		SessionID:          sess.SessionID,
		AssistantMessageID: sess.AssistantMessageID,
		UserID:             userID,
		RequestID:          requestID,
		ApprovalCtx:        approvalCtx,
		ExecTimeout:        retryTimeout,
	}
}

// oauthWaiter is the subset of the approval gate used to pause while the user
// completes MCP OAuth. Accessed via type assertion so MCPApproval fakes stay unchanged.
type oauthWaiter interface {
	RequestOAuthAndWait(ctx context.Context, req approval.OAuthPendingRequest) (approval.Decision, error)
}

// getOrCreateMCPClientWithOAuthRetry connects to an MCP service and, when OAuth
// authorization is required, pauses for the in-conversation prompt before retrying once.
func getOrCreateMCPClientWithOAuthRetry(
	ctx context.Context,
	mcpManager *mcp.MCPManager,
	service *types.MCPService,
	gate approval.MCPApproval,
	oauthSess *MCPOAuthSession,
	mcpToolName, toolCallID string,
) (mcp.MCPClient, error) {
	client, err := mcpManager.GetOrCreateClient(ctx, service)
	if err == nil {
		return client, nil
	}
	if oauthSess == nil {
		return nil, err
	}

	retryCtx, cancel, ok := waitForMCPOAuthAuthorization(ctx, gate, oauthSess, service, mcpToolName, toolCallID, err)
	if !ok {
		return nil, err
	}
	defer cancel()

	_ = mcpManager.CloseClient(service.ID)
	return mcpManager.GetOrCreateClient(retryCtx, service)
}

func waitForMCPOAuthAuthorization(
	ctx context.Context,
	gate approval.MCPApproval,
	sess *MCPOAuthSession,
	service *types.MCPService,
	mcpToolName, toolCallID string,
	connectErr error,
) (context.Context, context.CancelFunc, bool) {
	noop := func() {}
	if sess == nil || service == nil || !service.AuthConfig.IsOAuth() || !isAuthorizationRequired(connectErr) {
		return ctx, noop, false
	}
	ow, ok := gate.(oauthWaiter)
	if !ok || ow == nil || sess.EventBus == nil {
		return ctx, noop, false
	}

	waitCtx := ctx
	if sess.ApprovalCtx != nil {
		waitCtx = sess.ApprovalCtx
	}

	tenantID, _ := types.TenantIDFromContext(ctx)
	userID := sess.UserID
	if userID == "" {
		userID, _ = types.UserIDFromContext(ctx)
	}
	requestID := sess.RequestID
	if requestID == "" {
		requestID, _ = types.RequestIDFromContext(ctx)
	}

	decision, waitErr := ow.RequestOAuthAndWait(waitCtx, approval.OAuthPendingRequest{
		TenantID:           tenantID,
		UserID:             userID,
		SessionID:          sess.SessionID,
		AssistantMessageID: sess.AssistantMessageID,
		RequestID:          requestID,
		EventBus:           sess.EventBus,
		ServiceID:          service.ID,
		ServiceName:        service.Name,
		MCPToolName:        mcpToolName,
		ToolCallID:         toolCallID,
	})
	if waitErr != nil || !decision.Approved {
		return ctx, noop, false
	}

	if sess.ApprovalCtx != nil && sess.ExecTimeout > 0 {
		freshCtx, cancel := context.WithTimeout(sess.ApprovalCtx, sess.ExecTimeout)
		return freshCtx, cancel, true
	}
	return ctx, noop, true
}

// oauthAwareConnectError turns a low-level MCP connect/call error into a
// message the agent (and ultimately the user) can act on.
func oauthAwareConnectError(service *types.MCPService, err error) string {
	if service.AuthConfig.IsOAuth() && isAuthorizationRequired(err) {
		return fmt.Sprintf(
			"MCP service %q requires OAuth authorization. Please open the service settings "+
				"and click \"Authorize\" to grant access, then retry.",
			service.Name,
		)
	}
	return fmt.Sprintf("Failed to connect to MCP service: %v", err)
}

func isAuthorizationRequired(err error) bool {
	if err == nil {
		return false
	}
	if mcpclient.IsOAuthAuthorizationRequiredError(err) || mcpclient.IsAuthorizationRequiredError(err) {
		return true
	}
	msg := err.Error()
	return strings.Contains(msg, "authorization required") ||
		strings.Contains(msg, "no valid token") ||
		strings.Contains(msg, "401")
}
