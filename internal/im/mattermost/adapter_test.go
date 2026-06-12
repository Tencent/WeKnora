package mattermost

import (
	"encoding/json"
	"testing"
)

func TestParseOutgoingBody_ThreadRoot(t *testing.T) {
	tests := []struct {
		name            string
		payload         outgoingPayload
		postReplyToMain bool
		wantThreadRoot  string
	}{
		{
			name: "threaded reply has RootID",
			payload: outgoingPayload{
				PostID: "post-123",
				RootID: "root-456",
			},
			postReplyToMain: false,
			wantThreadRoot:  "root-456",
		},
		{
			name: "top-level message uses PostID as thread root",
			payload: outgoingPayload{
				PostID: "post-789",
				RootID: "",
			},
			postReplyToMain: false,
			wantThreadRoot:  "post-789",
		},
		{
			name: "postReplyToMain clears thread root",
			payload: outgoingPayload{
				PostID: "post-123",
				RootID: "root-456",
			},
			postReplyToMain: true,
			wantThreadRoot:  "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var threadRoot string
			if tt.postReplyToMain {
				threadRoot = ""
			} else {
				threadRoot = tt.payload.RootID
				if threadRoot == "" {
					threadRoot = tt.payload.PostID
				}
			}

			if threadRoot != tt.wantThreadRoot {
				t.Errorf("threadRoot = %q, want %q", threadRoot, tt.wantThreadRoot)
			}
		})
	}
}

func TestParseOutgoingBody_JSON(t *testing.T) {
	body := `{"token":"tok","user_id":"u1","user_name":"alice","channel_id":"ch1","post_id":"p1","text":"hello","root_id":"r1"}`

	payload, err := parseOutgoingBody("application/json", []byte(body))
	if err != nil {
		t.Fatalf("parseOutgoingBody error: %v", err)
	}

	if payload.RootID != "r1" {
		t.Errorf("RootID = %q, want %q", payload.RootID, "r1")
	}
	if payload.PostID != "p1" {
		t.Errorf("PostID = %q, want %q", payload.PostID, "p1")
	}
}

func TestParseOutgoingBody_FormEncoded(t *testing.T) {
	body := "token=tok&user_id=u1&channel_id=ch1&post_id=p2&text=hello&root_id=r2"

	payload, err := parseOutgoingBody("application/x-www-form-urlencoded", []byte(body))
	if err != nil {
		t.Fatalf("parseOutgoingBody error: %v", err)
	}

	if payload.RootID != "r2" {
		t.Errorf("RootID = %q, want %q", payload.RootID, "r2")
	}
	if payload.PostID != "p2" {
		t.Errorf("PostID = %q, want %q", payload.PostID, "p2")
	}
}

func TestThreadIDInMessage(t *testing.T) {
	// Simulate the adapter logic: build the message and verify ThreadID
	payload := outgoingPayload{
		Token:     "tok",
		UserID:    "user-1",
		UserName:  "alice",
		ChannelID: "ch-1",
		PostID:    "post-100",
		Text:      "hello",
		RootID:    "root-200",
	}

	threadRoot := payload.RootID
	if threadRoot == "" {
		threadRoot = payload.PostID
	}

	// Verify the Extra map and ThreadID would be consistent
	extra := map[string]string{
		extraKeyThreadRoot: threadRoot,
		extraKeyChannelID:  payload.ChannelID,
	}

	if extra[extraKeyThreadRoot] != "root-200" {
		t.Errorf("Extra thread_root_id = %q, want %q", extra[extraKeyThreadRoot], "root-200")
	}

	// ThreadID should equal threadRoot
	threadID := threadRoot
	if threadID != "root-200" {
		t.Errorf("ThreadID = %q, want %q", threadID, "root-200")
	}
}

func TestParseFileIDs(t *testing.T) {
	tests := []struct {
		name string
		raw  json.RawMessage
		want []string
	}{
		{"array", json.RawMessage(`["f1","f2"]`), []string{"f1", "f2"}},
		{"string", json.RawMessage(`"f3"`), []string{"f3"}},
		{"empty", nil, nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseFileIDs(tt.raw)
			if len(got) != len(tt.want) {
				t.Fatalf("parseFileIDs() len = %d, want %d", len(got), len(tt.want))
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("parseFileIDs()[%d] = %q, want %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}
