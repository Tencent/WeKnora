package api

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"
)

func TestSSEReaderJSONProviderFraming(t *testing.T) {
	for _, newline := range []string{"\n", "\r\n", "\r"} {
		for _, prefix := range []string{"", "\ufeff"} {
			t.Run(prefix+newline, func(t *testing.T) {
				body := prefix + ": heartbeat" + newline + "event: message" + newline +
					"data: {\"choices\":" + newline +
					"data: [{\"delta\":{\"content\":\"hello\"},\"finish_reason\":\"stop\"}] }" + newline + newline +
					"data: [DONE]" + newline + newline
				got := readAll(t, body)
				if len(got) != 2 || got[1] != "[DONE]" {
					t.Fatalf("events = %#v", got)
				}
				var decoded struct {
					Choices []struct {
						Delta        struct{ Content string }
						FinishReason string `json:"finish_reason"`
					}
				}
				if err := json.Unmarshal([]byte(got[0]), &decoded); err != nil {
					t.Fatalf("provider payload %q: %v", got[0], err)
				}
				if len(decoded.Choices) != 1 || decoded.Choices[0].Delta.Content != "hello" ||
					decoded.Choices[0].FinishReason != "stop" {
					t.Fatalf("packet = %#v", decoded)
				}
				if !strings.Contains(got[0], "\n") {
					t.Fatalf("data lines were not joined: %q", got[0])
				}
			})
		}
	}
}

func TestSSEReaderKeepsAdjacentJSONProviderCompatibility(t *testing.T) {
	tests := []struct {
		name, body string
		want       []string
	}{
		{
			"adjacent complete packets",
			"data: {\"a\":1}\ndata: {\"b\":2}\ndata: [DONE]\n",
			[]string{`{"a":1}`, `{"b":2}`, "[DONE]"},
		},
		{
			"malformed packet before healthy packet",
			"data: not-json\ndata: {\"a\":1}\n\n",
			[]string{"not-json", `{"a":1}`},
		},
		{"incomplete malformed object before healthy packet", "data: {\ndata: {\"a\":1}\n\n", []string{"{", `{"a":1}`}},
		{
			"array with independently valid JSON element",
			"data: [\ndata: {\"a\":1}\ndata: ]\n\n",
			[]string{"[\n{\"a\":1}\n]"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := readAll(t, tt.body)
			if len(got) != len(tt.want) {
				t.Fatalf("events=%#v, want %#v", got, tt.want)
			}
			for i := range tt.want {
				if got[i] != tt.want[i] {
					t.Fatalf("event %d=%q, want %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}

type oneByteSSEReader struct{ source io.Reader }

func (r oneByteSSEReader) Read(p []byte) (int, error) {
	if len(p) > 1 {
		p = p[:1]
	}
	return r.source.Read(p)
}

func TestSSEReaderSplitCRLFAndBOM(t *testing.T) {
	body := "\ufeffdata: {\"choices\":\r\ndata: [{\"delta\":{\"content\":\"hello\"}}]}\r\n\r\n"
	reader := NewSSEReader(oneByteSSEReader{strings.NewReader(body)})
	event, err := reader.ReadEvent()
	if err != nil {
		t.Fatal(err)
	}
	var packet struct {
		Choices []struct{ Delta struct{ Content string } }
	}
	if err := json.Unmarshal(event.Data, &packet); err != nil {
		t.Fatal(err)
	}
	if len(packet.Choices) != 1 || packet.Choices[0].Delta.Content != "hello" {
		t.Fatalf("packet=%#v", packet)
	}
	if _, err := reader.ReadEvent(); err != io.EOF {
		t.Fatalf("next event error=%v, want EOF", err)
	}
}

func TestSSEReaderCompletePacketDoesNotWaitForAnotherFrame(t *testing.T) {
	for _, ending := range []struct{ name, value string }{{"LF", "\n"}, {"CR", "\r"}, {"CRLF", "\r\n"}} {
		for _, oneByte := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/oneByte=%t", ending.name, oneByte), func(t *testing.T) {
				source, writer := io.Pipe()
				t.Cleanup(func() { _ = source.Close(); _ = writer.Close() })
				type result struct {
					event *SSEEvent
					err   error
				}
				results := make(chan result, 1)
				var input io.Reader = source
				if oneByte {
					input = oneByteSSEReader{source}
				}
				go func() { event, err := NewSSEReader(input).ReadEvent(); results <- result{event, err} }()
				go func() {
					_, _ = io.WriteString(writer,
						"data: {\"choices\":[{\"delta\":{\"content\":\"hello\"}}]}"+ending.value)
				}()
				// The writer deliberately remains open, like a provider pausing before
				// its next token. This must not require the next frame or stream closure.
				select {
				case got := <-results:
					if got.err != nil || got.event == nil || !json.Valid(got.event.Data) {
						t.Fatalf("event=%#v, error=%v", got.event, got.err)
					}
				case <-time.After(time.Second):
					t.Fatal("complete JSON packet waited for another network frame")
				}
			})
		}
	}
}

func TestSSEReaderLimitsAssembledProviderPacket(t *testing.T) {
	// Each line is well under the existing scanner limit. The open array makes
	// these lines one unfinished JSON event rather than independent packets.
	line := "data: " + strings.Repeat("0,", 65536) + "\n"
	body := "data: [\n" + strings.Repeat(line, 9)
	event, err := NewSSEReader(strings.NewReader(body)).ReadEvent()
	if err == nil || event != nil || !strings.Contains(err.Error(), "SSE event exceeds") {
		t.Fatalf("oversize event=%#v, error=%v", event, err)
	}
}

func TestSSEReaderPendingHealthyPacketDoesNotWait(t *testing.T) {
	source, writer := io.Pipe()
	t.Cleanup(func() { _ = source.Close(); _ = writer.Close() })
	results := make(chan error, 1)
	go func() {
		reader := NewSSEReader(source)
		first, err := reader.ReadEvent()
		if err != nil {
			results <- err
			return
		}
		if string(first.Data) != "{" {
			results <- fmt.Errorf("malformed packet=%q", first.Data)
			return
		}
		second, err := reader.ReadEvent()
		if err == nil && (second == nil || string(second.Data) != `{"a":1}`) {
			err = fmt.Errorf("healthy pending packet=%#v", second)
		}
		results <- err
	}()
	go func() { _, _ = io.WriteString(writer, "data: {\ndata: {\"a\":1}\n") }()
	select {
	case err := <-results:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("healthy pending packet waited for another network frame")
	}
}

func TestSSEReaderBoundsHighlyFragmentedPacketWork(t *testing.T) {
	body := "data: [\n" + strings.Repeat("data: \n", 129) + "data: 1]\n"
	event, err := NewSSEReader(strings.NewReader(body)).ReadEvent()
	if err == nil || event != nil || !strings.Contains(err.Error(), "data lines") {
		t.Fatalf("fragmented packet must be rejected: event=%#v, error=%v", event, err)
	}
}

func TestSSEReaderAllowsBoundedFragmentedPacket(t *testing.T) {
	body := "data: [\n" + strings.Repeat("data: \n", 126) + "data: 1]\n"
	event, err := NewSSEReader(strings.NewReader(body)).ReadEvent()
	if err != nil || event == nil || !strings.HasSuffix(string(event.Data), "1]") {
		t.Fatalf("128-line packet should remain supported: event=%#v, error=%v", event, err)
	}
}
