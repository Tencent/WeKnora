package im

import (
	"encoding/json"
	"testing"
)

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestRedactCredentialsHidesOnlySecrets(t *testing.T) {
	stored := mustJSON(t, map[string]any{"site_url": "https://mm", "bot_token": "tok", "post_to_main": true})
	got, err := RedactCredentials("mattermost", stored)
	if err != nil {
		t.Fatal(err)
	}
	if got["site_url"] != "https://mm" || got["bot_token"] != "***" || got["post_to_main"] != true {
		t.Fatalf("redacted = %v", got)
	}
}

func TestMergeCredentials(t *testing.T) {
	stored := mustJSON(t, map[string]any{
		"site_url": "https://mm", "bot_token": "tok", "outgoing_token": "out", "bot_user_id": "u1",
	})
	merge := func(update map[string]any) map[string]any {
		t.Helper()
		out, err := MergeCredentials("mattermost", stored, mustJSON(t, update))
		if err != nil {
			t.Fatal(err)
		}
		var m map[string]any
		if err := json.Unmarshal(out, &m); err != nil {
			t.Fatal(err)
		}
		return m
	}

	// An edit that never loaded the credentials must not wipe them.
	if got := merge(map[string]any{}); got["bot_token"] != "tok" || got["site_url"] != "https://mm" {
		t.Fatalf("empty update changed credentials: %v", got)
	}
	// The redacted form comes back with one plain field changed.
	got := merge(map[string]any{
		"site_url": "https://new", "bot_token": "***", "outgoing_token": "", "bot_user_id": "",
	})
	want := map[string]any{"site_url": "https://new", "bot_token": "tok", "outgoing_token": "out", "bot_user_id": ""}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %v, want %v (all: %v)", k, got[k], v, got)
		}
	}
	// A new secret replaces the stored one.
	got = merge(map[string]any{"bot_token": "new-tok"})
	if got["bot_token"] != "new-tok" || got["outgoing_token"] != "out" {
		t.Fatalf("secret replacement = %v", got)
	}
}
