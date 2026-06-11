package service

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/redis/go-redis/v9"
)

const (
	embedSessionTokenPrefix = "ems_"
	embedSessionRedisPrefix = "embed:session:"
	embedSessionTTL         = 30 * time.Minute
)

var ErrEmbedSessionUnavailable = errors.New("embed session tokens unavailable")

func generateEmbedSessionToken() (string, error) {
	buf := make([]byte, embedTokenBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return embedSessionTokenPrefix + base64.RawURLEncoding.EncodeToString(buf), nil
}

// IssueSessionToken mints a short-lived session token bound to channelID.
func (s *embedChannelService) IssueSessionToken(ctx context.Context, channelID string) (string, int, error) {
	if s.redis == nil {
		return "", 0, ErrEmbedSessionUnavailable
	}
	channelID = strings.TrimSpace(channelID)
	if channelID == "" {
		return "", 0, ErrEmbedTokenInvalid
	}
	token, err := generateEmbedSessionToken()
	if err != nil {
		return "", 0, err
	}
	key := embedSessionRedisPrefix + token
	if err := s.redis.Set(ctx, key, channelID, embedSessionTTL).Err(); err != nil {
		return "", 0, err
	}
	return token, int(embedSessionTTL.Seconds()), nil
}

// ResolveSessionToken returns the channel ID stored for a session token.
func (s *embedChannelService) ResolveSessionToken(ctx context.Context, token string) (string, error) {
	token = strings.TrimSpace(token)
	if !strings.HasPrefix(token, embedSessionTokenPrefix) {
		return "", ErrEmbedTokenInvalid
	}
	if s.redis == nil {
		return "", ErrEmbedSessionUnavailable
	}
	channelID, err := s.redis.Get(ctx, embedSessionRedisPrefix+token).Result()
	if err == redis.Nil {
		return "", ErrEmbedTokenInvalid
	}
	if err != nil {
		return "", err
	}
	channelID = strings.TrimSpace(channelID)
	if channelID == "" {
		return "", ErrEmbedTokenInvalid
	}
	return channelID, nil
}

// LookupEnabledChannel loads an embed channel and verifies it is enabled.
func (s *embedChannelService) LookupEnabledChannel(ctx context.Context, channelID string) (*types.EmbedChannel, error) {
	channelID = strings.TrimSpace(channelID)
	if channelID == "" {
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
	return ch, nil
}

// IsEmbedSessionToken reports whether token is a session token (ems_ prefix).
func IsEmbedSessionToken(token string) bool {
	return strings.HasPrefix(strings.TrimSpace(token), embedSessionTokenPrefix)
}
