package service

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/redis/go-redis/v9"
)

const embedTokenBytes = 32

var (
	ErrEmbedChannelNotFound = errors.New("embed channel not found")
	ErrEmbedTokenInvalid    = errors.New("embed publish token is invalid")
	ErrEmbedChannelDisabled = errors.New("embed channel is disabled")
	ErrEmbedChunkNotFound   = errors.New("embed chunk not found")
	ErrEmbedChunkForbidden  = errors.New("embed chunk not accessible")
)

type embedChannelService struct {
	repo         interfaces.EmbedChannelRepository
	agentService interfaces.CustomAgentService
	chunkService interfaces.ChunkService
	redis        *redis.Client
}

func NewEmbedChannelService(
	repo interfaces.EmbedChannelRepository,
	agentService interfaces.CustomAgentService,
	chunkService interfaces.ChunkService,
	redisClient *redis.Client,
) interfaces.EmbedChannelService {
	return &embedChannelService{
		repo:         repo,
		agentService: agentService,
		chunkService: chunkService,
		redis:        redisClient,
	}
}

func generateEmbedPublishToken() (string, error) {
	buf := make([]byte, embedTokenBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return "em_" + base64.RawURLEncoding.EncodeToString(buf), nil
}

func (s *embedChannelService) Create(
	ctx context.Context, tenantID uint64, agentID string, req *types.EmbedChannel,
) (*types.EmbedChannel, string, error) {
	agentID = strings.TrimSpace(agentID)
	if _, err := s.ensureAgentOwned(ctx, tenantID, agentID); err != nil {
		return nil, "", err
	}
	token, err := generateEmbedPublishToken()
	if err != nil {
		return nil, "", err
	}
	originsJSON := req.AllowedOrigins
	if len(originsJSON) == 0 {
		originsJSON = []byte("[]")
	}
	ch := &types.EmbedChannel{
		TenantID:               tenantID,
		AgentID:                agentID,
		Name:                   strings.TrimSpace(req.Name),
		Enabled:                req.Enabled,
		PublishToken:           token,
		AllowedOrigins:         originsJSON,
		WelcomeMessage:         req.WelcomeMessage,
		RateLimitPerMinute:     req.RateLimitPerMinute,
		RateLimitPerDay:        req.RateLimitPerDay,
		PrimaryColor:           strings.TrimSpace(req.PrimaryColor),
		PageTitle:              strings.TrimSpace(req.PageTitle),
		HeaderTitleMode:        types.NormalizeEmbedHeaderTitleMode(req.HeaderTitleMode),
		ShowSuggestedQuestions: req.ShowSuggestedQuestions,
		WidgetPosition:         types.NormalizeEmbedWidgetPosition(req.WidgetPosition),
		AllowWebSearch:         req.AllowWebSearch,
		AllowMemory:            req.AllowMemory,
		AllowFileUpload:        req.AllowFileUpload,
		DefaultLocale:          types.NormalizeEmbedDefaultLocale(req.DefaultLocale),
	}
	if ch.RateLimitPerMinute <= 0 {
		ch.RateLimitPerMinute = 30
	}
	if ch.RateLimitPerDay <= 0 {
		ch.RateLimitPerDay = types.DefaultEmbedRateLimitPerDay
	}
	if err := s.repo.Create(ctx, ch); err != nil {
		return nil, "", err
	}
	return ch, token, nil
}

func (s *embedChannelService) ListByAgent(
	ctx context.Context, tenantID uint64, agentID string,
) ([]*types.EmbedChannel, error) {
	agentID = strings.TrimSpace(agentID)
	if _, err := s.ensureAgentOwned(ctx, tenantID, agentID); err != nil {
		return nil, err
	}
	return s.repo.ListByAgent(ctx, tenantID, agentID)
}

func (s *embedChannelService) ListByTenant(
	ctx context.Context, tenantID uint64,
) ([]*types.EmbedChannel, error) {
	return s.repo.ListByTenant(ctx, tenantID)
}

func (s *embedChannelService) Update(
	ctx context.Context, tenantID uint64, id string, req *types.EmbedChannel,
	enabled *bool, showSuggested *bool, allowWebSearch *bool, allowMemory *bool, allowFileUpload *bool,
	defaultLocale *string, webhookURL *string, webhookSecret *string,
) (*types.EmbedChannel, error) {
	ch, err := s.getOwned(ctx, tenantID, id)
	if err != nil {
		return nil, err
	}
	if req.Name != "" {
		ch.Name = strings.TrimSpace(req.Name)
	}
	ch.WelcomeMessage = req.WelcomeMessage
	ch.PrimaryColor = strings.TrimSpace(req.PrimaryColor)
	ch.PageTitle = strings.TrimSpace(req.PageTitle)
	ch.HeaderTitleMode = types.NormalizeEmbedHeaderTitleMode(req.HeaderTitleMode)
	if showSuggested != nil {
		ch.ShowSuggestedQuestions = *showSuggested
	}
	if allowWebSearch != nil {
		ch.AllowWebSearch = *allowWebSearch
	}
	if allowMemory != nil {
		ch.AllowMemory = *allowMemory
	}
	if allowFileUpload != nil {
		ch.AllowFileUpload = *allowFileUpload
	}
	if defaultLocale != nil {
		ch.DefaultLocale = types.NormalizeEmbedDefaultLocale(*defaultLocale)
	}
	if webhookURL != nil {
		trimmed := strings.TrimSpace(*webhookURL)
		if err := ValidateEmbedWebhookURL(trimmed); err != nil {
			return nil, err
		}
		ch.WebhookURL = trimmed
	}
	if webhookSecret != nil {
		ch.WebhookSecret = strings.TrimSpace(*webhookSecret)
	}
	if req.WidgetPosition != "" {
		ch.WidgetPosition = types.NormalizeEmbedWidgetPosition(req.WidgetPosition)
	}
	if enabled != nil {
		ch.Enabled = *enabled
	}
	if req.RateLimitPerMinute > 0 {
		ch.RateLimitPerMinute = req.RateLimitPerMinute
	}
	if req.RateLimitPerDay > 0 {
		ch.RateLimitPerDay = req.RateLimitPerDay
	}
	if req.AllowedOrigins != nil {
		if len(req.AllowedOrigins) == 0 {
			ch.AllowedOrigins = []byte("[]")
		} else {
			ch.AllowedOrigins = req.AllowedOrigins
		}
	}
	if trimmed := strings.TrimSpace(req.AgentID); trimmed != "" && trimmed != ch.AgentID {
		if _, err := s.ensureAgentOwned(ctx, tenantID, trimmed); err != nil {
			return nil, err
		}
		ch.AgentID = trimmed
	}
	if err := s.repo.Update(ctx, ch); err != nil {
		return nil, err
	}
	return ch, nil
}

func (s *embedChannelService) Delete(ctx context.Context, tenantID uint64, id string) error {
	if _, err := s.getOwned(ctx, tenantID, id); err != nil {
		return err
	}
	return s.repo.Delete(ctx, tenantID, id)
}

func (s *embedChannelService) RotateToken(
	ctx context.Context, tenantID uint64, id string,
) (*types.EmbedChannel, string, error) {
	ch, err := s.getOwned(ctx, tenantID, id)
	if err != nil {
		return nil, "", err
	}
	token, err := generateEmbedPublishToken()
	if err != nil {
		return nil, "", err
	}
	ch.PublishToken = token
	if err := s.repo.Update(ctx, ch); err != nil {
		return nil, "", err
	}
	return ch, token, nil
}

func (s *embedChannelService) LookupForEmbed(
	ctx context.Context, channelID, token string,
) (*types.EmbedChannel, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return nil, ErrEmbedTokenInvalid
	}
	ch, err := s.repo.GetByID(ctx, channelID)
	if err != nil {
		return nil, err
	}
	if ch == nil {
		return nil, ErrEmbedTokenInvalid
	}
	if !ch.Enabled {
		return nil, ErrEmbedChannelDisabled
	}
	if subtle.ConstantTimeCompare([]byte(ch.PublishToken), []byte(token)) != 1 {
		return nil, ErrEmbedTokenInvalid
	}
	return ch, nil
}

func (s *embedChannelService) PublicConfig(ctx context.Context, ch *types.EmbedChannel) types.EmbedChannelPublicConfig {
	kbIDs := s.resolveKnowledgeBaseIDs(ctx, ch)
	displayTitle, agentName, agentAvatar := s.resolveDisplayMeta(ctx, ch)
	agentWebSearchEnabled := false
	agentImageUploadEnabled := false
	if agent, err := s.agentService.GetAgentByID(ctx, ch.AgentID); err == nil && agent != nil {
		agentWebSearchEnabled = agent.Config.WebSearchEnabled
		agentImageUploadEnabled = agent.Config.ImageUploadEnabled
	}
	return types.EmbedChannelPublicConfig{
		ChannelID:               ch.ID,
		Name:                    ch.Name,
		DisplayTitle:            displayTitle,
		KnowledgeBaseIDs:        kbIDs,
		AgentID:                 ch.AgentID,
		AgentName:               agentName,
		AgentAvatar:             agentAvatar,
		WelcomeMessage:          ch.WelcomeMessage,
		PrimaryColor:            ch.PrimaryColor,
		PageTitle:               ch.PageTitle,
		HeaderTitleMode:         types.NormalizeEmbedHeaderTitleMode(ch.HeaderTitleMode),
		ShowSuggestedQuestions:  ch.ShowSuggestedQuestions,
		AllowedOrigins:          ch.AllowedOriginsList(),
		WidgetPosition:          types.NormalizeEmbedWidgetPosition(ch.WidgetPosition),
		AllowWebSearch:          ch.AllowWebSearch,
		AllowMemory:             ch.AllowMemory,
		AllowFileUpload:         ch.AllowFileUpload,
		AgentWebSearchEnabled:   agentWebSearchEnabled,
		AgentImageUploadEnabled: agentImageUploadEnabled,
		DefaultLocale:           types.NormalizeEmbedDefaultLocale(ch.DefaultLocale),
	}
}

func (s *embedChannelService) EmbedChunk(
	ctx context.Context, ch *types.EmbedChannel, chunkID string,
) (*types.Chunk, error) {
	chunkID = strings.TrimSpace(chunkID)
	if chunkID == "" {
		return nil, ErrEmbedChunkNotFound
	}
	chunk, err := s.chunkService.GetChunkByIDOnly(ctx, chunkID)
	if err != nil || chunk == nil {
		return nil, ErrEmbedChunkNotFound
	}
	if !s.chunkAllowedForEmbed(ctx, ch, chunk) {
		return nil, ErrEmbedChunkForbidden
	}
	return chunk, nil
}

func (s *embedChannelService) chunkAllowedForEmbed(
	ctx context.Context, ch *types.EmbedChannel, chunk *types.Chunk,
) bool {
	if chunk == nil || chunk.KnowledgeBaseID == "" {
		return false
	}
	// 显式重校验租户：GetChunkByIDOnly 无租户过滤，且 KBSelectionMode=="all"/默认
	// 分支会无条件放行，必须在此挡住跨租户 chunk id 撞库读取他人知识库正文。
	if ch == nil || chunk.TenantID != ch.TenantID {
		return false
	}
	allowedKBs := s.resolveKnowledgeBaseIDs(ctx, ch)
	if len(allowedKBs) > 0 {
		for _, kbID := range allowedKBs {
			if kbID == chunk.KnowledgeBaseID {
				return true
			}
		}
		return false
	}
	agent, err := s.agentService.GetAgentByID(ctx, ch.AgentID)
	if err != nil || agent == nil {
		return false
	}
	switch agent.Config.KBSelectionMode {
	case "none":
		return false
	case "selected":
		return false
	default:
		return true
	}
}

func (s *embedChannelService) SuggestedQuestions(
	ctx context.Context, ch *types.EmbedChannel, limit int,
) ([]types.SuggestedQuestion, error) {
	if ch == nil || !ch.ShowSuggestedQuestions {
		return nil, nil
	}
	if limit <= 0 {
		limit = 6
	}
	kbIDs := s.resolveKnowledgeBaseIDs(ctx, ch)
	return s.agentService.GetSuggestedQuestions(ctx, ch.AgentID, kbIDs, nil, nil, limit)
}

// EmbedDisplayTitle resolves the human-readable title for embed sessions and UI chrome.
func (s *embedChannelService) EmbedDisplayTitle(ctx context.Context, ch *types.EmbedChannel) string {
	title, _, _ := s.resolveDisplayMeta(ctx, ch)
	return title
}

func (s *embedChannelService) resolveDisplayMeta(
	ctx context.Context, ch *types.EmbedChannel,
) (displayTitle, agentName, agentAvatar string) {
	if pageTitle := strings.TrimSpace(ch.PageTitle); pageTitle != "" {
		displayTitle = pageTitle
	} else if name := strings.TrimSpace(ch.Name); name != "" {
		displayTitle = name
	}
	agent, err := s.agentService.GetAgentByID(ctx, ch.AgentID)
	if err == nil && agent != nil {
		agentName = strings.TrimSpace(agent.Name)
		agentAvatar = strings.TrimSpace(agent.Avatar)
		if displayTitle == "" && agentName != "" {
			displayTitle = agentName
		}
	}
	if displayTitle == "" {
		displayTitle = "AI Assistant"
	}
	return displayTitle, agentName, agentAvatar
}

func (s *embedChannelService) resolveKnowledgeBaseIDs(ctx context.Context, ch *types.EmbedChannel) []string {
	agent, err := s.agentService.GetAgentByID(ctx, ch.AgentID)
	if err == nil && agent != nil && agent.Config.KBSelectionMode == "selected" {
		return append([]string(nil), agent.Config.KnowledgeBases...)
	}
	return nil
}

func (s *embedChannelService) ensureAgentOwned(ctx context.Context, tenantID uint64, agentID string) (*types.CustomAgent, error) {
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return nil, apperrors.NewBadRequestError("agent_id is required")
	}
	agent, err := s.agentService.GetAgentByID(ctx, agentID)
	if err != nil {
		return nil, err
	}
	if agent == nil || agent.TenantID != tenantID {
		return nil, apperrors.NewNotFoundError("agent not found")
	}
	return agent, nil
}

func (s *embedChannelService) GetOwnedChannel(
	ctx context.Context, tenantID uint64, id string,
) (*types.EmbedChannel, error) {
	return s.getOwned(ctx, tenantID, id)
}

func (s *embedChannelService) getOwned(ctx context.Context, tenantID uint64, id string) (*types.EmbedChannel, error) {
	ch, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if ch == nil || ch.TenantID != tenantID {
		return nil, ErrEmbedChannelNotFound
	}
	return ch, nil
}

// EmbedSessionDescription returns the marker stored on embed-created sessions.
func EmbedSessionDescription(channelID string) string {
	return fmt.Sprintf("%s%s", types.EmbedSessionMarkerPrefix, channelID)
}
