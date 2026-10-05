package service

import (
	"context"
	"errors"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

type checkpointDocReader struct {
	calls int
	fail  bool
}

func (r *checkpointDocReader) Read(context.Context, *types.ReadRequest) (*types.ReadResult, error) {
	r.calls++
	if r.fail {
		return nil, errors.New("parser unavailable")
	}
	return &types.ReadResult{MarkdownContent: "parsed book"}, nil
}

func TestDocumentRetryReusesCompletedParserStage(t *testing.T) {
	t.Setenv("WEKNORA_PROCESSING_CHECKPOINT_DIR", t.TempDir())
	service := &knowledgeService{}
	reader := &checkpointDocReader{}
	request := &types.ReadRequest{FileName: "book.txt", FileContent: []byte("book"), RequestID: "first"}
	if _, err := service.callDocReaderWithTimeout(context.Background(), reader, request); err != nil {
		t.Fatal(err)
	}
	reader.fail = true
	request.RequestID = "retry"
	result, err := service.callDocReaderWithTimeout(context.Background(), reader, request)
	if err != nil || result.MarkdownContent != "parsed book" || reader.calls != 1 {
		t.Fatalf("parser stage reran: calls=%d err=%v", reader.calls, err)
	}
	request.FileContent = []byte("changed book")
	_, err = service.callDocReaderWithTimeout(context.Background(), reader, request)
	if err == nil || reader.calls != 2 {
		t.Fatal("changed upload reused old parser result")
	}
}
