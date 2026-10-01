package session

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/agent/skills"
	"github.com/Tencent/WeKnora/internal/application/service"
	"github.com/Tencent/WeKnora/internal/event"
	"github.com/Tencent/WeKnora/internal/sandbox"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

type stubShellRunner struct {
	stdout   string
	exitCode int
	err      error
	calls    int
}

func (s *stubShellRunner) ExecShellCommand(
	_ context.Context, _ string, _, _ string, _ time.Duration, _ map[string]string,
) (*sandbox.ExecuteResult, error) {
	s.calls++
	if s.err != nil {
		return nil, s.err
	}
	return &sandbox.ExecuteResult{ExitCode: s.exitCode, Stdout: s.stdout}, nil
}

type stubSandboxIDLookup struct {
	id string
	ok bool
}

func (s stubSandboxIDLookup) BoundSandboxID(context.Context, string) (string, bool) {
	return s.id, s.ok
}

func newCheckpointHandler(
	t *testing.T, runner service.SandboxShellRunner, lookup SandboxIDLookup,
) (*AgentStreamHandler, *types.Message) {
	t.Helper()
	message := &types.Message{ID: "m1", SessionID: "s1", Role: "assistant"}
	h := NewAgentStreamHandler(
		context.Background(), "s1", "m1", "req1", 1, time.Now(),
		message, &completionEventRecorder{}, event.NewEventBus(), nil,
		service.NewWorkspaceCheckpointer(runner), lookup,
	)
	return h, message
}

func completeEvent() event.Event {
	return event.Event{Data: event.AgentCompleteData{MessageID: "m1", FinalAnswer: "done"}}
}

func TestHandleCompleteRecordsCheckpoint(t *testing.T) {
	runner := &stubShellRunner{stdout: strings.Repeat("a", 40) + "\n"}
	h, message := newCheckpointHandler(t, runner, stubSandboxIDLookup{id: "sbx-1", ok: true})

	require.NoError(t, h.handleComplete(context.Background(), completeEvent()))

	require.Equal(t, 1, runner.calls)
	require.NotNil(t, message.SandboxCheckpoint)
	require.Equal(t, "sbx-1", message.SandboxCheckpoint.SandboxID)
	require.Equal(t, strings.Repeat("a", 40), message.SandboxCheckpoint.CommitSHA)
}

// A failed checkpoint must never disturb the reply: the message still
// completes, it just carries no checkpoint and cannot serve as a fork point.
func TestHandleCompleteSurvivesCheckpointFailure(t *testing.T) {
	runner := &stubShellRunner{err: errors.New("sandbox unreachable")}
	h, message := newCheckpointHandler(t, runner, stubSandboxIDLookup{id: "sbx-1", ok: true})

	require.NoError(t, h.handleComplete(context.Background(), completeEvent()))

	require.Nil(t, message.SandboxCheckpoint)
	require.True(t, message.IsCompleted)
}

func TestHandleCompleteSkipsCheckpointWithoutBoundSandbox(t *testing.T) {
	runner := &stubShellRunner{stdout: strings.Repeat("a", 40) + "\n"}
	h, message := newCheckpointHandler(t, runner, stubSandboxIDLookup{ok: false})

	require.NoError(t, h.handleComplete(context.Background(), completeEvent()))

	require.Zero(t, runner.calls)
	require.Nil(t, message.SandboxCheckpoint)
}

func TestHandleCompleteWorksWithoutCheckpointerWired(t *testing.T) {
	message := &types.Message{ID: "m1", SessionID: "s1", Role: "assistant"}
	h := NewAgentStreamHandler(
		context.Background(), "s1", "m1", "req1", 1, time.Now(),
		message, &completionEventRecorder{}, event.NewEventBus(), nil, nil, nil,
	)

	require.NoError(t, h.handleComplete(context.Background(), completeEvent()))
	require.Nil(t, message.SandboxCheckpoint)
}

type capturingMessageService struct {
	interfaces.MessageService
	content    string
	checkpoint *types.SandboxCheckpoint
}

func (s *capturingMessageService) UpdateMessage(_ context.Context, message *types.Message) error {
	if message == nil {
		return nil
	}
	s.content = message.Content
	if message.SandboxCheckpoint != nil {
		cp := *message.SandboxCheckpoint
		s.checkpoint = &cp
	}
	return nil
}

func (s *capturingMessageService) IndexMessageToKB(context.Context, string, string, string, string) {}

func TestQuickAnswerCompletionPersistsSandboxCheckpoint(t *testing.T) {
	runner := &stubShellRunner{stdout: strings.Repeat("a", 40) + "\n"}
	bus := event.NewEventBus()
	message := &types.Message{
		ID: "m1", SessionID: "s1", Role: "assistant", Content: "hello from quick answer",
	}
	streamHandler := NewAgentStreamHandler(
		context.Background(), "s1", "m1", "req1", 1, time.Now(),
		message, &completionEventRecorder{}, bus,
		nil, service.NewWorkspaceCheckpointer(runner), stubSandboxIDLookup{id: "sbx-1", ok: true},
	)
	streamHandler.Subscribe()

	stub := &capturingMessageService{}
	h := &Handler{messageService: stub}
	h.completeQuickAnswerTurn(context.Background(), &sseStreamContext{
		eventBus:         bus,
		streamHandler:    streamHandler,
		assistantMessage: message,
	}, "", "")

	require.Equal(t, 1, runner.calls, "quick-answer complete must checkpoint before persist")
	require.NotNil(t, stub.checkpoint, "UpdateMessage must see SandboxCheckpoint")
	require.Equal(t, "sbx-1", stub.checkpoint.SandboxID)
	require.Equal(t, strings.Repeat("a", 40), stub.checkpoint.CommitSHA)
	require.Equal(t, "hello from quick answer", stub.content, "complete event must not re-append the answer")
}

// layoutCollectSource records the directory CollectWithNotify asked to scan
// and only yields files that sit under the advertised OutputDir.
type layoutCollectSource struct {
	layout     sandbox.WorkspaceLayout
	listedDirs []string
	entries    map[string][]sandbox.RemoteDirEntry
	contents   map[string][]byte
	// layoutLookups guards against the collect target and the skip decision
	// drifting back into two lookups that can disagree.
	layoutLookups int
}

func (s *layoutCollectSource) ListSessionFiles(_ context.Context, _, dir string) ([]sandbox.RemoteDirEntry, error) {
	s.listedDirs = append(s.listedDirs, dir)
	return s.entries[dir], nil
}

func (s *layoutCollectSource) ReadSessionFile(_ context.Context, _ string, path string) ([]byte, error) {
	if data, ok := s.contents[path]; ok {
		return data, nil
	}
	return nil, errors.New("not found: " + path)
}

func (s *layoutCollectSource) SessionWorkspaceLayout(context.Context, string) (sandbox.WorkspaceLayout, error) {
	s.layoutLookups++
	return s.layout, nil
}

type collectDirFileService struct {
	interfaces.FileService
}

func (s *collectDirFileService) SaveBytes(
	_ context.Context, _ []byte, _ uint64, fileName string, _ bool,
) (string, error) {
	return "fake://" + fileName, nil
}

func TestCompletionCollectsFromLayoutOutputDir(t *testing.T) {
	layout := sandbox.WorkspaceLayout{
		Origin:    sandbox.WorkspaceOriginHost,
		Root:      "/Users/dev/proj",
		OutputDir: "/Users/dev/AppData/s1/output",
	}
	source := &layoutCollectSource{
		layout: layout,
		entries: map[string][]sandbox.RemoteDirEntry{
			layout.Root: {{
				Name: "main.go", Path: layout.Root + "/src/main.go",
				Type: sandbox.RemoteEntryFile, Size: 12,
				ModTime: time.Date(2026, 7, 10, 10, 20, 33, 0, time.UTC),
			}},
			layout.OutputDir: {{
				Name: "report.pptx", Path: layout.OutputDir + "/report.pptx",
				Type: sandbox.RemoteEntryFile, Size: 4,
				ModTime: time.Date(2026, 7, 10, 10, 20, 34, 0, time.UTC),
			}},
		},
		contents: map[string][]byte{
			layout.Root + "/src/main.go":      []byte("package main"),
			layout.OutputDir + "/report.pptx": []byte("PPTX"),
		},
	}
	message := &types.Message{ID: "m1", SessionID: "s1", Role: "assistant"}
	h := NewAgentStreamHandler(
		context.Background(), "s1", "m1", "req1", 1, time.Now(),
		message, &completionEventRecorder{}, event.NewEventBus(),
		service.NewArtifactCollector(
			source, &collectDirFileService{}, completionHistory{}, nil, service.ArtifactCollectorConfig{},
		),
		nil, nil,
	)

	require.NoError(t, h.handleComplete(context.Background(), completeEvent()))
	require.Equal(t, []string{layout.OutputDir}, source.listedDirs)
	require.Len(t, message.Artifacts, 1)
	require.Equal(t, "report.pptx", message.Artifacts[0].FileName)
	require.Equal(t, 1, source.layoutLookups,
		"the collect directory and the skip decision must come from one lookup")
}

func TestCompletionSkipsCollectWhenHostLayoutHasNoOutputDir(t *testing.T) {
	source := &layoutCollectSource{
		layout: sandbox.WorkspaceLayout{Origin: sandbox.WorkspaceOriginHost, Root: "/Users/dev/proj"},
		entries: map[string][]sandbox.RemoteDirEntry{
			"/Users/dev/proj": {{
				Name: "main.go", Path: "/Users/dev/proj/src/main.go",
				Type: sandbox.RemoteEntryFile, Size: 12,
				ModTime: time.Date(2026, 7, 10, 10, 20, 33, 0, time.UTC),
			}},
		},
		contents: map[string][]byte{
			"/Users/dev/proj/src/main.go": []byte("package main"),
		},
	}
	message := &types.Message{ID: "m1", SessionID: "s1", Role: "assistant"}
	h := NewAgentStreamHandler(
		context.Background(), "s1", "m1", "req1", 1, time.Now(),
		message, &completionEventRecorder{}, event.NewEventBus(),
		service.NewArtifactCollector(
			source, &collectDirFileService{}, completionHistory{}, nil, service.ArtifactCollectorConfig{},
		),
		nil, nil,
	)

	require.NoError(t, h.handleComplete(context.Background(), completeEvent()))
	require.Empty(t, source.listedDirs)
	require.Empty(t, message.Artifacts)
}

func TestCompletionSkipsCollectWhenOutputDirEqualsRoot(t *testing.T) {
	source := &layoutCollectSource{
		layout: sandbox.WorkspaceLayout{
			Origin:    sandbox.WorkspaceOriginHost,
			Root:      "/Users/dev/proj",
			OutputDir: "/Users/dev/proj",
		},
		entries: map[string][]sandbox.RemoteDirEntry{
			"/Users/dev/proj": {{
				Name: "main.go", Path: "/Users/dev/proj/src/main.go",
				Type: sandbox.RemoteEntryFile, Size: 12,
				ModTime: time.Date(2026, 7, 10, 10, 20, 33, 0, time.UTC),
			}},
		},
		contents: map[string][]byte{
			"/Users/dev/proj/src/main.go": []byte("package main"),
		},
	}
	message := &types.Message{ID: "m1", SessionID: "s1", Role: "assistant"}
	h := NewAgentStreamHandler(
		context.Background(), "s1", "m1", "req1", 1, time.Now(),
		message, &completionEventRecorder{}, event.NewEventBus(),
		service.NewArtifactCollector(
			source, &collectDirFileService{}, completionHistory{}, nil, service.ArtifactCollectorConfig{},
		),
		nil, nil,
	)

	require.NoError(t, h.handleComplete(context.Background(), completeEvent()))
	require.Empty(t, source.listedDirs)
	require.Empty(t, message.Artifacts)
}

type failingLayoutCollectSource struct {
	listedDirs []string
}

func (s *failingLayoutCollectSource) ListSessionFiles(
	_ context.Context, _, dir string,
) ([]sandbox.RemoteDirEntry, error) {
	s.listedDirs = append(s.listedDirs, dir)
	return nil, nil
}

func (s *failingLayoutCollectSource) ReadSessionFile(context.Context, string, string) ([]byte, error) {
	return nil, errors.New("not found")
}

func (s *failingLayoutCollectSource) SessionWorkspaceLayout(context.Context, string) (sandbox.WorkspaceLayout, error) {
	return sandbox.WorkspaceLayout{}, errors.New("layout unavailable")
}

func TestCompletionSkipsCollectWhenLayoutLookupFails(t *testing.T) {
	source := &failingLayoutCollectSource{}
	message := &types.Message{ID: "m1", SessionID: "s1", Role: "assistant"}
	h := NewAgentStreamHandler(
		context.Background(), "s1", "m1", "req1", 1, time.Now(),
		message, &completionEventRecorder{}, event.NewEventBus(),
		service.NewArtifactCollector(
			source, &collectDirFileService{}, completionHistory{}, nil, service.ArtifactCollectorConfig{},
		),
		nil, nil,
	)

	require.NoError(t, h.handleComplete(context.Background(), completeEvent()))
	require.Empty(t, source.listedDirs)
	require.Empty(t, message.Artifacts)
}

func TestCompletionCollectDirOnRemoteUsesArtifactOutputDir(t *testing.T) {
	source := &remoteCollectSource{}
	message := &types.Message{ID: "m1", SessionID: "s1", Role: "assistant"}
	h := NewAgentStreamHandler(
		context.Background(), "s1", "m1", "req1", 1, time.Now(),
		message, &completionEventRecorder{}, event.NewEventBus(),
		service.NewArtifactCollector(
			source, &collectDirFileService{}, completionHistory{}, nil, service.ArtifactCollectorConfig{},
		),
		nil, nil,
	)

	require.NoError(t, h.handleComplete(context.Background(), completeEvent()))
	require.Equal(t, []string{skills.ArtifactOutputDir()}, source.listedDirs)
}

// remoteCollectSource is a sandbox filesystem that does not advertise a
// workspace layout, matching remote backends that keep /workspace/output.
type remoteCollectSource struct {
	listedDirs []string
}

func (s *remoteCollectSource) ListSessionFiles(_ context.Context, _, dir string) ([]sandbox.RemoteDirEntry, error) {
	s.listedDirs = append(s.listedDirs, dir)
	return nil, nil
}

func (s *remoteCollectSource) ReadSessionFile(context.Context, string, string) ([]byte, error) {
	return nil, errors.New("not found")
}
