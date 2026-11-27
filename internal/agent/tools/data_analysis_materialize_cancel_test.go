package tools

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

type cancelMaterializationReader struct {
	cancel context.CancelFunc
	reads  int
	closed bool
}

func (r *cancelMaterializationReader) Read(p []byte) (int, error) {
	r.reads++
	if r.reads > 8 {
		return 0, io.EOF
	}
	r.cancel()
	return copy(p, "id,value\n1,data\n"), nil
}

func (r *cancelMaterializationReader) Close() error {
	r.closed = true
	return nil
}

func TestMaterializeKnowledgeFileStopsAfterCancellation(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TMPDIR", dir)
	t.Setenv("TMP", dir)
	t.Setenv("TEMP", dir)
	for _, alreadyCanceled := range []bool{false, true} {
		t.Run(map[bool]string{false: "during_copy", true: "before_copy"}[alreadyCanceled], func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if alreadyCanceled {
				cancel()
			}
			reader := &cancelMaterializationReader{cancel: cancel}
			service := &fakeFileService{readers: map[string]func() (io.ReadCloser, error){
				"report.csv": func() (io.ReadCloser, error) { return reader, nil },
			}}
			tool := &DataAnalysisTool{fileService: service}
			path, cleanup, err := tool.materializeKnowledgeFile(ctx, &types.Knowledge{
				ID: "cancelled", FileType: "csv", FilePath: "report.csv",
			})
			defer cleanup()
			if path != "" {
				t.Cleanup(func() { _ = os.Remove(path) })
			}
			require.Truef(t, errors.Is(err, context.Canceled), "got error %v after %d reads", err, reader.reads)
			require.Empty(t, path)
			require.True(t, reader.closed)
			maxReads := 1
			if alreadyCanceled {
				maxReads = 0
			}
			require.LessOrEqual(t, reader.reads, maxReads)
			matches, err := filepath.Glob(filepath.Join(dir, "weknora-data-analysis-*"))
			require.NoError(t, err)
			require.Empty(t, matches)
		})
	}
}
