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
)

const embedTokenBytes = 32

var (
	ErrEmbedChannelNotFound = errors.New("embed channel not found")
	ErrEmbedTokenInvalid    = errors.New("embed publish token is invalid")
)

type embedChannelService struct {
	repo      interfaces.EmbedChannelRepository
	kbService interfaces.KnowledgeBaseService
}

func NewEmbedChannelService(
	repo interfaces.EmbedChannelRepository,
	kbService interfaces.KnowledgeBaseService,
) interfaces.EmbedChannelService {
	return &embedChannelService{repo: repo, kbService: kbService}
}

func generateEmbedPublishToken() (string, error) {
	buf := make([]byte, embedTokenBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return "em_" + base64.RawURLEncoding.EncodeToString(buf), nil
}

func (s *embedChannelService) Create(
	ctx context.Context, tenantID uint64, kbID string, req *types.EmbedChannel,
) (*types.EmbedChannel, string, error) {
	if _, err := s.ensureKBOwned(ctx, tenantID, kbID); err != nil {
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
		KnowledgeBaseID:    kbID,
		AgentID:            strings.TrimSpace(req.AgentID),
		Name:               strings.TrimSpace(req.Name),
		Enabled:            req.Enabled,
		PublishToken:       token,
		AllowedOrigins:     originsJSON,
		WelcomeMessage:     req.WelcomeMessage,
		RateLimitPerMinute: req.RateLimitPerMinute,
	}
	if ch.AgentID == "" {
		ch.AgentID = types.BuiltinQuickAnswerID
	}
	if ch.RateLimitPerMinute <= 0 {
		ch.RateLimitPerMinute = 30
	}
	if err := s.repo.Create(ctx, ch); err != nil {
		return nil, "", err
	}
	return ch, token, nil
}

func (s *embedChannelService) ListByKnowledgeBase(
	ctx context.Context, tenantID uint64, kbID string,
) ([]*types.EmbedChannel, error) {
	if _, err := s.ensureKBOwned(ctx, tenantID, kbID); err != nil {
		return nil, err
	}
	return s.repo.ListByKnowledgeBase(ctx, tenantID, kbID)
}

func (s *embedChannelService) Update(
	ctx context.Context, tenantID uint64, id string, req *types.EmbedChannel,
) (*types.EmbedChannel, error) {
	ch, err := s.getOwned(ctx, tenantID, id)
	if err != nil {
		return nil, err
	}
	if req.Name != "" {
		ch.Name = strings.TrimSpace(req.Name)
	}
	if req.AgentID != "" {
		ch.AgentID = strings.TrimSpace(req.AgentID)
	}
	ch.Enabled = req.Enabled
	ch.WelcomeMessage = req.WelcomeMessage
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
	if ch == nil || !ch.Enabled {
		return nil, ErrEmbedTokenInvalid
	}
	if subtle.ConstantTimeCompare([]byte(ch.PublishToken), []byte(token)) != 1 {
		return nil, ErrEmbedTokenInvalid
	}
	return ch, nil
}

func (s *embedChannelService) PublicConfig(ch *types.EmbedChannel) types.EmbedChannelPublicConfig {
	return types.EmbedChannelPublicConfig{
		ChannelID:       ch.ID,
		Name:            ch.Name,
		KnowledgeBaseID: ch.KnowledgeBaseID,
		AgentID:         ch.AgentID,
		WelcomeMessage:  ch.WelcomeMessage,
		AllowedOrigins:  ch.AllowedOriginsList(),
	}
}

func (s *embedChannelService) ensureKBOwned(ctx context.Context, tenantID uint64, kbID string) (*types.KnowledgeBase, error) {
	kb, err := s.kbService.GetKnowledgeBaseByID(ctx, kbID)
	if err != nil {
		return nil, err
	}
	if kb == nil || kb.TenantID != tenantID {
		return nil, apperrors.NewNotFoundError("knowledge base not found")
	}
	return kb, nil
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
