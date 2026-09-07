package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	secutils "github.com/Tencent/WeKnora/internal/utils"
	"github.com/hibiken/asynq"
	"github.com/redis/go-redis/v9"
)

var ErrWebhookInFlightBusy = errors.New("webhook in-flight slots full")

const webhookPOSTTimeout = 10 * time.Second

type webhookDeliverer struct {
	outbox         interfaces.WebhookOutboxRepository
	endpoints      interfaces.WebhookEndpointRepository
	deliveries     interfaces.WebhookDeliveryRepository
	knowledge      interfaces.KnowledgeRepository
	redis          *redis.Client
	httpClient     *http.Client
	loopbackClient *http.Client
}

func NewWebhookDeliverer(
	outbox interfaces.WebhookOutboxRepository,
	endpoints interfaces.WebhookEndpointRepository,
	deliveries interfaces.WebhookDeliveryRepository,
	knowledge interfaces.KnowledgeRepository,
	redisClient *redis.Client,
) interfaces.WebhookDeliverer {
	return &webhookDeliverer{
		outbox:     outbox,
		endpoints:  endpoints,
		deliveries: deliveries,
		knowledge:  knowledge,
		redis:      redisClient,
		httpClient: secutils.NewSSRFSafeHTTPClient(secutils.SSRFSafeHTTPClientConfig{
			Timeout:      webhookPOSTTimeout,
			MaxRedirects: 5,
		}),
	}
}

func (w *webhookDeliverer) Deliver(ctx context.Context, payload types.WebhookDeliverPayload) error {
	if payload.EventID == "" || payload.EndpointID == "" || payload.DeliveryID == "" {
		return asynq.SkipRetry
	}
	row, err := w.outbox.GetByEventID(ctx, payload.EventID)
	if err != nil {
		return err
	}
	ep, err := w.endpoints.GetByIDUnscoped(ctx, payload.EndpointID)
	if err != nil {
		_ = w.deliveries.UpdateAttempt(ctx, payload.DeliveryID, 0, 0, 0, err.Error(), types.WebhookDeliveryFailed, true)
		return fmt.Errorf("endpoint gone: %w", asynq.SkipRetry)
	}
	if err := w.acquireSlot(ctx, payload.TenantID); err != nil {
		return err
	}
	defer w.releaseSlot(ctx, payload.TenantID)

	body, err := w.bodyWithTicket(ctx, row.Payload)
	if err != nil {
		return err
	}
	secret, err := decryptWebhookSecret(ep.SecretEnc)
	if err != nil {
		_ = w.deliveries.UpdateAttempt(ctx, payload.DeliveryID, 0, 0, 0, err.Error(), types.WebhookDeliveryFailed, true)
		return fmt.Errorf("secret: %w", asynq.SkipRetry)
	}
	if _, err := validateWebhookTargetURL(ep.URL); err != nil {
		_ = w.deliveries.UpdateAttempt(ctx, payload.DeliveryID, 0, 0, 0, err.Error(), types.WebhookDeliveryFailed, true)
		return fmt.Errorf("url: %w", asynq.SkipRetry)
	}

	ts := time.Now().Unix()
	sig := secutils.SignWebhookTimestampBody(secret, ts, body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, ep.URL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", types.WebhookUserAgent)
	req.Header.Set("X-WeKnora-Event", row.EventType)
	req.Header.Set("X-WeKnora-Delivery", payload.DeliveryID)
	req.Header.Set("X-WeKnora-Timestamp", strconv.FormatInt(ts, 10))
	req.Header.Set("X-WeKnora-Signature", sig)

	start := time.Now()
	resp, err := w.clientForURL(ep.URL).Do(req)
	dur := int(time.Since(start).Milliseconds())
	if err != nil {
		status, finished := retryDeliveryOutcome(ctx)
		_ = w.deliveries.UpdateAttempt(ctx, payload.DeliveryID, 0, incrementAttempts(ctx), dur, err.Error(), status, finished)
		if finished {
			return fmt.Errorf("%v: %w", err, asynq.SkipRetry)
		}
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))

	attempts := incrementAttempts(ctx)
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		_ = w.deliveries.UpdateAttempt(ctx, payload.DeliveryID, resp.StatusCode, attempts, dur, "", types.WebhookDeliverySuccess, true)
		return nil
	}
	msg := fmt.Sprintf("http %d", resp.StatusCode)
	retryable := resp.StatusCode == http.StatusRequestTimeout ||
		resp.StatusCode == http.StatusTooManyRequests ||
		resp.StatusCode >= 500
	if !retryable {
		_ = w.deliveries.UpdateAttempt(ctx, payload.DeliveryID, resp.StatusCode, attempts, dur, msg, types.WebhookDeliveryFailed, true)
		return fmt.Errorf("%s: %w", msg, asynq.SkipRetry)
	}
	status, finished := retryDeliveryOutcome(ctx)
	_ = w.deliveries.UpdateAttempt(ctx, payload.DeliveryID, resp.StatusCode, attempts, dur, msg, status, finished)
	if finished {
		return fmt.Errorf("%s: %w", msg, asynq.SkipRetry)
	}
	return fmt.Errorf("%s", msg)
}

func (w *webhookDeliverer) clientForURL(raw string) *http.Client {
	if parsed, err := url.Parse(raw); err == nil && isLoopbackHost(parsed.Hostname()) {
		if w.loopbackClient == nil {
			w.loopbackClient = &http.Client{Timeout: webhookPOSTTimeout}
		}
		return w.loopbackClient
	}
	if w.httpClient != nil {
		return w.httpClient
	}
	return http.DefaultClient
}

func incrementAttempts(ctx context.Context) int {
	retried, _, ok := types.TaskRetryMetadataFromContext(ctx)
	if ok {
		return retried + 1
	}
	n, ok := asynq.GetRetryCount(ctx)
	if ok {
		return n + 1
	}
	return 1
}

func retryDeliveryOutcome(ctx context.Context) (status string, finished bool) {
	if isWebhookFinalAttempt(ctx) {
		return types.WebhookDeliveryFailed, true
	}
	return types.WebhookDeliveryPending, false
}

func isWebhookFinalAttempt(ctx context.Context) bool {
	if retried, maxRetry, ok := types.TaskRetryMetadataFromContext(ctx); ok {
		return retried >= maxRetry
	}
	retried, retriedOK := asynq.GetRetryCount(ctx)
	maxRetry, maxOK := asynq.GetMaxRetry(ctx)
	if retriedOK && maxOK {
		return retried >= maxRetry
	}
	return false
}

func (w *webhookDeliverer) bodyWithTicket(ctx context.Context, raw json.RawMessage) ([]byte, error) {
	var env types.WorkspaceWebhookEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, err
	}
	var data types.WebhookKnowledgeData
	if err := json.Unmarshal(env.Data, &data); err != nil || data.Resource != "knowledge" {
		return raw, nil
	}
	if !data.Download.Available || data.KnowledgeID == "" {
		return raw, nil
	}
	tenantID := env.TenantID
	if w.knowledge != nil {
		if k, err := w.knowledge.GetKnowledgeByIDOnly(ctx, data.KnowledgeID); err == nil && k != nil {
			tenantID = k.TenantID
		}
	}
	ticket, exp, err := secutils.SignKnowledgeDownloadTicket(data.KnowledgeID, tenantID, time.Now())
	if err != nil {
		data.Download.Available = false
		data.Download.Reason = "signing_unavailable"
		data.Download.Ticket = ""
		data.Download.Path = ""
	} else {
		data.Download.Ticket = ticket
		data.Download.TicketExpiresAt = exp.UTC().Format(time.RFC3339)
	}
	patched, err := json.Marshal(data)
	if err != nil {
		return nil, err
	}
	env.Data = patched
	return json.Marshal(env)
}

func (w *webhookDeliverer) acquireSlot(ctx context.Context, tenantID uint64) error {
	if w.redis == nil {
		return nil
	}
	key := fmt.Sprintf("weknora:webhook:inflight:%d", tenantID)
	n, err := w.redis.Incr(ctx, key).Result()
	if err != nil {
		logger.Warnf(ctx, "webhook in-flight incr: %v", err)
		return nil
	}
	_ = w.redis.Expire(ctx, key, 2*time.Minute)
	if n > int64(types.WebhookInFlightLimit) {
		_ = w.redis.Decr(ctx, key)
		return ErrWebhookInFlightBusy
	}
	return nil
}

func (w *webhookDeliverer) releaseSlot(ctx context.Context, tenantID uint64) {
	if w.redis == nil {
		return
	}
	key := fmt.Sprintf("weknora:webhook:inflight:%d", tenantID)
	if n, err := w.redis.Decr(ctx, key).Result(); err == nil && n < 0 {
		w.redis.Del(ctx, key)
	}
}

func decryptWebhookSecret(enc string) (string, error) {
	if enc == "" {
		return "", errors.New("webhook secret missing")
	}
	key := secutils.GetAESKey()
	plain, err := secutils.DecryptAESGCM(enc, key)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(plain) == "" {
		return "", errors.New("webhook secret missing")
	}
	return plain, nil
}
