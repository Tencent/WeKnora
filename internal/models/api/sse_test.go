package api

import (
	"io"
	"strings"
	"testing"
)

// readAll drains the reader into a comparable transcript.
func readAll(t *testing.T, body string) []string {
	t.Helper()
	r := NewSSEReader(strings.NewReader(body))
	var out []string
	for {
		event, err := r.ReadEvent()
		if err == io.EOF {
			return out
		}
		if err != nil {
			t.Fatalf("ReadEvent: %v", err)
		}
		if event.Done {
			out = append(out, "[DONE]")
			continue
		}
		out = append(out, string(event.Data))
	}
}

func TestSSEReader_DoneSentinelVariants(t *testing.T) {
	cases := map[string]string{
		"canonical":      "data: [DONE]\n",
		"no space":       "data:[DONE]\n",
		"trailing space": "data: [DONE] \n",
		"crlf":           "data: [DONE]\r\n",
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			got := readAll(t, body)
			if len(got) != 1 || got[0] != "[DONE]" {
				t.Fatalf("events = %#v, want a single [DONE] sentinel", got)
			}
		})
	}
}

func TestSSEReader_SkipsNonDataLinesAndKeepsPayload(t *testing.T) {
	body := ": keep-alive\n" +
		"event: content_block_delta\n" +
		"id: 42\n" +
		"data: {\"a\":1}\n" +
		"\n" +
		"data:{\"b\":2}\n" +
		"data: [DONE]\n"
	got := readAll(t, body)
	want := []string{`{"a":1}`, `{"b":2}`, "[DONE]"}
	if len(got) != len(want) {
		t.Fatalf("events = %#v, want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("event %d = %q, want %q", i, got[i], want[i])
		}
	}
}

// A payload that merely contains "[DONE]" must not be mistaken for the
// sentinel.
func TestSSEReader_DoneOnlyMatchesTheSentinel(t *testing.T) {
	got := readAll(t, "data: {\"text\":\"[DONE]\"}\n")
	if len(got) != 1 || got[0] != `{"text":"[DONE]"}` {
		t.Fatalf("events = %#v", got)
	}
}
