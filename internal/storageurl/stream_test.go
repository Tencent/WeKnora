package storageurl

import (
	"context"
	"strconv"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
)

func TestFindIncompleteRef(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want int // expected return; -1 means no match expected
	}{
		{
			"complete URL terminated by )",
			"![img](local://1/abc/img.png)",
			// The URL `local://1/abc/img.png` ends with `)` which is a terminator,
			// but the regex [^\s)\]>"]* matches up to `)` — the `)` is NOT included.
			// So the URL portion is `local://1/abc/img.png` and `)` terminates it.
			// The match does NOT reach end of string → should return -1.
			-1,
		},
		{
			"complete URL terminated by space",
			"text local://1/abc/img.png more text",
			-1,
		},
		{
			"truncated URL at end",
			"text ![img](local://1/abc/im",
			12, // starts at `l` in `local://`
		},
		{
			"just scheme at end",
			"text minio://",
			5,
		},
		{
			"partial scoped URL at end",
			"text storage://backend-a/co",
			5,
		},
		{
			"no storage URL",
			"just plain text http://example.com",
			-1,
		},
		{
			"URL at very end",
			"local://1/img.png",
			0,
		},
		{
			"truncated resource handle at end",
			"see ![img](resource://xifDo7NTSL",
			11,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, FindIncompleteRef(tt.in), "FindIncompleteRef(%q)", tt.in)
		})
	}
}

func TestFindIncompleteMarkdownImage(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want int
	}{
		{"complete image", "![img](local://1/a.png)", -1},
		{"complete then text", "![img](local://1/a.png) trailing", -1},
		{"truncated provider URL in image", `![知识助理"知识库"管理视图界面](minio://wizard-test/10000/exports/c91cf852`, 0},
		{"open paren only", "text ![alt](", 5},
		{"bare provider suffix without markdown", "text minio://wizard-test/10000/exp", -1},
		{"two images complete", "![a](local://1/a.png) ![b](local://1/b.png)", -1},
		{"first complete second incomplete", "![a](local://1/a.png) ![b](minio://part", 22},
		{"bracket inside alt text", "![a[b]](minio://wizard-test/10000/part", 0},
		{"destination with whitespace is prose, not a link", "![alt](see the figure below", -1},
		{
			"destination too long to be a link",
			"![alt](" + strings.Repeat("x", maxIncompleteImageBytes),
			-1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, FindIncompleteMarkdownImage(tt.in), "FindIncompleteMarkdownImage(%q)", tt.in)
		})
	}
}

func TestHoldbackCutoff(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want int // -1 means "expect len(in)", i.e. no holdback
	}{
		{"no holdback needed", "plain text with complete ![img](local://1/img.png) content", -1},
		{"truncated URL inside markdown image", "text ![img](local://1/abc/im", 5},
		{"bare truncated reference", "text local://1/abc/im", 5},
		{"unopened image destination", "prefix ![alt](", 7},
		{"empty chunk", "", -1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			want := tt.want
			if want == -1 {
				want = len(tt.in)
			}
			assert.Equal(t, want, HoldbackCutoff(tt.in), "HoldbackCutoff(%q)", tt.in)
		})
	}
}

// A reference split across two deltas must be held back and rewritten once
// complete, never emitted as a broken fragment.
func TestStreamRewriter_HoldsSplitReference(t *testing.T) {
	sr := NewStreamRewriter(NewRewriter(stubResolver("https://cdn.example.com/x.png"), "TEST"))
	ctx := context.Background()

	first := sr.Push(ctx, "answer-1", "here it is: ![img](resource://xifDo7", false, nil)
	assert.Equal(t, "here it is: ", first, "the incomplete image must be held back")

	second := sr.Push(ctx, "answer-1", "NTSL300Lp1goVutw) done", false, nil)
	assert.Equal(t, "![img](https://cdn.example.com/x.png) done", second)

	assert.Empty(t, sr.Push(ctx, "answer-1", "", true, nil))
	assert.Empty(t, sr.FlushAll(ctx), "nothing should remain held")
}

// Streams are keyed independently so interleaved events do not corrupt each
// other's holdback buffers.
func TestStreamRewriter_KeysAreIndependent(t *testing.T) {
	sr := NewStreamRewriter(NewRewriter(stubResolver("https://cdn.example.com/x.png"), "TEST"))
	ctx := context.Background()

	assert.Empty(t, sr.Push(ctx, "a", "![x](resource://aaaa", false, nil))
	assert.Equal(t, "plain b", sr.Push(ctx, "b", "plain b", false, nil))
	assert.Equal(t,
		"![x](https://cdn.example.com/x.png)",
		sr.Push(ctx, "a", "bbbbccccdddddd)", false, nil),
	)
}

// A stream that ends without a terminal chunk must not silently drop the tail.
func TestStreamRewriter_FlushAllReleasesHeldTail(t *testing.T) {
	sr := NewStreamRewriter(NewRewriter(stubResolver("https://cdn.example.com/x.png"), "TEST"))
	ctx := context.Background()

	meta := map[string]interface{}{"event_id": "answer-1", "is_fallback": true}
	assert.Equal(t, "tail ",
		sr.Push(ctx, "answer-1", "tail ![img](resource://partial", false, meta))
	assert.Equal(t,
		map[string]Held{"answer-1": {
			Content: "![img](https://cdn.example.com/x.png",
			Meta:    meta,
		}},
		sr.FlushAll(ctx),
		"the held tail must still reach the client, rewritten, with its metadata",
	)
}

// Text that merely looks like an unfinished image must not stall the stream:
// prose can contain a literal "](", and a URL never runs this long.
func TestStreamRewriter_LongUnclosedImageIsNotHeld(t *testing.T) {
	sr := NewStreamRewriter(NewRewriter(stubResolver("https://cdn.example.com/x.png"), "TEST"))
	chunk := "![never closed](" + strings.Repeat("x", maxIncompleteImageBytes)
	assert.Equal(t, chunk, sr.Push(context.Background(), "answer-1", chunk, false, nil))
}

// Holdback must be bounded so a stream that never terminates a reference cannot
// buffer the whole answer, and the byte-based release must not split a rune.
func TestStreamRewriter_HoldbackIsBounded(t *testing.T) {
	sr := NewStreamRewriter(NewRewriter(stubResolver("https://cdn.example.com/x.png"), "TEST"))
	ctx := context.Background()

	var emitted strings.Builder
	emitted.WriteString(sr.Push(ctx, "answer-1", "开头 resource://", false, nil))
	for i := 0; i < 20; i++ {
		emitted.WriteString(sr.Push(ctx, "answer-1", strings.Repeat("中", 1024), false, nil))
	}
	assert.Greater(t, emitted.Len(), 0, "bounded holdback must release the excess")
	assert.LessOrEqual(t, len(sr.held["answer-1"].content), maxHeldBytes)
	assert.True(t, utf8.ValidString(emitted.String()), "the release must not split a rune")
	assert.True(t, utf8.ValidString(sr.held["answer-1"].content), "the retained tail must stay valid")
}

// A disabled rewriter is a pass-through: the default API mode must not add
// latency or buffering.
func TestStreamRewriter_DisabledIsPassThrough(t *testing.T) {
	sr := NewStreamRewriter(NewRewriter(nil, "TEST"))
	in := "![img](resource://xifDo7"
	assert.Equal(t, in, sr.Push(context.Background(), "answer-1", in, false, nil))
	assert.False(t, sr.Enabled())
}

// Two goroutines pushing different streams share one resolver, so resolution
// must be serialised — this fails under -race if it is not.
func TestStreamRewriter_ConcurrentPushIsSafe(t *testing.T) {
	sr := NewStreamRewriter(NewRewriter(stubResolver("https://cdn.example.com/x.png"), "TEST"))
	ctx := context.Background()

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			key := "answer-" + strconv.Itoa(i)
			for j := 0; j < 20; j++ {
				// A distinct reference per push so every call really reaches the
				// resolver instead of hitting the memo.
				ref := "minio://bucket/10000/" + strconv.Itoa(i) + "-" + strconv.Itoa(j) + ".png"
				sr.Push(ctx, key, "![x]("+ref+") ", false, nil)
			}
		}(i)
	}
	wg.Wait()
	assert.Empty(t, sr.FlushAll(ctx))
}
