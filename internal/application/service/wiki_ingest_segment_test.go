package service

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
)

func TestSplitWikiContentShortContentIsOneSegment(t *testing.T) {
	segments, truncated := splitWikiContent("短文本 short", 32, 4, 8)
	if truncated || len(segments) != 1 || segments[0] != "短文本 short" {
		t.Fatalf("got %q truncated=%v", segments, truncated)
	}
}

func TestSplitWikiContentCoversEveryRune(t *testing.T) {
	content := strings.Repeat("中文段落内容abc", 50) // 450 runes, multi-byte
	segments, truncated := splitWikiContent(content, 100, 10, 100)
	if truncated {
		t.Fatal("unexpected truncation")
	}
	if len(segments) < 2 {
		t.Fatalf("expected several segments, got %d", len(segments))
	}

	var rebuilt []rune
	for i, seg := range segments {
		runes := []rune(seg)
		if len(runes) > 100 {
			t.Fatalf("segment %d has %d runes, want <= 100", i, len(runes))
		}
		if i > 0 {
			prev := []rune(segments[i-1])
			if string(prev[len(prev)-10:]) != string(runes[:10]) {
				t.Fatalf("segment %d does not overlap the previous one", i)
			}
			runes = runes[10:]
		}
		rebuilt = append(rebuilt, runes...)
	}
	if string(rebuilt) != content {
		t.Fatal("segments minus overlap do not rebuild the original content")
	}
}

func TestSplitWikiContentPrefersNewlineCut(t *testing.T) {
	content := strings.Repeat("x", 95) + "\n" + strings.Repeat("y", 200)
	segments, _ := splitWikiContent(content, 100, 10, 10)
	if !strings.HasSuffix(segments[0], "\n") || len([]rune(segments[0])) != 96 {
		t.Fatalf("first segment should end at the newline, got %d runes", len([]rune(segments[0])))
	}
}

func TestSplitWikiContentHonorsSegmentCap(t *testing.T) {
	content := strings.Repeat("a", 1000)
	segments, truncated := splitWikiContent(content, 100, 10, 3)
	if !truncated || len(segments) != 3 {
		t.Fatalf("got %d segments truncated=%v, want 3 truncated", len(segments), truncated)
	}
}

func TestMergeCombinedExtractionsUnionsBySlug(t *testing.T) {
	merged := mergeCombinedExtractions([]*combinedExtraction{
		{Entities: []extractedItem{{Name: "Tencent", Slug: "entity/tencent", Aliases: []string{"腾讯"}}}},
		{
			Entities: []extractedItem{
				{
					Name: "Tencent Holdings", Slug: "entity/tencent",
					Aliases: []string{"腾讯", "TCEHY"}, Description: "company",
				},
				{Name: "WeKnora", Slug: "entity/weknora"},
			},
			Concepts: []extractedItem{{Name: "RAG", Slug: "concept/rag"}},
		},
	})
	if len(merged.Entities) != 2 || len(merged.Concepts) != 1 {
		t.Fatalf("got %d entities, %d concepts", len(merged.Entities), len(merged.Concepts))
	}
	tencent := merged.Entities[0]
	if tencent.Name != "Tencent" || tencent.Description != "company" {
		t.Fatalf("unexpected merged item %+v", tencent)
	}
	want := []string{"腾讯", "Tencent Holdings", "TCEHY"}
	if strings.Join(tencent.Aliases, "|") != strings.Join(want, "|") {
		t.Fatalf("aliases = %q, want %q", tencent.Aliases, want)
	}
}

// segmentKeyedChatModel answers WikiCandidateSlugPrompt calls with the
// response registered for the marker found in the prompt.
type segmentKeyedChatModel struct {
	mu        sync.Mutex
	responses map[string]string
	calls     int
}

func (m *segmentKeyedChatModel) Chat(
	_ context.Context,
	messages []chat.Message,
	_ *chat.ChatOptions,
) (*types.ChatResponse, error) {
	m.mu.Lock()
	m.calls++
	m.mu.Unlock()
	var prompt strings.Builder
	for _, msg := range messages {
		prompt.WriteString(msg.Content)
	}
	for marker, resp := range m.responses {
		if strings.Contains(prompt.String(), marker) {
			return &types.ChatResponse{Content: resp}, nil
		}
	}
	return &types.ChatResponse{Content: `{"entities":[],"concepts":[]}`}, nil
}

func (m *segmentKeyedChatModel) ChatStream(
	context.Context,
	[]chat.Message,
	*chat.ChatOptions,
) (<-chan types.StreamResponse, error) {
	return nil, nil
}

func (m *segmentKeyedChatModel) GetModelName() string { return "segment" }
func (m *segmentKeyedChatModel) GetModelID() string   { return "segment" }

func TestExtractCandidateSlugsReadsEverySegment(t *testing.T) {
	model := &segmentKeyedChatModel{responses: map[string]string{
		"SEGMENT_ONE": `{"entities":[{"name":"Alpha","slug":"entity/alpha"}],"concepts":[]}`,
		"SEGMENT_TWO": `{"entities":[{"name":"Alpha","slug":"entity/alpha","aliases":["A"]}],` +
			`"concepts":[{"name":"Late Idea","slug":"concept/late-idea"}]}`,
		"SEGMENT_THREE": `not json`,
	}}
	svc := &wikiIngestService{}
	entities, concepts, slugItems, failed, err := svc.extractCandidateSlugs(
		context.Background(), model, "kb",
		[]string{"SEGMENT_ONE text", "SEGMENT_TWO text", "SEGMENT_THREE text"},
		"English", nil, &WikiBatchContext{},
	)
	if err != nil {
		t.Fatalf("extractCandidateSlugs: %v", err)
	}
	if model.calls != 3 {
		t.Fatalf("model called %d times, want 3", model.calls)
	}
	if failed != 1 {
		t.Fatalf("failed segments = %d, want 1", failed)
	}
	if len(entities) != 1 || len(concepts) != 1 {
		t.Fatalf("got %d entities, %d concepts", len(entities), len(concepts))
	}
	if _, ok := slugItems["concept/late-idea"]; !ok {
		t.Fatalf("candidate from the second segment is missing: %v", slugItems)
	}
}

func TestExtractCandidateSlugsFailsWhenEverySegmentFails(t *testing.T) {
	model := &segmentKeyedChatModel{responses: map[string]string{"SEG": "not json"}}
	svc := &wikiIngestService{}
	_, _, _, failed, err := svc.extractCandidateSlugs(
		context.Background(), model, "kb",
		[]string{"SEG one", "SEG two"},
		"English", nil, &WikiBatchContext{},
	)
	if err == nil || failed != 2 {
		t.Fatalf("err=%v failed=%d, want error with 2 failed", err, failed)
	}
}
