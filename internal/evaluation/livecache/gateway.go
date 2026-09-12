package livecache

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"sync"
	"time"

	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
	"gorm.io/gorm"
)

// Reservation is committed before outbound I/O. Reservations are never refunded,
// including failures and unknown usage; a crash therefore cannot free budget.
type Reservation struct {
	StepID                string     `json:"step_id" gorm:"primaryKey"`
	Operation             string     `json:"operation"`
	InputTokenUpperBound  int        `json:"input_token_upper_bound"`
	OutputTokenUpperBound int        `json:"output_token_upper_bound"`
	ReservedMicrounits    int64      `json:"reserved_microunits"`
	PreparedAt            time.Time  `json:"prepared_at"`
	CompletedAt           *time.Time `json:"completed_at,omitempty"`
	DurationMs            int64      `json:"duration_ms"`
	HTTPStatus            int        `json:"http_status"`
	State                 string     `json:"state"`
	RequestSHA256         string     `json:"request_sha256"`
	RawUsage              types.JSON `json:"raw_usage,omitempty" gorm:"type:json"`
	PromptTokens          *int       `json:"prompt_tokens,omitempty"`
	CompletionTokens      *int       `json:"completion_tokens,omitempty"`
	CacheReadTokens       *int       `json:"cache_read_tokens,omitempty"`
}

// TableName identifies the experiment's durable budget reservations.
func (Reservation) TableName() string { return "x08_reservations" }

type gateway struct {
	mu             sync.Mutex
	db             *gorm.DB
	client         *http.Client
	key            string
	embeddingKey   string
	nonce          string
	active         *Step
	activeContext  context.Context
	spent          bool
	stopped        string
	reserved       int64
	budget         int64
	chatCount      int
	embeddingCount int
}

func (g *gateway) arm(ctx context.Context, s *Step) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.stopped != "" {
		return errors.New(g.stopped)
	}
	g.active = s
	g.activeContext = ctx
	g.spent = false
	return nil
}

func (g *gateway) failure(code string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.stopped == "" {
		g.stopped = code
	}
	if g.activeContext != nil {
		_ = types.RecordModelAccountingError(g.activeContext, errors.New(code))
	}
}

func (g *gateway) state() (int, int, int64, string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.chatCount, g.embeddingCount, g.reserved, g.stopped
}

func (g *gateway) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	deny := func(code string) {
		g.failure(code)
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"error":{"message":"x08 controlled request stopped"}}`)
	}
	if r.Method != "POST" || r.URL.RawQuery != "" || r.Header.Get("X-X08-Gate") != g.nonce {
		deny("gateway_request_rejected")
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 65537))
	if err != nil || len(body) > 65536 {
		deny("request_body_limit")
		return
	}
	g.mu.Lock()
	if g.active == nil || g.spent || g.stopped != "" {
		g.mu.Unlock()
		deny("extra_attempt_rejected")
		return
	}
	step := *g.active
	if err := validateWire(r.URL.Path, body, step); err != nil {
		g.mu.Unlock()
		deny("request_plan_mismatch: " + err.Error())
		return
	}
	if g.reserved+step.ReservationMicrounits > g.budget ||
		(step.Operation == "chat" && g.chatCount >= MaxChatRequests) ||
		(step.Operation == "embedding" && g.embeddingCount >= MaxEmbeddingRequests) {
		g.mu.Unlock()
		deny("budget_exhausted")
		return
	}
	reservation := Reservation{
		StepID: step.ID, Operation: step.Operation,
		InputTokenUpperBound: step.InputTokenUpperBound, OutputTokenUpperBound: step.OutputTokenUpperBound,
		ReservedMicrounits: step.ReservationMicrounits, PreparedAt: time.Now().UTC(),
		State: "reserved", RequestSHA256: hash(body),
	}
	if err := g.db.Create(&reservation).Error; err != nil {
		g.mu.Unlock()
		deny("reservation_persistence_failed")
		return
	}
	g.spent = true
	g.reserved += step.ReservationMicrounits
	if step.Operation == "chat" {
		g.chatCount++
	} else {
		g.embeddingCount++
	}
	g.mu.Unlock()
	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()
	path := "/embeddings"
	if step.Operation == "chat" {
		path = "/chat/completions"
	}
	req, err := http.NewRequestWithContext(ctx, "POST", Endpoint+path, bytes.NewReader(body))
	if err != nil {
		deny("outbound_request_failed")
		return
	}
	req.Header.Set("Content-Type", "application/json")
	key := g.key
	if step.Operation == "embedding" && g.embeddingKey != "" {
		key = g.embeddingKey
	}
	req.Header.Set("Authorization", "Bearer "+key)
	start := time.Now()
	resp, sendErr := g.client.Do(req)
	var response []byte
	if sendErr == nil {
		reservation.HTTPStatus = resp.StatusCode
		response, err = io.ReadAll(io.LimitReader(resp.Body, 8*1024*1024+1))
		_ = resp.Body.Close()
		if err != nil || len(response) > 8*1024*1024 {
			sendErr = errors.New("response_body_limit")
		}
	}
	reservation.DurationMs = time.Since(start).Milliseconds()
	ended := time.Now().UTC()
	reservation.CompletedAt = &ended
	code := ""
	if sendErr != nil {
		code = "provider_transport_failed"
	} else if reservation.HTTPStatus != http.StatusOK {
		code = "provider_http_failed"
	} else {
		code = parseEvidence(response, step, &reservation)
	}
	reservation.State = "complete"
	if code != "" {
		reservation.State = code
	}
	if err := g.db.Save(&reservation).Error; err != nil {
		code = "response_evidence_persistence_failed"
	}
	if code != "" {
		// Preserve a successful provider response for the production usage parser
		// while latching the experiment stop. Partial usage remains in its ledger.
		if sendErr == nil && reservation.HTTPStatus == http.StatusOK && json.Valid(response) {
			g.failure(code)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(response)
			return
		}
		deny(code)
		return
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(response)
}

func validateWire(path string, b []byte, s Step) error {
	if len(b)+1024 > s.InputTokenUpperBound {
		return errors.New("wire bytes exceed reserved token envelope")
	}
	var raw map[string]json.RawMessage
	if json.Unmarshal(b, &raw) != nil {
		return errors.New("invalid JSON")
	}
	var model string
	_ = json.Unmarshal(raw["model"], &model)
	allowed := map[string]bool{"model": true}
	if s.Operation == "embedding" {
		if path != "/compatible-mode/v1/embeddings" || model != EmbeddingModel {
			return errors.New("embedding identity")
		}
		for _, key := range []string{"input", "encoding_format", "dimensions", "truncate_prompt_tokens"} {
			allowed[key] = true
		}
		var input []string
		var dimensions, truncate int
		var format string
		_ = json.Unmarshal(raw["input"], &input)
		_ = json.Unmarshal(raw["dimensions"], &dimensions)
		_ = json.Unmarshal(raw["encoding_format"], &format)
		_ = json.Unmarshal(raw["truncate_prompt_tokens"], &truncate)
		if !reflect.DeepEqual(input, s.Input) || dimensions != 1024 || format != "float" || truncate != 511 {
			return errors.New("embedding payload")
		}
	} else {
		if path != "/compatible-mode/v1/chat/completions" || model != ChatModel {
			return errors.New("chat identity")
		}
		for _, key := range []string{
			"messages", "temperature", "max_completion_tokens", "enable_thinking", "stream",
		} {
			allowed[key] = true
		}
		var messages []chat.Message
		var temperature float64
		var maxTokens int
		if json.Unmarshal(raw["messages"], &messages) != nil {
			return errors.New("messages")
		}
		if !bytes.Equal(bytes.TrimSpace(raw["enable_thinking"]), []byte("false")) {
			return errors.New("thinking must be explicit false")
		}
		if stream, exists := raw["stream"]; exists && !bytes.Equal(bytes.TrimSpace(stream), []byte("false")) {
			return errors.New("stream must be false")
		}
		_ = json.Unmarshal(raw["temperature"], &temperature)
		_ = json.Unmarshal(raw["max_completion_tokens"], &maxTokens)
		if !reflect.DeepEqual(messages, s.Messages) || temperature != 0.3 ||
			maxTokens != MaxOutputTokens {
			return errors.New("chat payload or limits")
		}
		// Reject hidden image/tool fields that the neutral message decoder ignores.
		var wireMessages []map[string]json.RawMessage
		_ = json.Unmarshal(raw["messages"], &wireMessages)
		for _, m := range wireMessages {
			for k := range m {
				if k != "role" && k != "content" {
					return errors.New("non-text message field")
				}
			}
		}
	}
	for k := range raw {
		if !allowed[k] {
			return fmt.Errorf("unexpected request field %s", k)
		}
	}
	return nil
}

// Only a numeric allowlist is retained from usage. No headers, response text,
// arbitrary error strings or URL queries enter the redacted provider evidence.
func parseEvidence(b []byte, s Step, r *Reservation) string {
	var payload struct {
		Model string          `json:"model"`
		Usage json.RawMessage `json:"usage"`
	}
	if json.Unmarshal(b, &payload) != nil {
		return "invalid_provider_json"
	}
	if payload.Model != "" &&
		payload.Model != map[string]string{"chat": ChatModel, "embedding": EmbeddingModel}[s.Operation] {
		return "provider_model_drift"
	}
	var usage struct {
		Prompt     *int `json:"prompt_tokens"`
		Completion *int `json:"completion_tokens"`
		Total      *int `json:"total_tokens"`
		Details    *struct {
			Cached *int `json:"cached_tokens"`
		} `json:"prompt_tokens_details"`
	}
	if json.Unmarshal(payload.Usage, &usage) != nil || usage.Total == nil {
		return "usage_unreported"
	}
	clean := map[string]any{}
	if usage.Prompt != nil {
		clean["prompt_tokens"] = *usage.Prompt
	}
	if usage.Completion != nil {
		clean["completion_tokens"] = *usage.Completion
	}
	clean["total_tokens"] = *usage.Total
	if usage.Details != nil && usage.Details.Cached != nil {
		clean["prompt_tokens_details"] = map[string]int{"cached_tokens": *usage.Details.Cached}
	}
	encoded, _ := json.Marshal(clean)
	r.RawUsage = types.JSON(encoded)
	if s.Operation == "embedding" && usage.Prompt == nil {
		v := *usage.Total
		usage.Prompt = &v
	}
	if usage.Prompt == nil {
		return "input_usage_unreported"
	}
	r.PromptTokens = usage.Prompt
	if s.Operation == "chat" {
		if usage.Completion == nil {
			return "output_usage_unreported"
		}
		r.CompletionTokens = usage.Completion
		if usage.Details == nil || usage.Details.Cached == nil {
			return "cache_usage_unreported"
		}
		r.CacheReadTokens = usage.Details.Cached
		if *usage.Details.Cached < 0 || *usage.Details.Cached > *usage.Prompt {
			return "invalid_cache_usage"
		}
	} else {
		zero := 0
		r.CompletionTokens = &zero
		if usage.Completion != nil && *usage.Completion != 0 {
			return "unexpected_embedding_output_charge"
		}
	}
	if *r.PromptTokens < 0 || *r.CompletionTokens < 0 || *usage.Total != *r.PromptTokens+*r.CompletionTokens {
		return "invalid_usage_counters"
	}
	if *r.PromptTokens > s.InputTokenUpperBound || *r.CompletionTokens > s.OutputTokenUpperBound {
		return "reported_usage_exceeds_reservation"
	}
	return ""
}
