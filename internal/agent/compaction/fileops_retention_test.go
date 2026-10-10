package compaction

import (
	"fmt"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFileOpsKeepsNewPathsAfterReachingLimit(t *testing.T) {
	for _, tool := range []string{"read_file", "write_sandbox_file", "edit_sandbox_file"} {
		t.Run(tool, func(t *testing.T) {
			var history []chat.Message
			for i := 0; i < maxTrackedFilePaths; i++ {
				history = append(history, toolCallMsg(tool, fmt.Sprintf(`{"path":"/workspace/%02d.txt"}`, i)))
			}
			previous := extractFileOps("", history).format()
			current := extractFileOps(previous, []chat.Message{
				toolCallMsg(tool, `{"path":"/workspace/newest.txt"}`),
			})
			read, modified := current.resolve()
			paths := append(read, modified...)
			require.Len(t, paths, maxTrackedFilePaths)
			assert.Contains(t, paths, "/workspace/newest.txt")
			assert.NotContains(t, paths, "/workspace/00.txt")
			assert.Contains(t, paths, "/workspace/49.txt")
			// The new artifact must survive a further compaction as well.
			assert.Contains(t, extractFileOps(current.format()).format(), "/workspace/newest.txt")
		})
	}
}

func TestFileOpsDuplicateAtLimitDoesNotEvictAnotherPath(t *testing.T) {
	var paths []string
	for i := 0; i < maxTrackedFilePaths; i++ {
		paths = append(paths, fmt.Sprintf("/workspace/%02d.txt", i))
	}
	before := append([]string(nil), paths...)
	assert.Equal(t, before, appendUnique(paths, " /workspace/00.txt "))
	assert.Equal(t, before, appendUnique(paths, " "))
}
