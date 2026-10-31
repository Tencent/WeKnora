package client

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestStreamingReadersRejectIncompleteSSEFrame(t *testing.T) {
	for _, api := range []string{"agent", "knowledge", "continue"} {
		for _, tc := range []struct {
			name, tail string
			wantErr    bool
		}{
			{
				name: "truncated JSON", tail: "event: message\ndata: {\"response_type\":\"answer\",\"content\":\"cut",
				wantErr: true,
			},
			{name: "complete JSON without delimiter", tail: "event: message\ndata: {}\n", wantErr: true},
			{name: "empty data", tail: "data:\n"},
			{name: "heartbeat", tail: ": ping\n"},
			{name: "clean EOF"},
		} {
			t.Run(api+"/"+tc.name, func(t *testing.T) {
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.Header().Set("Content-Type", "text/event-stream")
					_, _ = fmt.Fprint(w, "event: message\ndata: {\"response_type\":\"answer\",\"content\":\"ok\"}\n\n")
					_, _ = fmt.Fprint(w, tc.tail)
				}))
				defer srv.Close()
				c := NewClient(srv.URL)
				count := 0
				var err error
				switch api {
				case "agent":
					err = c.AgentQAStreamWithRequest(context.Background(), "sess", &AgentQARequest{Query: "q"},
						func(*AgentStreamResponse) error { count++; return nil })
				case "knowledge":
					err = c.KnowledgeQAStream(context.Background(), "sess", &KnowledgeQARequest{Query: "q"},
						func(*StreamResponse) error { count++; return nil })
				case "continue":
					err = c.ContinueStream(context.Background(), "sess", "msg",
						func(*StreamResponse) error { count++; return nil })
				}
				if tc.wantErr && !errors.Is(err, io.ErrUnexpectedEOF) {
					t.Errorf("error = %v, want unexpected EOF", err)
				}
				if !tc.wantErr && err != nil {
					t.Errorf("error = %v, want nil", err)
				}
				if count != 1 {
					t.Errorf("callbacks = %d, want only the complete frame", count)
				}
			})
		}
	}
}
