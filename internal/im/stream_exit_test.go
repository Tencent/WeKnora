package im

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/event"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

type streamExitSessionService struct {
	interfaces.SessionService
	run func(context.Context, *event.EventBus) error
}

func runStreamExitHandler(
	ctx context.Context, service *Service, sender StreamSender, agent *types.CustomAgent,
) error {
	return service.handleMessageStream(
		ctx, &IncomingMessage{Platform: PlatformFeishu, UserID: "user", Content: "question"},
		&types.Session{ID: "session"}, agent, nil, nil, nil, sender, nil, "user", nil,
	)
}

func streamExitAgent() *types.CustomAgent {
	return &types.CustomAgent{Config: types.CustomAgentConfig{AgentMode: types.AgentModeSmartReasoning}}
}

func TestHandleMessageStreamCompletionStopsProducer(t *testing.T) {
	producerStopped := make(chan struct{})
	sessionSvc := &streamExitSessionService{run: func(ctx context.Context, bus *event.EventBus) error {
		defer close(producerStopped)
		if err := bus.Emit(ctx, event.Event{
			Type: event.EventAgentComplete, Data: event.AgentCompleteData{FinalAnswer: "complete-only answer"},
		}); err != nil {
			return err
		}
		// Completion must end the stream even if the caller is still unwinding.
		<-ctx.Done()
		return nil
	}}
	service := &Service{
		sessionService: sessionSvc, messageService: &fullOutputMessageService{},
		streamManager: &fullOutputStreamManager{},
	}
	sender := &recordingStreamSender{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	returned := make(chan error, 1)
	go func() {
		returned <- runStreamExitHandler(ctx, service, sender, streamExitAgent())
	}()
	select {
	case err := <-returned:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		cancel()
		<-returned
		t.Fatal("completion event did not end the stream")
	}
	select {
	case <-producerStopped:
	case <-time.After(time.Second):
		t.Fatal("stream return did not cancel the QA context")
	}
	_, final, ended := sender.snapshot()
	if !ended || final != "complete-only answer" {
		t.Fatalf("final=%q, ended=%v", final, ended)
	}
}

func TestHandleMessageStreamPreservesTerminalPaths(t *testing.T) {
	for _, tc := range []struct {
		name string
		run  func(context.Context, *event.EventBus) error
		want string
	}{
		{name: "partial answer then return", want: "partial answer", run: func(
			ctx context.Context, bus *event.EventBus,
		) error {
			return bus.Emit(ctx, event.Event{
				Type: event.EventAgentFinalAnswer, Data: event.AgentFinalAnswerData{Content: "partial answer"},
			})
		}},
		{name: "normal answer and completion", want: "normal answer", run: func(
			ctx context.Context, bus *event.EventBus,
		) error {
			if err := bus.Emit(ctx, event.Event{
				Type: event.EventAgentFinalAnswer,
				Data: event.AgentFinalAnswerData{Content: "normal answer", Done: true},
			}); err != nil {
				return err
			}
			return bus.Emit(ctx, event.Event{
				Type: event.EventAgentComplete, Data: event.AgentCompleteData{FinalAnswer: "normal answer"},
			})
		}},
		{
			name: "QA error", want: imQAFailureReply(errors.New("QA execution error: upstream failed")),
			run: func(context.Context, *event.EventBus) error { return errors.New("upstream failed") },
		},
		{name: "error event", want: imQAFailureReply(errors.New("QA pipeline error: upstream failed")), run: func(
			ctx context.Context, bus *event.EventBus,
		) error {
			return bus.Emit(ctx, event.Event{Type: event.EventError, Data: event.ErrorData{Error: "upstream failed"}})
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			service := &Service{
				sessionService: &streamExitSessionService{run: tc.run}, messageService: &fullOutputMessageService{},
				streamManager: &fullOutputStreamManager{},
			}
			sender := &recordingStreamSender{}
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			err := runStreamExitHandler(ctx, service, sender, streamExitAgent())
			if err != nil {
				t.Fatal(err)
			}
			if ctx.Err() != nil {
				t.Fatal("handler only returned because test deadline expired")
			}
			_, final, ended := sender.snapshot()
			if !ended || final != tc.want {
				t.Fatalf("final=%q, ended=%v; want %q", final, ended, tc.want)
			}
		})
	}
}

func TestHandleMessageStreamWaitsForAgentCompletion(t *testing.T) {
	answerSent := make(chan struct{})
	releaseCompletion := make(chan struct{})
	producerStopped := make(chan struct{})
	service := &Service{
		sessionService: &streamExitSessionService{run: func(ctx context.Context, bus *event.EventBus) error {
			defer close(producerStopped)
			if err := bus.Emit(ctx, event.Event{
				Type: event.EventAgentFinalAnswer, Data: event.AgentFinalAnswerData{Content: "draft", Done: true},
			}); err != nil {
				return err
			}
			close(answerSent)
			select {
			case <-releaseCompletion:
			case <-ctx.Done():
				return ctx.Err()
			}
			return bus.Emit(ctx, event.Event{
				Type: event.EventAgentComplete, Data: event.AgentCompleteData{FinalAnswer: "draft"},
			})
		}},
		messageService: &fullOutputMessageService{}, streamManager: &fullOutputStreamManager{},
	}
	sender := &recordingStreamSender{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	returned := make(chan error, 1)
	go func() {
		returned <- runStreamExitHandler(ctx, service, sender, streamExitAgent())
	}()
	select {
	case <-answerSent:
	case <-time.After(time.Second):
		t.Fatal("answer was not emitted")
	}
	select {
	case <-returned:
		t.Fatal("terminal answer caused exit before agent completion")
	case <-time.After(30 * time.Millisecond):
	}
	close(releaseCompletion)
	select {
	case err := <-returned:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		cancel()
		<-returned
		t.Fatal("completion did not finish stream")
	}
	<-producerStopped
	_, final, ended := sender.snapshot()
	if !ended || final != "draft" {
		t.Fatalf("final=%q, ended=%v", final, ended)
	}
}

func TestHandleMessageStreamCancellationStopsQA(t *testing.T) {
	started := make(chan struct{})
	stopped := make(chan struct{})
	service := &Service{
		sessionService: &streamExitSessionService{run: func(ctx context.Context, _ *event.EventBus) error {
			close(started)
			<-ctx.Done()
			close(stopped)
			return ctx.Err()
		}},
		messageService: &fullOutputMessageService{}, streamManager: &fullOutputStreamManager{},
	}
	sender := &recordingStreamSender{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	returned := make(chan error, 1)
	go func() {
		returned <- runStreamExitHandler(ctx, service, sender, nil)
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("QA did not start")
	}
	cancel()
	select {
	case err := <-returned:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("cancellation did not end stream")
	}
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("QA was not cancelled")
	}
	_, _, ended := sender.snapshot()
	if !ended {
		t.Fatal("cancelled stream was not ended")
	}
}

func TestHandleMessageStreamWaitsForAsyncKnowledgeQA(t *testing.T) {
	qaReturned := make(chan struct{})
	releaseAnswer := make(chan struct{})
	emitted := make(chan error, 1)
	service := &Service{
		sessionService: &streamExitSessionService{run: func(ctx context.Context, bus *event.EventBus) error {
			// The real KnowledgeQA starts its stream consumer in a goroutine.
			go func() {
				select {
				case <-ctx.Done():
					emitted <- ctx.Err()
				case <-releaseAnswer:
					emitted <- bus.Emit(ctx, event.Event{
						Type: event.EventAgentFinalAnswer,
						Data: event.AgentFinalAnswerData{Content: "asynchronous answer", Done: true},
					})
				}
			}()
			close(qaReturned)
			return nil
		}},
		messageService: &fullOutputMessageService{}, streamManager: &fullOutputStreamManager{},
	}
	sender := &recordingStreamSender{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	returned := make(chan error, 1)
	go func() { returned <- runStreamExitHandler(ctx, service, sender, nil) }()
	select {
	case <-qaReturned:
	case <-time.After(time.Second):
		t.Fatal("KnowledgeQA did not return")
	}
	select {
	case <-returned:
		t.Fatal("KnowledgeQA return ended its asynchronous answer stream")
	case <-time.After(30 * time.Millisecond):
	}
	close(releaseAnswer)
	select {
	case err := <-returned:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		cancel()
		<-returned
		t.Fatal("asynchronous final answer did not end stream")
	}
	if err := <-emitted; err != nil {
		t.Fatal(err)
	}
	_, final, ended := sender.snapshot()
	if !ended || final != "asynchronous answer" {
		t.Fatalf("final=%q, ended=%v", final, ended)
	}
}

func (s *streamExitSessionService) AgentQA(ctx context.Context, _ *types.QARequest, bus *event.EventBus) error {
	return s.run(ctx, bus)
}

func (s *streamExitSessionService) KnowledgeQA(ctx context.Context, _ *types.QARequest, bus *event.EventBus) error {
	return s.run(ctx, bus)
}

func TestHandleMessageStreamReturnsWhenQAOmitsTerminalAnswer(t *testing.T) {
	for _, tc := range []struct {
		name            string
		agent, complete bool
	}{
		{name: "agent returns without events", agent: true},
		{name: "agent completes without final answer event", agent: true, complete: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			qaReturned := make(chan struct{})
			service := &Service{
				sessionService: &streamExitSessionService{run: func(ctx context.Context, bus *event.EventBus) error {
					defer close(qaReturned)
					if tc.complete {
						return bus.Emit(ctx, event.Event{
							Type: event.EventAgentComplete,
							Data: event.AgentCompleteData{FinalAnswer: "completed answer"},
						})
					}
					return nil
				}},
				messageService: &fullOutputMessageService{}, streamManager: &fullOutputStreamManager{},
			}
			var agent *types.CustomAgent
			if tc.agent {
				agent = &types.CustomAgent{Config: types.CustomAgentConfig{AgentMode: types.AgentModeSmartReasoning}}
			}
			sender := &recordingStreamSender{}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			returned := make(chan error, 1)
			go func() {
				returned <- runStreamExitHandler(ctx, service, sender, agent)
			}()
			select {
			case <-qaReturned:
			case <-time.After(2 * time.Second):
				t.Fatal("QA did not run")
			}
			select {
			case err := <-returned:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(time.Second):
				cancel()
				select {
				case <-returned:
				case <-time.After(2 * time.Second):
					t.Fatal("handler did not exit even after cancellation")
				}
				t.Fatal("QA returned but the stream handler remained blocked waiting for a terminal answer event")
			}
			_, final, ended := sender.snapshot()
			if !ended {
				t.Fatal("stream was not ended")
			}
			want := imNoAnswerFallback
			if tc.complete {
				want = "completed answer"
			}
			if final != want {
				t.Fatalf("final content = %q, want %q", final, want)
			}
		})
	}
}
