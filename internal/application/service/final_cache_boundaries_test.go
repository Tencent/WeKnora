package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/chat"
)

func TestSharedCacheHTMLImageDestinations(t *testing.T) {
	for _, markup := range []string{
		`<img src="%s" alt="diagram" title="A">`,
		`<IMG alt='diagram' SRC = '%s' title='A'>`,
		`<img src=%s alt=diagram title=A>`,
		`<img title="x > y src='keep'" src="%s" alt="diagram"/>`,
	} {
		t.Run(markup, func(t *testing.T) {
			model := &processingCacheChat{identity: "model"}
			runtime := newStageTestRuntime()
			calls := 0
			for _, address := range []string{"local://old.png", "local://current.png"} {
				input := fmt.Sprintf(markup, address)
				got, err := cachedProcessingModelValue(
					context.Background(), runtime, 1, "doc", "html", model,
					[]chat.Message{{Content: input}}, nil, nil,
					func(string) error { return nil },
					func(_ context.Context, messages []chat.Message) (string, error) {
						calls++
						return messages[0].Content, nil
					},
				)
				if err != nil || got != input {
					t.Fatalf("roundtrip=%q want=%q err=%v", got, input, err)
				}
			}
			if calls != 1 {
				t.Fatalf("HTML address change missed cache: %d calls", calls)
			}
		})
	}
}

func TestWikiHTMLImageMaskPreservesDescriptions(t *testing.T) {
	input := `<img title="x > y src='keep'" src="local://current.png" alt="diagram">`
	masked, urls := maskImageURLs(input)
	want := `<img title="x > y src='keep'" src="wkimg:0001" alt="diagram">`
	if masked != want {
		t.Fatalf("masked=%q want=%q", masked, want)
	}
	encoded, err := json.Marshal(map[string]string{"content": masked})
	if err != nil {
		t.Fatal(err)
	}
	var restored map[string]string
	if err := json.Unmarshal([]byte(unmaskImageURLs(string(encoded), urls)), &restored); err != nil {
		t.Fatal(err)
	}
	if restored["content"] != input {
		t.Fatalf("restored=%q", restored["content"])
	}
	for _, changed := range []string{
		strings.Replace(input, "diagram", "other", 1),
		strings.Replace(input, "x > y", "x < y", 1),
	} {
		other, _ := maskImageURLs(changed)
		if other == masked {
			t.Fatal("description change must change model input")
		}
	}
}

func TestSharedCacheMarkdownDestinationBoundaries(t *testing.T) {
	model := &processingCacheChat{identity: "model"}
	runtime := newStageTestRuntime()
	calls := 0
	for _, destination := range []string{
		`https://old/a(old).png`,
		`https://current/a(new).jpg`,
		`<https://current/image with spaces.png>`,
		`https://current/a\(new\).png`,
	} {
		input := `![alt](` + destination + ` "title A")`
		got, err := cachedProcessingModelValue(
			context.Background(),
			runtime,
			1,
			"doc",
			"boundary",
			model,
			[]chat.Message{{Content: input}},
			nil,
			nil,
			func(string) error { return nil },
			func(_ context.Context, m []chat.Message) (string, error) { calls++; return m[0].Content, nil },
		)
		if err != nil {
			t.Fatal(err)
		}
		if got != input {
			t.Fatalf("roundtrip=%q want %q", got, input)
		}
	}
	if calls != 1 {
		t.Fatalf("location change missed: %d calls", calls)
	}
	_, err := cachedProcessingModelValue(
		context.Background(),
		runtime,
		1,
		"doc",
		"boundary",
		model,
		[]chat.Message{{Content: `![alt](https://current/x "title B")`}},
		nil,
		nil,
		func(string) error { return nil },
		func(_ context.Context, m []chat.Message) (string, error) {
			calls++
			if !strings.Contains(m[0].Content, `"title B"`) {
				t.Fatal("title removed from model input")
			}
			return m[0].Content, nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatal("title change reused stale result")
	}
}

func TestWikiMarkdownDestinationTitleRoundtrip(t *testing.T) {
	input := `![alt](<https://host/a(old) image.png> "title A")`
	masked, urls := maskImageURLs(input)
	if masked != `![alt](wkimg:0001 "title A")` {
		t.Fatalf("masked=%q", masked)
	}
	if got := unmaskImageURLs(masked, urls); got != input {
		t.Fatalf("roundtrip=%q", got)
	}
	model := &templateCaptureChatModel{response: `![alt](wkimg:0001 "title A")`}
	got, err := (&wikiIngestService{}).generateWithTemplate(
		context.Background(),
		model,
		`{{.Content}}`,
		map[string]string{"Content": input},
	)
	if err != nil || got != input {
		t.Fatalf("wiki got=%q err=%v", got, err)
	}
}

func TestWikiImageRestoreInsideJSONPreservesEscapedTitle(t *testing.T) {
	input := `{"details":"![alt](wkimg:0001 \"title A\")"}`
	got := unmaskImageURLs(input, map[string]string{"wkimg:0001": `<https://current/a(new) image.png>`})
	var parsed map[string]string
	if err := json.Unmarshal([]byte(got), &parsed); err != nil {
		t.Fatal(err)
	}
	if parsed["details"] != `![alt](<https://current/a(new) image.png> "title A")` {
		t.Fatalf("lost image or title: %s", got)
	}
}
