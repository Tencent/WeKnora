package searchutil

import (
	"strings"
	"testing"
)

func TestJoinChunkContentWindowBoundaries(t *testing.T) {
	for _, overlap := range []string{strings.Repeat("界🙂", 200), "abcdefghijkl", "前缀包含中文和表情符号🙂🙂🙂"} {
		acc := strings.Repeat("旧", 500) + overlap
		next := overlap + strings.Repeat("新", 500)
		want := acc + strings.Repeat("新", 500)
		if got := JoinChunkContent(acc, next, "\n"); got != want {
			t.Fatal("bounded overlap changed the joined content")
		}
	}
	overlap := "abcdefghijkl"
	acc := "before " + overlap
	next := overlap + strings.Repeat("中", 500) + "\xff\xfe after"
	if got := JoinChunkContent(acc, next, "\n"); got != acc+strings.Repeat("中", 500)+"\ufffd\ufffd after" {
		t.Fatalf("invalid UTF-8 normalization changed: %q", got)
	}
}

func BenchmarkJoinChunkContentLargeEditedBodies(b *testing.B) {
	acc := strings.Repeat("前", 128*1024) + "end of first chunk"
	next := "start of next chunk" + strings.Repeat("后", 128*1024)
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		chunkJoinBenchmarkResult = JoinChunkContent(acc, next, "\n\n")
	}
}

var chunkJoinBenchmarkResult string
