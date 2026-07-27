package userinput

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/google/uuid"
)

const pubsubChannelBase = "weknora:user_input:resolve"
const pendingKeyBase = "weknora:user_input:pending"

var instanceID = uuid.NewString()

type resolveMessage struct {
	TenantID     uint64 `json:"tenant_id"`
	UserID       string `json:"user_id"`
	PendingID    string `json:"pending_id"`
	Answer       Answer `json:"answer"`
	ReplyChannel string `json:"reply_channel,omitempty"`
	OriginID     string `json:"origin_id,omitempty"`
	Nonce        string `json:"nonce,omitempty"`
}

type resolveAck struct {
	Status string `json:"status"`
	Nonce  string `json:"nonce,omitempty"`
}

func pubsubChannel() string {
	if namespace := strings.TrimSpace(os.Getenv("WEKNORA_REDIS_NAMESPACE")); namespace != "" {
		return pubsubChannelBase + ":" + namespace
	}
	return pubsubChannelBase
}

func pendingKey(tenantID uint64, userID, sessionID string) string {
	base := pendingKeyBase
	if namespace := strings.TrimSpace(os.Getenv("WEKNORA_REDIS_NAMESPACE")); namespace != "" {
		base += ":" + namespace
	}
	return fmt.Sprintf("%s:%d:%s:%s", base, tenantID, userID, sessionID)
}

func (g *Gate) storePending(ctx context.Context, snapshot *PendingSnapshot) error {
	if g.rdb == nil {
		return nil
	}
	data, err := json.Marshal(snapshot)
	if err != nil {
		return err
	}
	ttl := time.Until(snapshot.ExpiresAt)
	if ttl <= 0 {
		return ErrPendingNotFound
	}
	if err := g.rdb.Set(ctx, pendingKey(snapshot.TenantID, snapshot.UserID, snapshot.SessionID), data, ttl).Err(); err != nil {
		return fmt.Errorf("persist pending user input: %w", err)
	}
	return nil
}

func (g *Gate) loadStoredPending(
	ctx context.Context,
	tenantID uint64,
	userID, sessionID string,
) (*PendingSnapshot, error) {
	if g.rdb == nil {
		return nil, ErrPendingNotFound
	}
	data, err := g.rdb.Get(ctx, pendingKey(tenantID, userID, sessionID)).Bytes()
	if err != nil {
		return nil, ErrPendingNotFound
	}
	var snapshot PendingSnapshot
	if json.Unmarshal(data, &snapshot) != nil || snapshot.Status != "pending" || time.Now().After(snapshot.ExpiresAt) {
		return nil, ErrPendingNotFound
	}
	return &snapshot, nil
}

func (g *Gate) deleteStoredPending(snapshot *PendingSnapshot) {
	if g.rdb == nil || snapshot == nil {
		return
	}
	key := pendingKey(snapshot.TenantID, snapshot.UserID, snapshot.SessionID)
	script := `if redis.call("GET", KEYS[1]) and string.find(redis.call("GET", KEYS[1]), ARGV[1], 1, true) then return redis.call("DEL", KEYS[1]) end return 0`
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = g.rdb.Eval(ctx, script, []string{key}, snapshot.PendingID).Err()
}

func (g *Gate) runSubscriber() {
	ctx := context.Background()
	backoff := time.Second
	for {
		sub := g.rdb.Subscribe(ctx, pubsubChannel())
		for msg := range sub.Channel() {
			g.handleResolveMessage(ctx, msg.Payload)
		}
		_ = sub.Close()
		time.Sleep(backoff)
		if backoff < 30*time.Second {
			backoff *= 2
		}
	}
}

func (g *Gate) handleResolveMessage(ctx context.Context, payload string) {
	var message resolveMessage
	if err := json.Unmarshal([]byte(payload), &message); err != nil || message.OriginID == instanceID {
		return
	}
	err := g.deliverLocal(message.TenantID, message.UserID, message.PendingID, message.Answer)
	status := resolutionStatus(err)
	if message.ReplyChannel == "" || status == "not_found" {
		return
	}
	data, _ := json.Marshal(resolveAck{Status: status, Nonce: message.Nonce})
	pubCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if err := g.rdb.Publish(pubCtx, message.ReplyChannel, data).Err(); err != nil {
		logger.GetLogger(ctx).Warnf("user input pubsub acknowledgement failed: %v", err)
	}
}

func resolutionStatus(err error) string {
	switch {
	case err == nil:
		return "ok"
	case errors.Is(err, ErrTenantMismatch):
		return "tenant_mismatch"
	case errors.Is(err, ErrUserMismatch):
		return "user_mismatch"
	case errors.Is(err, ErrAlreadyResolved):
		return "already_resolved"
	case errors.Is(err, ErrInvalidAnswer):
		return "invalid_answer"
	default:
		return "not_found"
	}
}

func (g *Gate) resolveCrossInstance(tenantID uint64, userID, pendingID string, answer Answer) error {
	nonce := uuid.NewString()
	replyChannel := pubsubChannel() + ":reply:" + pendingID
	sub := g.rdb.Subscribe(context.Background(), replyChannel)
	defer func() { _ = sub.Close() }()
	subCtx, subCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer subCancel()
	if _, err := sub.Receive(subCtx); err != nil {
		return fmt.Errorf("%w: subscribe for owner response", ErrOwnerUnavailable)
	}

	payload, err := json.Marshal(resolveMessage{
		TenantID: tenantID, UserID: userID, PendingID: pendingID, Answer: answer,
		ReplyChannel: replyChannel, OriginID: instanceID, Nonce: nonce,
	})
	if err != nil {
		return err
	}
	pubCtx, pubCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer pubCancel()
	if err := g.rdb.Publish(pubCtx, pubsubChannel(), payload).Err(); err != nil {
		return fmt.Errorf("%w: publish owner request", ErrOwnerUnavailable)
	}

	ackCtx, ackCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer ackCancel()
	for {
		msg, err := sub.ReceiveMessage(ackCtx)
		if err != nil {
			return ErrPendingNotFound
		}
		var ack resolveAck
		if json.Unmarshal([]byte(msg.Payload), &ack) != nil || ack.Nonce != nonce {
			continue
		}
		switch ack.Status {
		case "ok":
			return nil
		case "tenant_mismatch":
			return ErrTenantMismatch
		case "user_mismatch":
			return ErrUserMismatch
		case "already_resolved":
			return ErrAlreadyResolved
		case "invalid_answer":
			return ErrInvalidAnswer
		default:
			return ErrPendingNotFound
		}
	}
}
