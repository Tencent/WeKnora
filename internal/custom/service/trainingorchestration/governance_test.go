package trainingorchestration

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type governanceLLM struct {
	output string
	err    error
	calls  int32
}

func (f *governanceLLM) Complete(_ context.Context, _ string) (string, error) {
	atomic.AddInt32(&f.calls, 1)
	return f.output, f.err
}

func (*governanceLLM) Model() string         { return "governance-model" }
func (*governanceLLM) PromptVersion() string { return "governance-v1" }

type deadlineLLM struct {
	calls int32
}

func (f *deadlineLLM) Complete(ctx context.Context, _ string) (string, error) {
	atomic.AddInt32(&f.calls, 1)
	<-ctx.Done()
	return "", ctx.Err()
}

func (*deadlineLLM) Model() string         { return "deadline-model" }
func (*deadlineLLM) PromptVersion() string { return "governance-v1" }

type eofCompletionError struct{}

func (eofCompletionError) Error() string          { return "stream ended at EOF" }
func (eofCompletionError) Temporary() bool        { return true }
func (eofCompletionError) ConnectionClosed() bool { return true }

func TestCompletionGateRejectsPromptBeforeModelCall(t *testing.T) {
	llm := &governanceLLM{output: `{}`}
	gate := NewCompletionGate(CompletionGateConfig{MaxTotalCalls: 4})
	_, err := gate.Complete(context.Background(), llm, "planning", "batch-001", "a prompt", 1, nil)
	var capacity *InputCapacityError
	if !errors.As(err, &capacity) || atomic.LoadInt32(&llm.calls) != 0 {
		t.Fatalf("expected pre-call budget rejection, calls=%d err=%v", llm.calls, err)
	}
}

func TestCompletionGateRetriesTransientErrorAtMostOnce(t *testing.T) {
	llm := &governanceLLM{output: `{}`, err: temporaryCompletionError{}}
	logs := make([]CompletionLog, 0, 2)
	gate := NewCompletionGate(CompletionGateConfig{
		MaxTotalCalls: 4,
		Logger: func(entry CompletionLog) {
			logs = append(logs, entry)
		},
	})
	_, err := gate.Complete(context.Background(), llm, "relation_generation", "batch-001", "a prompt", 100, nil)
	if err == nil || atomic.LoadInt32(&llm.calls) != 2 || len(logs) != 2 {
		t.Fatalf("expected one retry, calls=%d logs=%d err=%v", llm.calls, len(logs), err)
	}
	if logs[0].EndReason != "transport_error" || logs[1].EndReason != "transport_error" {
		t.Fatalf("unexpected retry log reasons: %#v", logs)
	}
}

func TestCompletionGateSeparatesRequestTimeout(t *testing.T) {
	llm := &deadlineLLM{}
	gate := NewCompletionGate(CompletionGateConfig{RequestTimeout: 10 * time.Millisecond, MaxTotalCalls: 4})
	started := time.Now()
	_, err := gate.Complete(context.Background(), llm, "cluster_generation", "cluster-1", "a prompt", 100, nil)
	if time.Since(started) > time.Second {
		t.Fatal("request timeout did not stop the model call")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected request deadline, got %v", err)
	}
	if atomic.LoadInt32(&llm.calls) != 1 {
		t.Fatalf("request timeout was retried: calls=%d", llm.calls)
	}
}

func TestCompletionGateDoesNotRetryConnectionEOF(t *testing.T) {
	llm := &governanceLLM{err: eofCompletionError{}}
	gate := NewCompletionGate(CompletionGateConfig{MaxTotalCalls: 4})
	_, err := gate.Complete(context.Background(), llm, "relation_generation", "batch-001", "a prompt", 100, nil)
	if err == nil {
		t.Fatal("expected EOF error")
	}
	if atomic.LoadInt32(&llm.calls) != 1 {
		t.Fatalf("connection EOF was retried: calls=%d", llm.calls)
	}
	var generationErr *GenerationError
	classified := classifyCompletionError("relation generation", err)
	if !errors.As(classified, &generationErr) || generationErr.Code != "model_output_truncated" {
		t.Fatalf("expected EOF to be classified as truncation, got %v", classified)
	}
}

func TestCompletionGateStopsAtMaximumCallCount(t *testing.T) {
	llm := &governanceLLM{output: `{}`}
	gate := NewCompletionGate(CompletionGateConfig{MaxTotalCalls: 1})
	if _, err := gate.Complete(context.Background(), llm, "planning", "batch-001", "a prompt", 100, nil); err != nil {
		t.Fatal(err)
	}
	_, err := gate.Complete(context.Background(), llm, "planning", "batch-002", "a prompt", 100, nil)
	var generationErr *GenerationError
	if !errors.As(err, &generationErr) || generationErr.Code != "call_limit_exceeded" {
		t.Fatalf("expected call limit error, got %v", err)
	}
	if atomic.LoadInt32(&llm.calls) != 1 {
		t.Fatalf("call limit allowed a second model call: calls=%d", llm.calls)
	}
}

func TestInvalidStructuredOutputIsShrinkable(t *testing.T) {
	err := &GenerationError{Code: "model_output_invalid", Err: errors.New("invalid JSON")}
	if !isShrinkableGenerationError(err) {
		t.Fatal("invalid structured output should trigger a smaller planning batch")
	}
}

func TestPlanningContractOutputIsShrinkable(t *testing.T) {
	err := &GenerationError{Code: "planning_output_invalid", Err: errors.New("planning contract rejected")}
	if !isShrinkableGenerationError(err) {
		t.Fatal("planning contract output should trigger a smaller planning batch")
	}
}

func TestCompletionGateNewRunStartsWithFreshCounters(t *testing.T) {
	llm := &governanceLLM{output: `{}`}
	gate := NewCompletionGate(CompletionGateConfig{MaxTotalCalls: 1})
	if _, err := gate.Complete(context.Background(), llm, "planning", "batch-001", "a prompt", 100, nil); err != nil {
		t.Fatal(err)
	}
	runGate := gate.NewRun()
	if _, err := runGate.Complete(context.Background(), llm, "cluster_generation", "cluster-1", "a prompt", 100, nil); err != nil {
		t.Fatalf("fresh run inherited the previous call count: %v", err)
	}
}

func TestClassifyCompletionErrorRecognizesProviderContextLimit(t *testing.T) {
	err := classifyCompletionError("generate", errors.New("provider: maximum context length exceeded"))
	var generationErr *GenerationError
	if !errors.As(err, &generationErr) || generationErr.Code != "model_context_exceeded" {
		t.Fatalf("expected provider context classification, got %v", err)
	}
	if strings.Contains(err.Error(), "prompt body") {
		t.Fatalf("classification should not include prompt content: %v", err)
	}
}
