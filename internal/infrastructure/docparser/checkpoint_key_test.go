package docparser

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

func TestReadCheckpointInputs(t *testing.T) {
	r := types.ReadRequest{
		FileName: "book.pdf", FileContent: []byte("book"), ParserEngine: "mineru_cloud",
		RequestID: "first", ParserEngineOverrides: map[string]string{"language": "ch"},
	}
	key := ReadCheckpointKey(&r)
	r.RequestID = "retry"
	if key != ReadCheckpointKey(&r) {
		t.Fatal("retry invalidated checkpoint")
	}
	r.FileContent = []byte("changed book")
	if key == ReadCheckpointKey(&r) {
		t.Fatal("changed file reused result")
	}
	r.FileContent = []byte("book")
	r.ParserEngineOverrides["language"] = "en"
	if key == ReadCheckpointKey(&r) {
		t.Fatal("changed parser settings reused result")
	}
}
