package webhook

import (
	"strings"
	"testing"
)

func TestTokens(t *testing.T) {
	tok := NewTokens([]byte("k"))
	token := tok.Token("acme.jira", "events", 42, 0)
	if id, ok := Tenant(token); !ok || id != 42 || !tok.Verify("acme.jira", "events", token, 42, 0) {
		t.Fatalf("verify = %d, %v", id, ok)
	}
	for _, bad := range []struct{ plugin, hook, token string }{
		{"acme.other", "events", token},
		{"acme.jira", "other", token},
		{"acme.jira", "events", strings.Replace(token, "16.", "17.", 1)},
		{"acme.jira", "events", "16"},
		{"acme.jira", "events", "0." + strings.SplitN(token, ".", 2)[1]},
	} {
		if tok.Verify(bad.plugin, bad.hook, bad.token, 42, 0) {
			t.Errorf("%+v verified", bad)
		}
	}
	if NewTokens([]byte("other")).Verify("acme.jira", "events", token, 42, 0) {
		t.Fatal("another key verified the token")
	}
	if p := tok.Path("acme.jira", "events", 42, 0); !strings.HasPrefix(p, PathPrefix+"/acme.jira/events/16.") {
		t.Fatalf("path = %s", p)
	}
	// A new generation retires the URLs given before.
	next := tok.Token("acme.jira", "events", 42, 1)
	if next == token || tok.Verify("acme.jira", "events", token, 42, 1) ||
		!tok.Verify("acme.jira", "events", next, 42, 1) {
		t.Fatal("rotating did not retire the old token")
	}
}
