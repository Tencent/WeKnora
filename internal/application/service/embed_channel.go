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
	ErrEmbedChannelNotFound  = errors.New("embed channel not found")
	ErrEmbedTokenInvalid     = errors.New("embed publish token is invalid")
	ErrEmbedChannelDisabled  = errors.New("embed channel is disabled")
)

type embedChannelService struct {
	repo         interfaces.EmbedChannelRepository
	agentService interfaces.CustomAgentService
	redis        *redis.Client
}

func NewEmbedChannelService(
	repo interfaces.EmbedChannelRepository,
	agentService interfaces.CustomAgentService,
	redisClient *redis.Client,
) interfaces.EmbedChannelService {
	return &embedChannelService{repo: repo, agentService: agentService, redis: redisClient}
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
		TenantID:           tenantID,
		AgentID:            agentID,
		Name:               strings.TrimSpace(req.Name),
		Enabled:            req.Enabled,
		PublishToken:       token,
		AllowedOrigins:     originsJSON,
		WelcomeMessage:     req.WelcomeMessage,
		RateLimitPerMinute: req.RateLimitPerMinute,
		PrimaryColor:       strings.TrimSpace(req.PrimaryColor),
		PageTitle:          strings.TrimSpace(req.PageTitle),
		WidgetPosition:     types.NormalizeEmbedWidgetPosition(req.WidgetPosition),
	}
	if ch.RateLimitPerMinute <= 0 {
		ch.RateLimitPerMinute = 30
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

func (s *embedChannelService) Update(
	ctx context.Context, tenantID uint64, id string, req *types.EmbedChannel, enabled *bool,
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
	if req.WidgetPosition != "" {
		ch.WidgetPosition = types.NormalizeEmbedWidgetPosition(req.WidgetPosition)
	}
	if enabled != nil {
		ch.Enabled = *enabled
	}
	if req.RateLimitPerMinute > 0 {
		ch.RateLimitPerMinute = req.RateLimitPerMinute
	}
	if req.AllowedOrigins != nil {
		if len(req.AllowedOrigins) == 0 {
			ch.AllowedOrigins = []byte("[]")
		} else {
			ch.AllowedOrigins = req.AllowedOrigins
		}
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
	primaryKB := ""
	if len(kbIDs) > 0 {
		primaryKB = kbIDs[0]
	} else if ch.KnowledgeBaseID != "" {
		primaryKB = ch.KnowledgeBaseID
		kbIDs = []string{primaryKB}
	}
	return types.EmbedChannelPublicConfig{
		ChannelID:        ch.ID,
		Name:             ch.Name,
		KnowledgeBaseID:  primaryKB,
		KnowledgeBaseIDs: kbIDs,
		AgentID:          ch.AgentID,
		WelcomeMessage:   ch.WelcomeMessage,
		PrimaryColor:     ch.PrimaryColor,
		PageTitle:        ch.PageTitle,
		AllowedOrigins:   ch.AllowedOriginsList(),
	}
}

func (s *embedChannelService) resolveKnowledgeBaseIDs(ctx context.Context, ch *types.EmbedChannel) []string {
	if ch.KnowledgeBaseID != "" {
		return []string{ch.KnowledgeBaseID}
	}
	agent, err := s.agentService.GetAgentByID(ctx, ch.AgentID)
	if err != nil || agent == nil {
		return nil
	}
	if agent.Config.KBSelectionMode == "selected" {
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
