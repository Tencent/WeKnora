package memory

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/models/embedding"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/hibiken/asynq"
)

// stubTenantRepo serves workspace memory configuration. It embeds the
// interface so the tests only have to implement what the memory service
// actually calls; anything else panics loudly rather than silently returning
// a zero value.
type stubTenantRepo struct {
	interfaces.TenantRepository

	mu      sync.RWMutex
	configs map[uint64]*types.MemoryConfig
}

func (s *stubTenantRepo) set(tenantID uint64, cfg *types.MemoryConfig) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.configs[tenantID] = cfg
}

func (s *stubTenantRepo) GetTenantByID(_ context.Context, id uint64) (*types.Tenant, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return &types.Tenant{ID: id, MemoryConfig: s.configs[id]}, nil
}

// stubMessageRepo serves per-session transcripts. It implements the same
// watermark semantics as the real repository so tests exercise the paging that
// makes coverage guaranteed rather than asserting against a simplification.
type stubMessageRepo struct {
	interfaces.MessageRepository

	mu sync.Mutex
	// messages is the single-session shortcut used by most tests.
	messages []*types.Message
	// bySession is used by tests that span several conversations.
	bySession map[string][]*types.Message
}

func (s *stubMessageRepo) set(sessionID string, messages []*types.Message) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.bySession == nil {
		s.bySession = map[string][]*types.Message{}
	}
	s.bySession[sessionID] = messages
}

func (s *stubMessageRepo) GetMessagesBySessionBeforeTime(
	_ context.Context, sessionID string, beforeTime time.Time, limit int,
) ([]*types.Message, error) {
	s.mu.Lock()
	source := s.bySession[sessionID]
	if source == nil {
		source = s.messages
	}
	snapshot := append([]*types.Message(nil), source...)
	s.mu.Unlock()

	sort.SliceStable(snapshot, func(i, j int) bool {
		return snapshot[i].CreatedAt.Before(snapshot[j].CreatedAt)
	})
	var out []*types.Message
	for _, message := range snapshot {
		if !message.CreatedAt.Before(beforeTime) {
			break
		}
		out = append(out, message)
	}
	if limit > 0 && len(out) > limit {
		out = out[len(out)-limit:]
	}
	return out, nil
}

func (s *stubMessageRepo) ListMessagesBySessionAfterTime(
	_ context.Context, sessionID string, afterTime time.Time, limit int,
) ([]*types.Message, error) {
	s.mu.Lock()
	source := s.bySession[sessionID]
	if source == nil {
		source = s.messages
	}
	snapshot := append([]*types.Message(nil), source...)
	s.mu.Unlock()

	sort.SliceStable(snapshot, func(i, j int) bool {
		return snapshot[i].CreatedAt.Before(snapshot[j].CreatedAt)
	})
	var out []*types.Message
	for _, message := range snapshot {
		if !afterTime.IsZero() && !message.CreatedAt.After(afterTime) {
			continue
		}
		out = append(out, message)
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out, nil
}

// stubModelService hands out a chat model that replays a canned response and
// records what it was asked.
type stubModelService struct {
	interfaces.ModelService

	mu       sync.Mutex
	response string
	// responseFor lets one stub answer two different prompts. Distillation and
	// topic adjudication both go through this model, and a test that pins one
	// must not accidentally pin the other.
	responseFor map[string]string
	// finishReason is reported on every reply, so a test can simulate a model
	// that ran out of completion budget.
	finishReason string
	// truncateUntilCall makes the model return nothing until this many calls
	// have been made, simulating a reasoning model that only answers when it
	// is given room for its thinking.
	truncateUntilCall int
	// lastBudget is the completion ceiling the last call asked for.
	lastBudget int
	// lastThinking is the thinking flag the last call passed.
	lastThinking *bool
	// workspaceModels backs ListModels.
	workspaceModels []*types.Model
	// embedder backs GetEmbeddingModel.
	embedder         *stubEmbedder
	requestedModelID string
	requestedEmbedID string
	lastPrompt       string
	// prompts records every transcript the model was asked about, so a test
	// can assert that no message went unread across several runs.
	prompts []string
	calls   int
	// failNext makes the next call fail, standing in for a provider outage.
	failNext bool
	// lastFormat records the response schema the caller asked for.
	lastFormat json.RawMessage
}

// workspaceModels is what ListModels returns, so a test can reproduce a
// workspace that has a usable model and one that has none.
func (s *stubModelService) ListModels(context.Context) ([]*types.Model, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.workspaceModels, nil
}

func (s *stubModelService) GetEmbeddingModel(
	_ context.Context, modelID string,
) (embedding.Embedder, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.requestedEmbedID = modelID
	if s.embedder == nil {
		return nil, errors.New("no embedding model configured")
	}
	return s.embedder, nil
}

func (s *stubModelService) GetChatModel(_ context.Context, modelID string) (chat.Chat, error) {
	s.mu.Lock()
	s.requestedModelID = modelID
	s.mu.Unlock()
	return &stubChatModel{owner: s}, nil
}

// callCount is how many times the model has been asked anything.
func (s *stubModelService) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

// lastBudgetAsked is the completion ceiling of the most recent call.
func (s *stubModelService) lastBudgetAsked() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastBudget
}

// lastThinkingAsked is the thinking flag of the most recent call.
func (s *stubModelService) lastThinkingAsked() *bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastThinking
}

// lastPromptContaining returns the most recent prompt carrying a marker, so a
// test can pin the extraction call specifically. One run can also make topic
// adjudication and consolidation calls, and whichever ran last would otherwise
// be what an assertion measured.
func (s *stubModelService) lastPromptContaining(marker string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := len(s.prompts) - 1; i >= 0; i-- {
		if strings.Contains(s.prompts[i], marker) {
			return s.prompts[i]
		}
	}
	return ""
}

// seenTranscripts concatenates every prompt the model received.
func (s *stubModelService) seenTranscripts() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return strings.Join(s.prompts, "\n---\n")
}

type stubChatModel struct {
	owner *stubModelService
}

func (m *stubChatModel) Chat(
	_ context.Context, messages []chat.Message, opts *chat.ChatOptions,
) (*types.ChatResponse, error) {
	var prompt strings.Builder
	for _, message := range messages {
		prompt.WriteString(message.Content)
		prompt.WriteString("\n")
	}
	m.owner.mu.Lock()
	defer m.owner.mu.Unlock()
	m.owner.calls++
	m.owner.lastPrompt = prompt.String()
	if opts != nil {
		m.owner.lastFormat = opts.Format
		m.owner.lastBudget = opts.MaxCompletionTokens
		m.owner.lastThinking = opts.Thinking
	}
	m.owner.prompts = append(m.owner.prompts, prompt.String())
	if m.owner.failNext {
		m.owner.failNext = false
		return nil, errors.New("stub model outage")
	}
	if m.owner.calls <= m.owner.truncateUntilCall {
		return &types.ChatResponse{Content: "", FinishReason: "length"}, nil
	}

	body := m.owner.response
	for marker, canned := range m.owner.responseFor {
		if strings.Contains(prompt.String(), marker) {
			body = canned
			break
		}
	}
	return &types.ChatResponse{Content: body, FinishReason: m.owner.finishReason}, nil
}

func (m *stubChatModel) ChatStream(
	_ context.Context, _ []chat.Message, _ *chat.ChatOptions,
) (<-chan types.StreamResponse, error) {
	return nil, errors.New("not used")
}

func (m *stubChatModel) GetModelName() string { return "stub" }
func (m *stubChatModel) GetModelID() string   { return "stub" }

// stubEnqueueOptions captures the scheduling decisions a test cares about.
type stubEnqueueOptions struct {
	queue     string
	processIn time.Duration
}

// stubEnqueuer records enqueued tasks instead of touching Redis, and lets a
// test drain them in order the way a worker would.
type stubEnqueuer struct {
	mu      sync.Mutex
	tasks   []*asynq.Task
	options []stubEnqueueOptions
}

func (s *stubEnqueuer) Enqueue(task *asynq.Task, opts ...asynq.Option) (*asynq.TaskInfo, error) {
	recorded := stubEnqueueOptions{}
	for _, opt := range opts {
		switch opt.Type() {
		case asynq.QueueOpt:
			if queue, ok := opt.Value().(string); ok {
				recorded.queue = queue
			}
		case asynq.ProcessInOpt:
			if delay, ok := opt.Value().(time.Duration); ok {
				recorded.processIn = delay
			}
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tasks = append(s.tasks, task)
	s.options = append(s.options, recorded)
	return &asynq.TaskInfo{ID: "stub", Type: task.Type()}, nil
}

// pop returns the oldest queued task, or nil when the queue is empty.
func (s *stubEnqueuer) pop() *asynq.Task {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.tasks) == 0 {
		return nil
	}
	task := s.tasks[0]
	s.tasks = s.tasks[1:]
	return task
}

// stubEmbedder returns a deterministic vector per phrase, so a test can state
// which statements are semantically close without needing a real model.
type stubEmbedder struct {
	vectors map[string][]float32
	fail    bool
	delay   time.Duration
	calls   int
	texts   []string
}

func (e *stubEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	e.calls++
	e.texts = append(e.texts, text)
	if e.delay > 0 {
		select {
		case <-time.After(e.delay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if e.fail {
		return nil, errors.New("stub embedder outage")
	}
	for phrase, vector := range e.vectors {
		if strings.Contains(text, phrase) {
			return vector, nil
		}
	}
	// Anything unrecognised is orthogonal to everything named.
	return []float32{0, 0, 1}, nil
}

func (e *stubEmbedder) BatchEmbed(ctx context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, 0, len(texts))
	for _, text := range texts {
		vector, err := e.Embed(ctx, text)
		if err != nil {
			return nil, err
		}
		out = append(out, vector)
	}
	return out, nil
}

func (e *stubEmbedder) BatchEmbedWithPool(
	ctx context.Context, _ embedding.Embedder, texts []string,
) ([][]float32, error) {
	return e.BatchEmbed(ctx, texts)
}

func (e *stubEmbedder) GetModelName() string { return "stub-embedder" }
func (e *stubEmbedder) GetDimensions() int   { return 3 }
func (e *stubEmbedder) GetModelID() string   { return "embed-1" }
