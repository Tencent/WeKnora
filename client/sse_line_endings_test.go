package client

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/iotest"
	"time"
)

func TestStreamingReadersSSELineEndings(t *testing.T) {
	for _, api := range []string{"agent", "knowledge", "continue"} {
		for _, ending := range []string{"\n", "\r\n", "\r"} {
			t.Run(fmt.Sprintf("%s/%q", api, ending), func(t *testing.T) {
				frame := "event: message" + ending +
					`data: {"response_type":"answer","content":"hello"}` + ending + ending
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.Header().Set("Content-Type", "text/event-stream")
					_, _ = fmt.Fprint(w, frame)
				}))
				defer srv.Close()
				c := NewClient(srv.URL)
				var contents []string
				cb := func(e *StreamResponse) error { contents = append(contents, e.Content); return nil }
				var err error
				switch api {
				case "agent":
					err = c.AgentQAStreamWithRequest(context.Background(), "sess", &AgentQARequest{Query: "q"},
						func(e *AgentStreamResponse) error { contents = append(contents, e.Content); return nil })
				case "knowledge":
					err = c.KnowledgeQAStream(context.Background(), "sess", &KnowledgeQARequest{Query: "q"}, cb)
				case "continue":
					err = c.ContinueStream(context.Background(), "sess", "msg", cb)
				}
				if err != nil {
					t.Fatalf("stream failed: %v", err)
				}
				if len(contents) != 1 || contents[0] != "hello" {
					t.Fatalf("callbacks = %q", contents)
				}
			})
		}
	}
}

func TestAgentSSEFragmentedMixedLineEndings(t *testing.T) {
	frame := "event: message\r\n" + `data: {"response_type":"answer",` + "\r" +
		`data: "content":"hello"}` + "\r\n\r\n"
	count := 0
	err := (&Client{}).processAgentSSEStream(iotest.OneByteReader(strings.NewReader(frame)),
		func(e *AgentStreamResponse) error {
			count++
			if e.Content != "hello" {
				t.Errorf("content = %q", e.Content)
			}
			return nil
		})
	if err != nil || count != 1 {
		t.Fatalf("count = %d, error = %v", count, err)
	}
}

func TestAgentSSEBareCRDispatchesBeforeEOF(t *testing.T) {
	reader, writer := io.Pipe()
	defer reader.Close()
	defer writer.Close()
	stopped := errors.New("callback stopped stream")
	done := make(chan error, 1)
	go func() {
		done <- (&Client{}).processAgentSSEStream(reader, func(*AgentStreamResponse) error { return stopped })
	}()
	_, err := io.WriteString(writer, "data: {\"response_type\":\"answer\",\"content\":\"hello\"}\r\r")
	if err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if !errors.Is(err, stopped) {
			t.Fatalf("error = %v, want callback stop", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("bare CR frame waited for more bytes or EOF")
	}
}
