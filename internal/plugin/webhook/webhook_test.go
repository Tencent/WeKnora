package webhook

import (
	"strings"
	"testing"
)

func TestTokens(t *testing.T) {
	tok := NewTokens([]byte("k"))
	token := tok.Token("acme.jira", "events", 42)
	if id, ok := tok.Verify("acme.jira", "events", token); !ok || id != 42 {
		t.Fatalf("verify = %d, %v", id, ok)
	}
	for _, bad := range []struct{ plugin, hook, token string }{
		{"acme.other", "events", token},
		{"acme.jira", "other", token},
		{"acme.jira", "events", strings.Replace(token, "16.", "17.", 1)},
		{"acme.jira", "events", "16"},
		{"acme.jira", "events", "0." + strings.SplitN(token, ".", 2)[1]},
	} {
		if _, ok := tok.Verify(bad.plugin, bad.hook, bad.token); ok {
			t.Errorf("%+v verified", bad)
		}
	}
	if _, ok := NewTokens([]byte("other")).Verify("acme.jira", "events", token); ok {
		t.Fatal("another key verified the token")
	}
	if p := tok.Path("acme.jira", "events", 42); !strings.HasPrefix(p, PathPrefix+"/acme.jira/events/16.") {
		t.Fatalf("path = %s", p)
	}
}
