package im

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
	"time"

	"github.com/Tencent/WeKnora/internal/event"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

type exitSessionService struct {
	interfaces.SessionService
	run func(context.Context, *event.EventBus) error
}

func (s *exitSessionService) AgentQA(ctx context.Context, _ *types.QARequest, bus *event.EventBus) error {
	return s.run(ctx, bus)
}

func (s *exitSessionService) KnowledgeQA(ctx context.Context, _ *types.QARequest, bus *event.EventBus) error {
	return s.run(ctx, bus)
}

type qaExitResult struct {
	answer string
	err    error
}

// Exercise both real consumers; for runQA, render errors as fallbackNonStream does.
func startQAExitHandler(
	ctx context.Context, stream, agent bool, run func(context.Context, *event.EventBus) error,
) <-chan qaExitResult {
	service := &Service{
		sessionService: &exitSessionService{run: run}, messageService: &fullOutputMessageService{},
		streamManager: &fullOutputStreamManager{},
	}
	var customAgent *types.CustomAgent
	if agent {
		customAgent = &types.CustomAgent{Config: types.CustomAgentConfig{AgentMode: types.AgentModeSmartReasoning}}
	}
	result := make(chan qaExitResult, 1)
	go func() {
		session := &types.Session{ID: "session"}
		if stream {
			sender := &recordingStreamSender{}
			err := service.handleMessageStream(ctx,
				&IncomingMessage{Platform: PlatformFeishu, UserID: "user", Content: "question"},
				session, customAgent, nil, nil, nil, sender, nil, "user", nil)
			_, final, ended := sender.snapshot()
			if err == nil && !ended {
				err = errors.New("handler returned without ending stream")
			}
			result <- qaExitResult{answer: final, err: err}
			return
		}
		answer, err := service.runQA(ctx, session, "question", customAgent, nil, nil, nil, "user", nil)
		if err != nil {
			answer = imQAFailureReply(err)
		}
		result <- qaExitResult{answer: answer, err: err}
	}()
	return result
}

func checkQAExitResult(t *testing.T, result <-chan qaExitResult, stream, failed bool, want string) {
	t.Helper()
	// Wait for all runnable goroutines before inspecting the result. This exposes
	// premature completion without scheduler-dependent sleeps or real-time delays.
	synctest.Wait()
	select {
	case got := <-result:
		wantErr := failed && !stream
		if (got.err != nil) != wantErr || got.answer != want {
			t.Fatalf("answer=%q err=%v; want answer=%q error=%v", got.answer, got.err, want, wantErr)
		}
	default:
		t.Fatal("QA consumer did not return")
	}
}

func TestIMQAAgentReturnClosesReply(t *testing.T) {
	for _, stream := range []bool{true, false} {
		mode := "runQA"
		if stream {
			mode = "stream"
		}
		for _, tc := range []struct {
			name   string
			events []event.Event
			err    error
			want   string
			failed bool
		}{
			{name: "no events", want: imNoAnswerFallback},
			{name: "complete only", want: "completed answer", events: []event.Event{
				{Type: event.EventAgentComplete, Data: event.AgentCompleteData{FinalAnswer: "completed answer"}},
			}},
			{name: "partial answer", want: "partial answer", events: []event.Event{
				{Type: event.EventAgentFinalAnswer, Data: event.AgentFinalAnswerData{Content: "partial answer"}},
			}},
			{name: "terminal answer without complete", want: "final answer", events: []event.Event{
				{
					Type: event.EventAgentFinalAnswer,
					Data: event.AgentFinalAnswerData{Content: "final answer", Done: true},
				},
			}},
			{name: "normal completion", want: "final answer", events: []event.Event{
				{
					Type: event.EventAgentFinalAnswer,
					Data: event.AgentFinalAnswerData{Content: "final answer", Done: true},
				},
				{Type: event.EventAgentComplete, Data: event.AgentCompleteData{FinalAnswer: "final answer"}},
			}},
			{
				name: "initialization error", err: errors.New("model unavailable"), failed: true,
				want: imQAFailureReply(errors.New("QA execution error: model unavailable")),
			},
			{
				name: "error event", failed: true,
				want: imQAFailureReply(errors.New("QA pipeline error: upstream failed")),
				events: []event.Event{
					{Type: event.EventError, Data: event.ErrorData{Error: "upstream failed"}},
				},
			},
		} {
			t.Run(mode+"/"+tc.name, func(t *testing.T) {
				synctest.Test(t, func(t *testing.T) {
					ctx, cancel := context.WithCancel(context.Background())
					defer cancel()
					started := time.Now()
					result := startQAExitHandler(ctx, stream, true, func(
						ctx context.Context, bus *event.EventBus,
					) error {
						for _, evt := range tc.events {
							if err := bus.Emit(ctx, evt); err != nil {
								return err
							}
						}
						return tc.err
					})
					checkQAExitResult(t, result, stream, tc.failed, tc.want)
					if time.Since(started) >= time.Second {
						t.Fatal("reply relied on the completion wait timeout")
					}
				})
			})
		}
	}
}

func TestIMQACompleteBeforeError(t *testing.T) {
	for _, stream := range []bool{true, false} {
		mode := "runQA"
		if stream {
			mode = "stream"
		}
		t.Run(mode, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				releaseError := make(chan struct{})
				result := startQAExitHandler(ctx, stream, true, func(ctx context.Context, bus *event.EventBus) error {
					if err := bus.Emit(ctx, event.Event{
						Type: event.EventAgentComplete, Data: event.AgentCompleteData{},
					}); err != nil {
						return err
					}
					// executeLoop emits Complete in its defer; Execute then emits Error,
					// and AgentQA returns nil. Hold that boundary to expose early finalization.
					select {
					case <-releaseError:
					case <-ctx.Done():
					}
					_ = bus.Emit(ctx, event.Event{
						Type: event.EventError, Data: event.ErrorData{Error: "upstream failed"},
					})
					return nil
				})
				synctest.Wait()
				select {
				case got := <-result:
					t.Fatalf("Complete ended the reply before Error: %+v", got)
				default:
				}
				close(releaseError)
				want := imQAFailureReply(errors.New("QA pipeline error: upstream failed"))
				checkQAExitResult(t, result, stream, true, want)
			})
		})
	}
}

func TestIMQAWaitsForPendingEvents(t *testing.T) {
	for _, stream := range []bool{true, false} {
		mode := "runQA"
		if stream {
			mode = "stream"
		}
		for _, agent := range []bool{true, false} {
			name := "knowledge async answer"
			if agent {
				name = "agent completion after terminal answer"
			}
			t.Run(mode+"/"+name, func(t *testing.T) {
				synctest.Test(t, func(t *testing.T) {
					ctx, cancel := context.WithCancel(context.Background())
					defer cancel()
					release := make(chan struct{})
					result := startQAExitHandler(ctx, stream, agent, func(
						ctx context.Context, bus *event.EventBus,
					) error {
						answer := event.Event{
							Type: event.EventAgentFinalAnswer,
							Data: event.AgentFinalAnswerData{Content: "answer", Done: true},
						}
						if agent {
							if err := bus.Emit(ctx, answer); err != nil {
								return err
							}
							select {
							case <-release:
							case <-ctx.Done():
								return ctx.Err()
							}
							return bus.Emit(ctx, event.Event{
								Type: event.EventAgentComplete, Data: event.AgentCompleteData{FinalAnswer: "answer"},
							})
						}
						go func() {
							select {
							case <-release:
								_ = bus.Emit(ctx, answer)
							case <-ctx.Done():
							}
						}()
						return nil
					})
					synctest.Wait()
					select {
					case got := <-result:
						t.Fatalf("reply ended before pending event: %+v", got)
					default:
					}
					close(release)
					checkQAExitResult(t, result, stream, false, "answer")
				})
			})
		}
	}
}

func TestIMQACancellationStopsProducer(t *testing.T) {
	for _, stream := range []bool{true, false} {
		mode := "runQA"
		if stream {
			mode = "stream"
		}
		t.Run(mode, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				stopped := make(chan struct{})
				result := startQAExitHandler(ctx, stream, true, func(ctx context.Context, _ *event.EventBus) error {
					<-ctx.Done()
					close(stopped)
					return ctx.Err()
				})
				synctest.Wait()
				cancel()
				synctest.Wait()
				select {
				case got := <-result:
					if stream && got.err != nil {
						t.Fatal(got.err)
					}
					if !stream && !errors.Is(got.err, context.Canceled) {
						t.Fatalf("expected cancellation, got %v", got.err)
					}
				default:
					t.Fatal("cancellation did not end the reply")
				}
				select {
				case <-stopped:
				default:
					t.Fatal("QA producer did not stop")
				}
			})
		})
	}
}
