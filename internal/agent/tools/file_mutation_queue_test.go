package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/sandbox"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// slowSink widens the read-modify-write window so an unserialized append
// reliably loses content instead of only doing so under unlucky timing.
type slowSink struct {
	mu    sync.Mutex
	files map[string][]byte
}

func (s *slowSink) StatSessionFile(_ context.Context, _, filePath string) (*sandbox.RemoteStatEntry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, ok := s.files[filePath]
	if !ok {
		return nil, fmt.Errorf("no such file: %s", filePath)
	}
	return &sandbox.RemoteStatEntry{Path: filePath, Type: sandbox.RemoteEntryFile, Size: int64(len(data))}, nil
}

func (s *slowSink) ReadSessionFile(_ context.Context, _, filePath string) ([]byte, error) {
	s.mu.Lock()
	data, ok := s.files[filePath]
	out := append([]byte(nil), data...)
	s.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("no such file: %s", filePath)
	}
	// The real backend round-trips over the network here.
	time.Sleep(time.Millisecond)
	return out, nil
}

func (s *slowSink) WriteSessionWorkspaceFile(_ context.Context, _, filePath string, content []byte) error {
	time.Sleep(time.Millisecond)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.files == nil {
		s.files = map[string][]byte{}
	}
	s.files[filePath] = append([]byte(nil), content...)
	return nil
}

// The engine runs a response's tool calls in parallel, and append is
// read-modify-write against a backend with no atomic append. Without
// serialization the second write overwrites the first from a stale base, and
// the symptom is the model's own output silently going missing.
func TestConcurrentAppendsToSameFileDoNotLoseContent(t *testing.T) {
	const path = "/workspace/output/deck.html"
	sink := &slowSink{}
	tool := NewWriteSandboxFileTool(sink, 0)

	first, err := tool.Execute(sandboxFileTestContext(), mustWriteSandboxArgs(path, "start"))
	require.NoError(t, err)
	require.True(t, first.Success, first.Error)

	const appenders = 8
	var wg sync.WaitGroup
	for i := 0; i < appenders; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			result, err := tool.Execute(sandboxFileTestContext(), json.RawMessage(
				fmt.Sprintf(`{"path":%q,"content":"|%d","mode":"append"}`, path, i),
			))
			assert.NoError(t, err)
			assert.True(t, result.Success, result.Error)
		}(i)
	}
	wg.Wait()

	final := string(sink.files[path])
	assert.True(t, strings.HasPrefix(final, "start"))
	for i := 0; i < appenders; i++ {
		assert.Contains(t, final, fmt.Sprintf("|%d", i),
			"append %d was overwritten by a concurrent write", i)
	}
}

// Different paths must still run concurrently: serializing every mutation
// would remove the point of executing tool calls in parallel.
func TestMutationsToDifferentFilesDoNotBlockEachOther(t *testing.T) {
	release := lockSandboxFile("session-1", "/workspace/a.txt")
	defer release()

	done := make(chan struct{})
	go func() {
		lockSandboxFile("session-1", "/workspace/b.txt")()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("a held lock on one path blocked an unrelated path")
	}
}

// The lock table must not grow one entry per file ever touched.
func TestFileMutationQueueReleasesEntries(t *testing.T) {
	queue := &fileMutationQueue{locks: map[string]*fileMutationLock{}}
	for i := 0; i < 100; i++ {
		queue.lockFile(fmt.Sprintf("/workspace/%d.txt", i))()
	}
	assert.Empty(t, queue.locks)
}
